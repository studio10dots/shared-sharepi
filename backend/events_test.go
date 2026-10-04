package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func obj(g, ev, kind, id, ext string) string {
	return g + "/" + ev + "/" + kind + "/" + id + "." + ext
}

// seedItem stores an item the way an upload would: derivatives, then the original.
func (e *env) seedItem(ev, id, ext, uploader string, at time.Time) {
	meta := map[string]string{"uploader": uploader}
	e.st.put(obj(group, ev, "thumbnail", id, "jpg"), 30, meta, at)
	if !videoExts[ext] {
		e.st.put(obj(group, ev, "medium", id, "jpg"), 300, meta, at)
	}
	e.st.put(obj(group, ev, "original", id, ext), 3000, meta, at)
}

// seedEvent writes a valid event.json, which is what makes an event exist.
func (e *env) seedEvent(ev, name, date string) {
	_, _ = e.st.Write(context.Background(), group+"/"+ev+"/event.json", marshalEventFile(name, date), nil)
}

func evPath(ev string) string { return "/v1/groups/" + group + "/events/" + ev }

func (e *env) events(t *testing.T, tok string) []event {
	t.Helper()
	w := e.do("GET", "/v1/groups/"+group+"/events", tok, "")
	want(t, w, 200)
	return decodeBody[struct{ Events []event }](t, w).Events
}

func (e *env) eventFile(t *testing.T, ev string) eventFile {
	t.Helper()
	b, _, err := e.st.Read(context.Background(), group+"/"+ev+"/event.json")
	if err != nil {
		t.Fatalf("event.json of %s: %v", ev, err)
	}
	var f eventFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestListEventsReadsNameAndDateAndIgnoresStrays(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	e.seedEvent("bbq00002", "BBQ", "2026/09/23") // same name and day: the id breaks the tie
	e.seedEvent("skiday01", "Ski", "2026/01/05")
	e.seedEvent("newyear01", "海 と 山", "2025/12/31")
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	// Things that are not events.
	e.st.put(group+"/invites/abc", 0, nil, testNow)
	e.st.put(group+"/reports/"+pid+".json", 5, nil, testNow)
	e.st.put(group+"/2026/09/23/oldevent/original/"+pid+".jpg", 1, nil, testNow) // the old dated layout
	e.st.put(group+"/2026/09/23/oldevent/event.json", 0, nil, testNow)
	e.st.put(group+"/orphan001/thumbnail/"+pid+".jpg", 1, nil, testNow) // no event.json
	e.st.put(group+"/nodate0001/event.json", 0, nil, testNow)           // empty
	_, _ = e.st.Write(ctx, group+"/olddate01/event.json", []byte(`{"name":"Old"}`), nil)
	_, _ = e.st.Write(ctx, group+"/baddate01/event.json", []byte(`{"name":"Bad","date":"2026/02/31"}`), nil)
	_, _ = e.st.Write(ctx, group+"/badname01/event.json", []byte(`{"name":"","date":"2026/02/01"}`), nil)
	_, _ = e.st.Write(ctx, group+"/notjson01/event.json", []byte(`nope`), nil)
	e.seedEvent("Upper0001", "Upper", "2026/01/01") // not a valid id

	got := e.events(t, "alice")
	wantEv := []event{
		{"2025/12/31", "newyear01", "海 と 山"}, {"2026/01/05", "skiday01", "Ski"},
		{"2026/09/23", "bbq00001", "BBQ"}, {"2026/09/23", "bbq00002", "BBQ"},
	}
	if len(got) != len(wantEv) {
		t.Fatalf("events = %+v", got)
	}
	for i := range wantEv {
		if got[i] != wantEv[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], wantEv[i])
		}
	}
	want(t, e.do("GET", "/v1/groups/"+group+"/events", "carol", ""), 404)
	want(t, e.do("GET", "/v1/groups/"+group+"/events", "bob", ""), 404)
}

func TestListEventsSortsByDateThenName(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("aaaaaaa01", "Zebra", "2026/01/01")
	e.seedEvent("aaaaaaa02", "Apple", "2026/01/02")
	e.seedEvent("aaaaaaa03", "Mango", "2026/01/01")
	var ids []string
	for _, ev := range e.events(t, "alice") {
		ids = append(ids, ev.ID)
	}
	if strings.Join(ids, ",") != "aaaaaaa03,aaaaaaa01,aaaaaaa02" {
		t.Errorf("order = %v", ids)
	}
}

func TestCreateEventWritesNameAndDateKeepingTheFirst(t *testing.T) {
	e := newEnv(t)
	url := "/v1/groups/" + group + "/events"
	w := e.do("POST", url, "alice", `{"date":"2026/09/23","id":"picnic001","name":"Picnic"}`)
	want(t, w, 200)
	if got := decodeBody[event](t, w); got != (event{"2026/09/23", "picnic001", "Picnic"}) {
		t.Errorf("first = %+v", got)
	}
	if f := e.eventFile(t, "picnic001"); f != (eventFile{"Picnic", "2026/09/23"}) {
		t.Errorf("event.json = %+v", f)
	}
	// A retry with other values returns the stored ones and changes nothing.
	w = e.do("POST", url, "alice", `{"date":"2026/10/01","id":"picnic001","name":"Other"}`)
	want(t, w, 200)
	if got := decodeBody[event](t, w); got != (event{"2026/09/23", "picnic001", "Picnic"}) {
		t.Errorf("second = %+v", got)
	}
	if f := e.eventFile(t, "picnic001"); f != (eventFile{"Picnic", "2026/09/23"}) {
		t.Errorf("event.json after retry = %+v", f)
	}
	for _, bad := range []string{
		`{"date":"2026/09/23","id":"picnic002","name":"a\u0007"}`, `{"date":"2026/09/23","id":"picnic002","name":"  "}`,
		`{"date":"23-09-2026","id":"picnic002","name":"ok"}`, `{"date":"2026/02/31","id":"picnic002","name":"ok"}`,
		`{"date":"2026/09/23","id":"picnic002","name":"` + strings.Repeat("x", 65) + `"}`,
		`{"date":"2026/09/23","name":"no id"}`,
	} {
		want(t, e.do("POST", url, "alice", bad), 422)
	}
	want(t, e.do("POST", url, "carol", `{"date":"2026/09/23","id":"picnic003","name":"Picnic"}`), 404)
	// Event names may be longer than group names: 64 characters, not bytes.
	long := strings.Repeat("あ", 64)
	want(t, e.do("POST", url, "alice", `{"date":"2026/09/23","id":"picnic004","name":"`+long+`"}`), 200)
	if e.st.has(group + "/picnic002/event.json") {
		t.Error("an invalid request wrote an event")
	}
}

func TestUpdateEvent(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	put := func(tok, id, body string) *httptest.ResponseRecorder { return e.do("PUT", evPath(id), tok, body) }

	w := put("alice", "bbq00001", `{"name":"Barbecue"}`)
	want(t, w, 200)
	if got := decodeBody[event](t, w); got != (event{"2026/09/23", "bbq00001", "Barbecue"}) {
		t.Errorf("name only = %+v", got)
	}
	w = put("alice", "bbq00001", `{"date":"2026/09/30"}`)
	want(t, w, 200)
	if got := decodeBody[event](t, w); got != (event{"2026/09/30", "bbq00001", "Barbecue"}) {
		t.Errorf("date only = %+v", got)
	}
	w = put("alice", "bbq00001", `{"name":"Party","date":"2026/10/01"}`)
	want(t, w, 200)
	if got := decodeBody[event](t, w); got != (event{"2026/10/01", "bbq00001", "Party"}) {
		t.Errorf("both = %+v", got)
	}
	if f := e.eventFile(t, "bbq00001"); f != (eventFile{"Party", "2026/10/01"}) {
		t.Errorf("event.json = %+v", f)
	}
	for name, body := range map[string]string{
		"neither": `{}`, "bad date": `{"date":"2026/02/31"}`, "date format": `{"date":"2026-02-01"}`,
		"bad name": `{"name":"  "}`, "long name": `{"name":"` + strings.Repeat("x", 65) + `"}`, "bad json": `x`,
	} {
		if w := put("alice", "bbq00001", body); w.Code != 422 {
			t.Errorf("%s: got %d, want 422", name, w.Code)
		}
	}
	want(t, put("alice", "nosuch001", `{"name":"x"}`), 404)
	e.st.put(group+"/orphan001/thumbnail/"+pid+".jpg", 1, nil, testNow) // photos but no event.json
	want(t, put("alice", "orphan001", `{"name":"x"}`), 404)
	want(t, put("carol", "bbq00001", `{"name":"x"}`), 404)
	want(t, put("bob", "bbq00001", `{"name":"x"}`), 404)
	if f := e.eventFile(t, "bbq00001"); f != (eventFile{"Party", "2026/10/01"}) {
		t.Errorf("a refused call changed the event: %+v", f)
	}
}

// racingStore lets a test write to the store between a handler's Read of an
// event.json (or of the objects ending in suffix) and its Write, the way a
// second device would.
type racingStore struct {
	*memStore
	hook   func()
	suffix string
}

func (r *racingStore) Read(ctx context.Context, n string) ([]byte, Version, error) {
	b, g, err := r.memStore.Read(ctx, n)
	suffix := r.suffix
	if suffix == "" {
		suffix = "/event.json"
	}
	if r.hook != nil && strings.HasSuffix(n, suffix) {
		h := r.hook
		r.hook = nil
		h()
	}
	return b, g, err
}

func TestConcurrentNameAndDateEditsBothSurvive(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	rs := &racingStore{memStore: e.st}
	e.s.store = rs
	// While the date edit is between its read and its write, a name edit lands.
	rs.hook = func() {
		if w := e.do("PUT", evPath("bbq00001"), "alice", `{"name":"Renamed"}`); w.Code != 200 {
			t.Errorf("inner edit = %d", w.Code)
		}
	}
	want(t, e.do("PUT", evPath("bbq00001"), "alice", `{"date":"2026/12/24"}`), 200)
	if f := e.eventFile(t, "bbq00001"); f != (eventFile{"Renamed", "2026/12/24"}) {
		t.Errorf("event.json = %+v: an edit was lost", f)
	}
}

func TestUploadWithEventDateAndNameCreatesEventJSON(t *testing.T) {
	e := newEnv(t)
	url := "/v1/groups/" + group + "/uploads"
	body := func(ev, extra string) string {
		return `{"event_id":"` + ev + `",` + extra + `"photo_id":"` + pid + `","ext":"jpg","kinds":["thumbnail"]}`
	}
	both := `"event_date":"2026/09/23","event_name":"Picnic",`
	want(t, e.do("POST", url, "alice", body("newevent1", both)), 200)
	if f := e.eventFile(t, "newevent1"); f != (eventFile{"Picnic", "2026/09/23"}) {
		t.Errorf("event.json = %+v", f)
	}
	// Create-only: a later upload does not undo a rename.
	want(t, e.do("PUT", evPath("newevent1"), "alice", `{"name":"Renamed"}`), 200)
	want(t, e.do("POST", url, "alice", body("newevent1", both)), 200)
	if f := e.eventFile(t, "newevent1"); f.Name != "Renamed" {
		t.Errorf("upload overwrote the name: %+v", f)
	}
	// Without both valid fields no event.json is written, and the upload still works.
	for i, extra := range []string{``, `"event_name":"Only name",`, `"event_date":"2026/09/23",`, `"event_date":"2026/02/31","event_name":"x",`} {
		ev := "noevent" + string(rune('a'+i)) + "1"
		want(t, e.do("POST", url, "alice", body(ev, extra)), 200)
		if e.st.has(group + "/" + ev + "/event.json") {
			t.Errorf("case %d wrote an event.json", i)
		}
	}
}

func TestEventIDsAreRefusedOnEveryRoute(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	e.st.put(group+"/invites/abc", 0, nil, testNow)
	e.st.put(group+"/reports/"+pid+".json", 5, nil, testNow)
	for _, id := range []string{"invites", "reports", "short", "Upper0001", "UPPERCASE", "a/b", "..", "abc_defgh", strings.Repeat("a", 33)} {
		enc := strings.ReplaceAll(id, "/", "%2F")
		for _, c := range []struct{ method, path, body string }{
			{"PUT", evPath(enc), `{"name":"x"}`},
			{"DELETE", evPath(enc), ""},
			{"GET", evPath(enc) + "/items", ""},
			{"DELETE", evPath(enc) + "/items/" + pid, ""},
		} {
			// The router itself redirects paths containing "..", so no handler runs then.
			if w := e.do(c.method, c.path, "alice", c.body); w.Code != 422 && w.Code != 404 && w.Code != 307 && w.Code != 301 {
				t.Errorf("%s %s: %d", c.method, c.path, w.Code)
			}
		}
		post := func(url, body string) {
			if w := e.do("POST", url, "alice", body); w.Code != 422 {
				t.Errorf("POST %s %s: %d %s", url, body, w.Code, w.Body)
			}
		}
		post("/v1/groups/"+group+"/events", `{"date":"2026/09/23","name":"x","id":"`+id+`"}`)
		post("/v1/groups/"+group+"/uploads", `{"event_id":"`+id+`","photo_id":"`+pid+`","ext":"jpg","kinds":["original"]}`)
		post("/v1/groups/"+group+"/reports", `{"reason":"spam","target":{"type":"item","event":"`+id+`","photo_id":"`+pid+`"}}`)
	}
	// Nothing was created or removed.
	if len(e.events(t, "alice")) != 1 || !e.st.has(group+"/invites/abc") || !e.st.has(group+"/reports/"+pid+".json") {
		t.Error("a refused id changed the bucket")
	}
	if e.st.has(group+"/invites/event.json") || e.st.has(group+"/reports/event.json") {
		t.Error("an event.json was written into a reserved folder")
	}
}

func TestListItemsOldestFirstWithUploader(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid2, "mp4", "u-bob", testNow.Add(time.Hour)) // uploaded later
	e.seedItem("bbq00001", pid, "JPG", "u-alice", testNow)
	e.st.put(obj(group, "bbq00001", "original", "not-a-uuid", "jpg"), 1, nil, testNow) // ignored
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")                                       // ignored as an item

	w := e.do("GET", evPath("bbq00001")+"/items", "alice", "")
	want(t, w, 200)
	items := decodeBody[struct{ Items []item }](t, w).Items
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].PhotoID != pid || items[0].MediaType != "image" || items[0].Uploader != "u-alice" || items[0].Size != 3000 {
		t.Errorf("first = %+v", items[0])
	}
	if items[1].PhotoID != pid2 || items[1].MediaType != "video" || items[1].Ext != "mp4" || items[1].Uploader != "u-bob" {
		t.Errorf("second = %+v", items[1])
	}
	want(t, e.do("GET", evPath("bbq00001")+"/items", "carol", ""), 404)
	want(t, e.do("GET", evPath("bbq00001")+"/items", "bob", ""), 404)
}

func TestListItemsIsScopedToTheEvent(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	e.seedItem("bbq000012", pid2, "jpg", "u-alice", testNow) // shares an id prefix
	items := decodeBody[struct{ Items []item }](t, e.do("GET", evPath("bbq00001")+"/items", "alice", "")).Items
	if len(items) != 1 || items[0].PhotoID != pid {
		t.Errorf("items = %+v", items)
	}
}

func TestDeleteItemRemovesAllThreeObjects(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	e.seedItem("bbq00001", pid2, "mp4", "u-alice", testNow) // a video has no medium
	del := func(id, tok string) int {
		return e.do("DELETE", evPath("bbq00001")+"/items/"+id, tok, "").Code
	}
	if c := del(pid, "carol"); c != 404 {
		t.Errorf("stranger got %d", c)
	}
	if !e.st.has(obj(group, "bbq00001", "original", pid, "jpg")) {
		t.Fatal("a stranger deleted a photo")
	}
	if c := del(pid, "alice"); c != 204 {
		t.Fatalf("delete = %d", c)
	}
	if c := del(pid2, "alice"); c != 204 {
		t.Fatalf("delete video = %d", c)
	}
	if left := e.st.names(group + "/bbq00001/"); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
	if c := del(pid, "alice"); c != 404 {
		t.Errorf("deleting twice = %d", c)
	}
	if c := del("not-a-uuid", "alice"); c != 422 {
		t.Errorf("bad id = %d", c)
	}
}

func TestDeleteEventRemovesEverythingUnderItOnly(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	e.seedItem("bbq00001", pid2, "mp4", "u-alice", testNow)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	e.seedItem("bbq000012", pid, "jpg", "u-alice", testNow) // a different event with a shared id prefix
	e.seedEvent("bbq000012", "BBQ 2", "2026/09/23")
	other := "grp_other/bbq00001/original/" + pid + ".jpg"
	e.st.put(other, 1, nil, testNow) // another group's event of the same id

	want(t, e.do("DELETE", evPath("bbq00001"), "carol", ""), 404)
	w := e.do("DELETE", evPath("bbq00001"), "alice", "")
	want(t, w, 200)
	if n := decodeBody[map[string]int](t, w)["deleted"]; n != 6 { // 2 thumbs + 1 medium + 2 originals + event.json
		t.Errorf("deleted = %d", n)
	}
	if left := e.st.names(group + "/bbq00001/"); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
	if !e.st.has(obj(group, "bbq000012", "original", pid, "jpg")) || !e.st.has(other) {
		t.Error("deleted something outside the event")
	}
	// The event is gone from the listing, the sibling stays.
	got := e.events(t, "alice")
	if len(got) != 1 || got[0].Name != "BBQ 2" {
		t.Errorf("events = %+v", got)
	}
}

func TestListItemsOrderByTakenAtAndReportIt(t *testing.T) {
	e := newEnv(t)
	// Uploaded first, but taken last; the other has no capture time.
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	e.st.put(obj(group, "bbq00001", "original", pid, "jpg"), 3000, map[string]string{"uploader": "u-alice", "taken-at": "2026-09-26T10:00:00Z"}, testNow)
	e.seedItem("bbq00001", pid2, "jpg", "u-bob", testNow.Add(time.Hour))
	w := e.do("GET", evPath("bbq00001")+"/items", "alice", "")
	want(t, w, 200)
	items := decodeBody[struct{ Items []item }](t, w).Items
	if len(items) != 2 || items[0].PhotoID != pid2 || items[1].PhotoID != pid {
		t.Fatalf("items = %+v", items)
	}
	if items[1].TakenAt != "2026-09-26T10:00:00Z" || items[0].TakenAt != items[0].Uploaded {
		t.Errorf("taken_at = %q / %q", items[1].TakenAt, items[0].TakenAt)
	}
}

func TestUploadsSignTakenAtOnTheOriginalOnly(t *testing.T) {
	e := newEnv(t)
	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":["thumbnail","original"],"taken_at":"2024-05-01T12:00:00+09:00"}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 200)
	if got := e.sg.calls[0].Metadata["taken-at"]; got != "" {
		t.Errorf("thumbnail carries taken-at %q", got)
	}
	if got := e.sg.calls[1].Metadata["taken-at"]; got != "2024-05-01T03:00:00Z" {
		t.Errorf("original taken-at = %q", got)
	}
	e.sg.calls = nil
	bad := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":["original"],"taken_at":"1970-01-01T00:00:00Z"}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", bad), 200)
	if got := e.sg.calls[0].Metadata["taken-at"]; got != "" {
		t.Errorf("implausible taken-at was signed: %q", got)
	}
}

func TestUploadsSignCompressedOnTheOriginalOnly(t *testing.T) {
	e := newEnv(t)
	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"mp4","kinds":["thumbnail","medium","original"],"compressed":true}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 200)
	for i, wantV := range []string{"", "", "true"} {
		if got, ok := e.sg.calls[i].Metadata["compressed"]; got != wantV || (wantV == "" && ok) {
			t.Errorf("call %d compressed = %q", i, got)
		}
	}
	for _, b := range []string{
		`{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"mp4","kinds":["original"],"compressed":false}`,
		`{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"mp4","kinds":["original"]}`,
	} {
		e.sg.calls = nil
		want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", b), 200)
		if _, ok := e.sg.calls[0].Metadata["compressed"]; ok {
			t.Errorf("compressed signed for %s", b)
		}
	}
}

func TestListItemsReportsCompressed(t *testing.T) {
	e := newEnv(t)
	e.st.put(obj(group, "bbq00001", "original", pid, "mp4"), 3000, map[string]string{"uploader": "u-alice", "compressed": "true"}, testNow)
	e.st.put(obj(group, "bbq00001", "original", pid2, "mp4"), 3000, map[string]string{"uploader": "u-alice"}, testNow.Add(time.Hour))
	w := e.do("GET", evPath("bbq00001")+"/items", "alice", "")
	want(t, w, 200)
	if strings.Count(w.Body.String(), `"compressed":true`) != 1 || strings.Contains(w.Body.String(), `"compressed":false`) {
		t.Errorf("body = %s", w.Body.String())
	}
	items := decodeBody[struct{ Items []item }](t, w).Items
	if len(items) != 2 || !items[0].Compressed || items[1].Compressed {
		t.Errorf("items = %+v", items)
	}
}
