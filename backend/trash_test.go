package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func (e *env) trash(t *testing.T, who string) []trashObject {
	t.Helper()
	w := e.do("GET", "/v1/groups/"+group+"/trash", who, "")
	want(t, w, 200)
	return decodeBody[struct{ Objects []trashObject }](t, w).Objects
}

func TestTrashListsOnlyDeletedMediaOfTheGroup(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	other := "grp_other/bbq00001/original/" + pid + ".jpg"
	e.st.put(other, 1, nil, testNow)
	e.st.put(group+"/bbq00001/event.json", 0, nil, testNow)
	e.st.put(group+"/invites/abc", 0, nil, testNow)
	e.st.put(group+"/reports/"+pid+".json", 5, nil, testNow)
	// Things that must never show up in a member's trash.
	for _, n := range []string{other, group + "/bbq00001/event.json", group + "/invites/abc", group + "/reports/" + pid + ".json"} {
		_ = e.st.Delete(context.Background(), n)
	}
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "alice", ""), 204)

	got := e.trash(t, "alice")
	if len(got) != 3 { // the photo's original, medium and thumbnail
		t.Fatalf("trash = %+v", got)
	}
	for _, o := range got {
		if !strings.HasPrefix(o.Path, group+"/") || !mediaRe.MatchString(o.Path) || o.Generation == "" || o.DeletedAt == "" {
			t.Errorf("unexpected trash entry %+v", o)
		}
	}
	want(t, e.do("GET", "/v1/groups/"+group+"/trash", "carol", ""), 404)
	want(t, e.do("GET", "/v1/groups/"+group+"/trash", "bob", ""), 404)
	want(t, e.do("GET", "/v1/groups/"+group+"/trash", "", ""), 401)
}

func TestRestoreBringsAnItemBack(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "alice", ""), 204)
	items := func() int {
		return len(decodeBody[struct{ Items []item }](t, e.do("GET", evPath("bbq00001")+"/items", "alice", "")).Items)
	}
	if items() != 0 {
		t.Fatal("item should be gone")
	}
	body, _ := json.Marshal(map[string]any{"objects": e.trash(t, "alice")})
	w := e.do("POST", "/v1/groups/"+group+"/trash/restore", "alice", string(body))
	want(t, w, 200)
	if n := decodeBody[map[string]int](t, w)["restored"]; n != 3 {
		t.Errorf("restored = %d", n)
	}
	if items() != 1 {
		t.Error("restored item is not listed again")
	}
	if len(e.trash(t, "alice")) != 0 {
		t.Error("restored objects are still in the trash")
	}
	// The items are live again, so a repeat finds them already there: nothing to
	// restore, which is not an error (a past-retention version is the 404 case).
	w = e.do("POST", "/v1/groups/"+group+"/trash/restore", "alice", string(body))
	want(t, w, 200)
	if n := decodeBody[map[string]int](t, w)["restored"]; n != 0 {
		t.Errorf("a repeated restore restored %d", n)
	}
}

type orderStore struct {
	*memStore
	order []string
}

func (o *orderStore) Restore(ctx context.Context, n string, g Version) error {
	o.order = append(o.order, n)
	return o.memStore.Restore(ctx, n, g)
}

func TestRestoreOriginalComesLast(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "alice", ""), 204)
	objs := e.trash(t, "alice")
	for i, o := range objs { // hand them over with the original first
		if strings.Contains(o.Path, "/original/") {
			objs[0], objs[i] = objs[i], objs[0]
		}
	}
	rec := &orderStore{memStore: e.st}
	e.s.store = rec
	body, _ := json.Marshal(map[string]any{"objects": objs})
	want(t, e.do("POST", "/v1/groups/"+group+"/trash/restore", "alice", string(body)), 200)
	if len(rec.order) != 3 || !strings.Contains(rec.order[2], "/original/") {
		t.Errorf("restore order = %v", rec.order)
	}
}

func TestRestoreRejectsAnythingOutsideTheGroupsMedia(t *testing.T) {
	e := newEnv(t)
	good := group + "/bbq00001/thumbnail/" + pid + ".jpg"
	for name, obj := range map[string]string{
		"another group": "grp_other/bbq00001/thumbnail/" + pid + ".jpg",
		"roster":        group + "/members.json",
		"secret":        "_backend/secret.json",
		"report":        group + "/reports/" + pid + ".json",
		"traversal":     group + "/../x/thumbnail/" + pid + ".jpg",
	} {
		body := fmt.Sprintf(`{"objects":[{"path":%q,"generation":"5"}]}`, obj)
		if w := e.do("POST", "/v1/groups/"+group+"/trash/restore", "alice", body); w.Code != 422 {
			t.Errorf("%s: got %d, want 422", name, w.Code)
		}
	}
	for name, body := range map[string]string{
		"no generation": fmt.Sprintf(`{"objects":[{"path":%q,"generation":""}]}`, good),
		"empty":         `{"objects":[]}`,
	} {
		if w := e.do("POST", "/v1/groups/"+group+"/trash/restore", "alice", body); w.Code != 422 {
			t.Errorf("%s: got %d, want 422", name, w.Code)
		}
	}
	// A version is opaque (a GCS generation or an S3 ETag), so its format is not
	// checked; one that names no deleted object simply restores nothing.
	for name, body := range map[string]string{
		"unknown generation": fmt.Sprintf(`{"objects":[{"path":%q,"generation":"x"}]}`, good),
		"zero":               fmt.Sprintf(`{"objects":[{"path":%q,"generation":"0"}]}`, good),
	} {
		if w := e.do("POST", "/v1/groups/"+group+"/trash/restore", "alice", body); w.Code != 404 {
			t.Errorf("%s: got %d, want 404", name, w.Code)
		}
	}
	want(t, e.do("POST", "/v1/groups/"+group+"/trash/restore", "carol", fmt.Sprintf(`{"objects":[{"path":%q,"generation":"5"}]}`, good)), 404)
}

func TestTrashNamesTheEventAndServesTheThumbnail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	_, _ = e.st.Write(ctx, eventFilePath(group, "bbq00001"), []byte(`{"name":"BBQ","date":"2026/05/01"}`), nil)
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "alice", ""), 204)

	w := e.do("GET", "/v1/groups/"+group+"/trash", "alice", "")
	want(t, w, 200)
	got := decodeBody[struct {
		Objects []trashObject
		Events  []trashEvent
	}](t, w)
	if len(got.Events) != 1 || got.Events[0].Name != "BBQ" || got.Events[0].Restore != nil {
		t.Fatalf("live event: %+v", got.Events)
	}

	// Delete the whole event: the name still comes back, with the file to restore.
	_ = e.st.Delete(ctx, eventFilePath(group, "bbq00001"))
	w = e.do("GET", "/v1/groups/"+group+"/trash", "alice", "")
	got = decodeBody[struct {
		Objects []trashObject
		Events  []trashEvent
	}](t, w)
	if len(got.Events) != 1 || got.Events[0].Name != "BBQ" || got.Events[0].Restore == nil {
		t.Fatalf("deleted event: %+v", got.Events)
	}
	body, _ := json.Marshal(map[string]any{"objects": []trashObject{*got.Events[0].Restore}})
	want(t, e.do("POST", "/v1/groups/"+group+"/trash/restore", "alice", string(body)), 200)
	if !e.st.has(eventFilePath(group, "bbq00001")) {
		t.Error("event.json was not restored")
	}

	for _, o := range got.Objects {
		if strings.Contains(o.Path, "/thumbnail/") {
			w = e.do("GET", "/v1/groups/"+group+"/trash/thumbnail?path="+o.Path+"&generation="+o.Generation, "alice", "")
			want(t, w, 200)
		}
	}
	want(t, e.do("GET", "/v1/groups/"+group+"/trash/thumbnail?path="+group+"/bbq00001/original/"+pid+".jpg&generation=1", "alice", ""), 422)
}
