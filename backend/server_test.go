package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPingNeedsNoToken(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("GET", "/ping", "", ""), 200)
}

func TestAuthRejections(t *testing.T) {
	e := newEnv(t)
	for name, tok := range map[string]string{"none": "", "garbage": "x", "unverified email": "unverf"} {
		if w := e.do("GET", "/v1/me", tok, ""); w.Code != 401 {
			t.Errorf("%s: got %d, want 401", name, w.Code)
		}
	}
	if len(e.sg.calls) != 0 {
		t.Error("signed something for an unauthenticated caller")
	}
}

func TestMeReportsAdminCaseInsensitively(t *testing.T) {
	e := newEnv(t)
	got := decodeBody[struct {
		IsAdmin bool `json:"is_admin"`
	}](t, e.do("GET", "/v1/me", "admin", ""))
	if !got.IsAdmin {
		t.Error("admin not recognised")
	}
	got = decodeBody[struct {
		IsAdmin bool `json:"is_admin"`
	}](t, e.do("GET", "/v1/me", "alice", ""))
	if got.IsAdmin {
		t.Error("alice must not be admin")
	}
}

func TestStrangersAndRemovedGet404(t *testing.T) {
	e := newEnv(t)
	body := `{"paths":["` + group + `/bbq00001/thumbnail/` + pid + `.jpg"]}`
	for _, tok := range []string{"carol", "bob"} {
		want(t, e.do("POST", "/v1/groups/"+group+"/downloads", tok, body), 404)
	}
	// A group that does not exist looks identical to one the caller is not in.
	want(t, e.do("POST", "/v1/groups/nope/downloads", "alice", body), 404)
	want(t, e.do("POST", "/v1/groups/_backend/downloads", "alice", body), 404)
	if len(e.sg.calls) != 0 {
		t.Error("signed for a non-member")
	}
}

func TestDownloadsSignBatchForMember(t *testing.T) {
	e := newEnv(t)
	p1 := group + "/bbq00001/thumbnail/" + pid + ".jpg"
	p2 := group + "/bbq00001/original/" + pid + ".mp4"
	w := e.do("POST", "/v1/groups/"+group+"/downloads", "alice", `{"paths":["`+p1+`","`+p2+`"]}`)
	want(t, w, 200)
	res := decodeBody[struct {
		URLs map[string]string `json:"urls"`
	}](t, w)
	if len(res.URLs) != 2 || res.URLs[p1] == "" || res.URLs[p2] == "" {
		t.Errorf("urls = %v", res.URLs)
	}
	for _, c := range e.sg.calls {
		if c.Method != "GET" || c.Expires != time.Hour {
			t.Errorf("bad sign request %+v", c)
		}
	}
}

func TestDownloadsRejectPathsOutsideTheGroup(t *testing.T) {
	e := newEnv(t)
	bad := []string{
		"other/bbq00001/thumbnail/" + pid + ".jpg",
		group + "/members.json",
		group + "/invites/abc",
		"_backend/secret.json",
		group + "/bbq00001/../x/original/" + pid + ".jpg",
		group + "/bbq00001/thumbnail/" + pid + ".jpg/../..",
		group + "/bbq00001/event.json",
	}
	for _, p := range bad {
		if w := e.do("POST", "/v1/groups/"+group+"/downloads", "alice", `{"paths":["`+p+`"]}`); w.Code != 422 {
			t.Errorf("%q: got %d, want 422", p, w.Code)
		}
	}
	if len(e.sg.calls) != 0 {
		t.Error("signed a rejected path")
	}
}

func TestDownloadBatchLimit(t *testing.T) {
	e := newEnv(t)
	paths := make([]string, maxBatch+1)
	for i := range paths {
		paths[i] = `"` + group + "/bbq00001/thumbnail/" + pid + `.jpg"`
	}
	want(t, e.do("POST", "/v1/groups/"+group+"/downloads", "alice", `{"paths":[`+strings.Join(paths, ",")+`]}`), http.StatusUnprocessableEntity)
}

func TestUploadsSignUploaderHeaderFromRoster(t *testing.T) {
	e := newEnv(t)
	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"mp4","kinds":["thumbnail","original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 200)
	if len(e.sg.calls) != 2 {
		t.Fatalf("sign calls = %d", len(e.sg.calls))
	}
	for _, c := range e.sg.calls {
		if c.Method != "PUT" {
			t.Errorf("method %s", c.Method)
		}
		// The uploader comes from the roster, not from anything the client sent.
		if c.Metadata["uploader"] != "u-alice" {
			t.Errorf("uploader metadata = %q", c.Metadata["uploader"])
		}
		if c.MaxSize != 1000 {
			t.Errorf("max size = %d", c.MaxSize)
		}
	}
	if !strings.HasSuffix(e.sg.calls[1].Object, "/original/"+pid+".mp4") || !strings.HasSuffix(e.sg.calls[0].Object, "/thumbnail/"+pid+".jpg") {
		t.Errorf("objects = %q %q", e.sg.calls[0].Object, e.sg.calls[1].Object)
	}
}

func TestUploadsValidateInput(t *testing.T) {
	e := newEnv(t)
	for name, body := range map[string]string{
		"event with slash": `{"event_id":"a/b","photo_id":"` + pid + `","ext":"jpg","kinds":["original"]}`,
		"only dots":        `{"event_id":"........","photo_id":"` + pid + `","ext":"jpg","kinds":["original"]}`,
		"reserved id":      `{"event_id":"invites","photo_id":"` + pid + `","ext":"jpg","kinds":["original"]}`,
		"bad photo id":     `{"event_id":"bbq00001","photo_id":"x","ext":"jpg","kinds":["original"]}`,
		"bad kind":         `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":["members"]}`,
		"no kinds":         `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":[]}`,
	} {
		if w := e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body); w.Code != 422 {
			t.Errorf("%s: got %d, want 422", name, w.Code)
		}
	}
	if len(e.sg.calls) != 0 {
		t.Error("signed for invalid input")
	}
}

func TestUploadsSignTheOriginalsRealContentType(t *testing.T) {
	e := newEnv(t)
	for ext, want := range map[string]string{
		"jpg": "image/jpeg", "png": "image/png", "heic": "image/heic",
		"mp4": "video/mp4", "mov": "video/quicktime", "xyz": "application/octet-stream",
	} {
		body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"` + ext + `","kinds":["original","thumbnail"]}`
		e.sg.calls = nil
		w := e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", ext, w.Code, w.Body)
		}
		if got := e.sg.calls[0].ContentType; got != want {
			t.Errorf("original .%s signed as %q, want %q", ext, got, want)
		}
		if got := e.sg.calls[1].ContentType; got != "image/jpeg" {
			t.Errorf("thumbnail signed as %q, want image/jpeg", got)
		}
	}
}
