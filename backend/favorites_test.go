package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const favURL = "/v1/groups/" + group + "/favorites"

func favOpsBody(ops ...string) string { return `{"ops":[` + strings.Join(ops, ",") + `]}` }

func itemSetOp(pid, ev string, folders ...string) string {
	f, _ := json.Marshal(folders)
	return `{"op":"item_set","photo_id":"` + pid + `","event":"` + ev + `","path":"` + group + `/` + ev +
		`/original/` + pid + `.jpg","folders":` + string(f) + `,"added_at":"2026-09-25T12:00:00Z"}`
}

func (e *env) favs(t *testing.T, tok string) favDoc {
	t.Helper()
	w := e.do("GET", favURL, tok, "")
	want(t, w, 200)
	return decodeBody[favDoc](t, w)
}

func TestFavoritesStartEmptyAndAreMembersOnly(t *testing.T) {
	e := newEnv(t)
	d := e.favs(t, "alice")
	if len(d.Folders) != 0 || len(d.Items) != 0 || d.Folders == nil || d.Items == nil {
		t.Errorf("empty doc = %+v", d)
	}
	// A stranger and a removed member are answered as if the group did not exist.
	want(t, e.do("GET", favURL, "carol", ""), 404)
	want(t, e.do("GET", favURL, "bob", ""), 404)
	want(t, e.do("POST", favURL+"/ops", "carol", favOpsBody(`{"op":"folder_order","ids":[]}`)), 404)
	want(t, e.do("GET", favURL, "", ""), 401)
}

func TestFavoriteFoldersAndItems(t *testing.T) {
	e := newEnv(t)
	w := e.do("POST", favURL+"/ops", "alice", favOpsBody(
		`{"op":"folder_create","id":"aaaaaaaa1","name":"Travel"}`,
		`{"op":"folder_create","id":"bbbbbbbb2","name":"Food"}`,
		itemSetOp(pid, "evt00001", "default", "aaaaaaaa1"),
		itemSetOp(pid2, "evt00001", "bbbbbbbb2"),
	))
	want(t, w, 200)
	d := decodeBody[favDoc](t, w)
	if len(d.Folders) != 2 || d.Folders[0].Name != "Travel" || len(d.Items) != 2 {
		t.Fatalf("doc = %+v", d)
	}
	if got := strings.Join(d.Items[0].Folders, ","); got != "default,aaaaaaaa1" {
		t.Errorf("item folders = %s", got)
	}
	if d.Items[0].AddedAt != "2026-09-25T12:00:00Z" {
		t.Errorf("added_at = %s", d.Items[0].AddedAt)
	}
	// It is stored under the member's own folder, and a second member does not see it.
	if !e.st.has(group + "/members/u-alice/favorites.json") {
		t.Error("favorites are not under members/{user_id}/")
	}
	e.putRoster(group, "BBQ", []Member{
		{UserID: "u-alice", Sub: e.hash(t, "sub-alice"), Nickname: "A", Status: "active"},
		{UserID: "u-dave", Sub: e.hash(t, "sub-dave"), Nickname: "D", Status: "active"},
	})
	if d := e.favs(t, "dave"); len(d.Items) != 0 || len(d.Folders) != 0 {
		t.Errorf("another member sees %+v", d)
	}

	// Rename and reorder; an unknown id in the order is ignored, unlisted ones follow.
	d = decodeBody[favDoc](t, e.do("POST", favURL+"/ops", "alice", favOpsBody(
		`{"op":"folder_rename","id":"aaaaaaaa1","name":"Trips"}`,
		`{"op":"folder_order","ids":["bbbbbbbb2","zzzzzzzz9"]}`,
	)))
	if d.Folders[0].ID != "bbbbbbbb2" || d.Folders[1].Name != "Trips" {
		t.Errorf("after rename/order = %+v", d.Folders)
	}
	// Deleting a folder takes it out of the items; an item left nowhere goes.
	d = decodeBody[favDoc](t, e.do("POST", favURL+"/ops", "alice", favOpsBody(
		`{"op":"folder_delete","id":"bbbbbbbb2"}`,
	)))
	if len(d.Folders) != 1 || len(d.Items) != 1 || d.Items[0].PhotoID != pid {
		t.Errorf("after delete = %+v", d)
	}
	// An empty folder list removes the item.
	d = decodeBody[favDoc](t, e.do("POST", favURL+"/ops", "alice", favOpsBody(itemSetOp(pid, "evt00001"))))
	if len(d.Items) != 0 {
		t.Errorf("item not removed: %+v", d.Items)
	}
}

func TestFavoriteOpsAreIdempotentAndTolerant(t *testing.T) {
	e := newEnv(t)
	batch := favOpsBody(
		`{"op":"folder_create","id":"aaaaaaaa1","name":"Travel"}`,
		itemSetOp(pid, "evt00001", "aaaaaaaa1", "ffffffff0"), // the second folder does not exist
	)
	for range 2 { // a retried batch changes nothing
		want(t, e.do("POST", favURL+"/ops", "alice", batch), 200)
	}
	d := e.favs(t, "alice")
	if len(d.Folders) != 1 || len(d.Items) != 1 || strings.Join(d.Items[0].Folders, ",") != "aaaaaaaa1" {
		t.Errorf("doc = %+v", d)
	}
	// A rename or delete of a folder another device already deleted is not an error.
	want(t, e.do("POST", favURL+"/ops", "alice", favOpsBody(
		`{"op":"folder_rename","id":"gone0000","name":"x"}`,
		`{"op":"folder_delete","id":"gone0000"}`,
		itemSetOp(pid2, "evt00001"), // removing what was never there
	)), 200)
}

func TestFavoriteOpsAreRefusedWholeWhenOneIsInvalid(t *testing.T) {
	e := newEnv(t)
	bad := []string{
		`{"op":"nope"}`,
		`{"op":"folder_create","id":"short","name":"x"}`,
		`{"op":"folder_create","id":"default","name":"x"}`,
		`{"op":"folder_create","id":"aaaaaaaa1","name":"  "}`,
		`{"op":"folder_create","id":"aaaaaaaa1","name":"` + strings.Repeat("x", 31) + `"}`,
		`{"op":"folder_create","id":"aaaaaaaa1","name":"a\u0007"}`,
		itemSetOp("not-a-uuid", "evt00001", "default"),
		itemSetOp(pid, "members", "default"), // a reserved folder is not an event
		itemSetOp(pid, "invites", "default"),
		itemSetOp(pid, "evt00001", "BAD"),
		// a path outside the group, or another photo's
		`{"op":"item_set","photo_id":"` + pid + `","event":"evt00001","path":"other/evt00001/original/` + pid + `.jpg","folders":["default"]}`,
		`{"op":"item_set","photo_id":"` + pid + `","event":"evt00001","path":"` + group + `/evt00001/original/` + pid2 + `.jpg","folders":["default"]}`,
		`{"op":"item_set","photo_id":"` + pid + `","event":"evt00001","path":"` + group + `/evt00001/thumbnail/` + pid + `.jpg","folders":["default"]}`,
	}
	for _, op := range bad {
		// A valid operation first: nothing of it may be written.
		w := e.do("POST", favURL+"/ops", "alice", favOpsBody(`{"op":"folder_create","id":"cccccccc3","name":"ok"}`, op))
		if w.Code != 422 {
			t.Errorf("%s: got %d, want 422", op, w.Code)
		}
	}
	want(t, e.do("POST", favURL+"/ops", "alice", `{"ops":[]}`), 422)
	want(t, e.do("POST", favURL+"/ops", "alice", `x`), 422)
	if d := e.favs(t, "alice"); len(d.Folders) != 0 {
		t.Errorf("a refused batch wrote %+v", d)
	}
}

func TestFavoriteLimits(t *testing.T) {
	e := newEnv(t)
	var ops []string
	for i := range maxFavFolders + 1 {
		ops = append(ops, `{"op":"folder_create","id":"f`+strings.Repeat("0", 6)+string(rune('a'+i/26))+string(rune('a'+i%26))+`","name":"n"}`)
	}
	want(t, e.do("POST", favURL+"/ops", "alice", favOpsBody(ops[:maxFavFolders]...)), 200)
	want(t, e.do("POST", favURL+"/ops", "alice", favOpsBody(ops[maxFavFolders])), 422)
	over := make([]string, maxFavOps+1)
	for i := range over {
		over[i] = `{"op":"folder_order","ids":[]}`
	}
	want(t, e.do("POST", favURL+"/ops", "alice", favOpsBody(over...)), 422)
}

func TestFavoritesOfTwoDevicesBothSurvive(t *testing.T) {
	e := newEnv(t)
	rs := &racingStore{memStore: e.st}
	e.s.store = rs
	// While the first batch is between its read and its write, a second device
	// writes: the first must retry on the new generation, not overwrite it.
	rs.hook = func() {
		_, err := e.st.Write(context.Background(), group+"/members/u-alice/favorites.json",
			[]byte(`{"version":1,"folders":[{"id":"bbbbbbbb2","name":"Other device"}],"items":[]}`), nil)
		if err != nil {
			t.Error(err)
		}
	}
	rs.suffix = "/favorites.json"
	want(t, e.do("POST", favURL+"/ops", "alice", favOpsBody(`{"op":"folder_create","id":"aaaaaaaa1","name":"Mine"}`)), 200)
	d := e.favs(t, "alice")
	if len(d.Folders) != 2 {
		t.Errorf("a concurrent write was lost: %+v", d.Folders)
	}
}

func TestLeavingDeletesTheMembersFavorites(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("POST", favURL+"/ops", "alice", favOpsBody(`{"op":"folder_create","id":"aaaaaaaa1","name":"Travel"}`)), 200)
	want(t, e.do("POST", "/v1/groups/"+group+"/leave", "alice", ""), 204)
	if e.st.has(group + "/members/u-alice/favorites.json") {
		t.Error("favorites outlived the member")
	}
}

func TestMembersIsAReservedFolderNotAnEvent(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("POST", favURL+"/ops", "alice", favOpsBody(`{"op":"folder_create","id":"aaaaaaaa1","name":"Travel"}`)), 200)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	// members/ holds objects, but it is never listed as an event ...
	for _, ev := range e.events(t, "alice") {
		if ev.ID == "members" {
			t.Errorf("members/ listed as an event")
		}
	}
	// ... and no route accepts it as one.
	if validEventID("members") {
		t.Error("members is a valid event id")
	}
	want(t, e.do("POST", "/v1/groups/"+group+"/events", "alice", `{"date":"2026/09/23","id":"members","name":"x"}`), 422)
	// The trash lists media only, so favorites cannot be listed or restored there.
	e.st.put(group+"/members/u-alice/favorites.json", 10, nil, testNow)
	_ = e.st.Delete(context.Background(), group+"/members/u-alice/favorites.json")
	w := e.do("GET", "/v1/groups/"+group+"/trash", "alice", "")
	want(t, w, 200)
	if strings.Contains(w.Body.String(), "members/") {
		t.Errorf("the trash exposes members/: %s", w.Body)
	}
}
