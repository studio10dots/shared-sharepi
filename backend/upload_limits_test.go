package main

import (
	"strings"
	"testing"
)

// limitedEnv is newEnv with the tighter per-kind limits main.go sets, and a
// second active member (carol) next to alice.
func limitedEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.s.cfg.MaxUploadSize = 5000
	e.s.cfg.MaxThumbnailSize = 100
	e.s.cfg.MaxMediumSize = 1000
	e.s.cfg.MaxImageSize = 2000
	e.putRoster(group, "BBQ", []Member{
		{UserID: "u-alice", Sub: "sub-alice", Nickname: "A", Status: "active"},
		{UserID: "u-carol", Sub: "sub-carol", Nickname: "C", Status: "active"},
	})
	return e
}

func signedMax(e *env, kind string) int64 {
	for _, c := range e.sg.calls {
		if strings.Contains(c.Object, "/"+kind+"/") {
			return c.MaxSize
		}
	}
	return -1
}

func TestUploadURLsAreBoundToTheSizeOfWhatTheyCarry(t *testing.T) {
	e := limitedEnv(t)
	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":["thumbnail","medium","original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 200)
	if got := signedMax(e, "thumbnail"); got != 100 {
		t.Errorf("thumbnail limit = %d, want 100", got)
	}
	if got := signedMax(e, "medium"); got != 1000 {
		t.Errorf("medium limit = %d, want 1000", got)
	}
	if got := signedMax(e, "original"); got != 2000 {
		t.Errorf("photo original limit = %d, want 2000", got)
	}
}

func TestAVideoOriginalKeepsTheOverallLimit(t *testing.T) {
	e := limitedEnv(t)
	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"mp4","kinds":["original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 200)
	if got := signedMax(e, "original"); got != 5000 {
		t.Errorf("video original limit = %d, want 5000", got)
	}
}

func TestATighterLimitNeverExceedsTheOverallOne(t *testing.T) {
	e := limitedEnv(t)
	e.s.cfg.MaxUploadSize = 500 // lower than the medium and photo limits
	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":["medium","original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 200)
	for _, k := range []string{"medium", "original"} {
		if got := signedMax(e, k); got != 500 {
			t.Errorf("%s limit = %d, want 500", k, got)
		}
	}
}

func TestUploadsAskForAtMostOneURLPerKind(t *testing.T) {
	e := limitedEnv(t)
	many := make([]string, 100000)
	for i := range many {
		many[i] = `"original"`
	}
	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":[` + strings.Join(many, ",") + `]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 422)
	if len(e.sg.calls) != 0 {
		t.Errorf("%d URLs were signed for a refused request", len(e.sg.calls))
	}

	// Three entries is fine, and a repeat of one kind is signed once.
	dup := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":["original","original","original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", dup), 200)
	if len(e.sg.calls) != 1 {
		t.Errorf("signed %d URLs for one kind asked three times", len(e.sg.calls))
	}

	// Four is more than an item has kinds.
	four := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":["original","medium","thumbnail","original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", four), 422)
}

func TestAMemberCannotReplaceAnotherMembersItem(t *testing.T) {
	e := limitedEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)

	for _, kind := range []string{"original", "medium", "thumbnail"} {
		w := e.do("POST", "/v1/groups/"+group+"/uploads", "carol", uploadBody("bbq00001", kind))
		want(t, w, 409)
		if !strings.Contains(w.Body.String(), "item_exists") {
			t.Errorf("%s: body = %s", kind, w.Body)
		}
	}
	if len(e.sg.calls) != 0 {
		t.Errorf("%d URLs were signed over someone else's item", len(e.sg.calls))
	}

	// The same photo id under another extension is the same item: not allowed either.
	png := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"png","kinds":["original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "carol", png), 409)

	// Another photo id, or another event, is free.
	other := `{"event_id":"bbq00001","photo_id":"` + pid2 + `","ext":"jpg","kinds":["original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "carol", other), 200)
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "carol", uploadBody("bbq00002", "original")), 200)
}

func TestTheUploaderMayRetryItsOwnUpload(t *testing.T) {
	e := limitedEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	for _, kind := range []string{"original", "medium", "thumbnail"} {
		want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", uploadBody("bbq00001", kind)), 200)
	}
}

func TestAnObjectWithNoKnownUploaderIsNotReplaced(t *testing.T) {
	e := limitedEnv(t)
	e.st.put(obj(group, "bbq00001", "original", pid, "jpg"), 10, nil, testNow)
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", uploadBody("bbq00001", "original")), 409)
}

func TestPhotoLimitFallsBackToTheOverallOneWhenUnset(t *testing.T) {
	e := newEnv(t) // MaxUploadSize 1000 and no per-kind limits
	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":["thumbnail","medium","original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 200)
	for _, c := range e.sg.calls {
		if c.MaxSize != 1000 {
			t.Errorf("%s limit = %d, want 1000", c.Object, c.MaxSize)
		}
	}
}
