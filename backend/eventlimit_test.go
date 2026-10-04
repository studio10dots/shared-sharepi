package main

import (
	"strings"
	"testing"
)

func uploadBody(ev, kind string) string {
	return `{"event_id":"` + ev + `","photo_id":"` + pid + `","ext":"jpg","kinds":["` + kind + `"]}`
}

func TestAnEventCannotHoldMoreThanTheLimit(t *testing.T) {
	e := newEnv(t) // the test config limits an event to 5 items
	ids := []string{
		"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444",
		"55555555-5555-4555-8555-555555555555",
	}
	for _, id := range ids[:4] {
		e.seedItem("fullevent1", id, "jpg", "u-alice", testNow)
	}
	// Room for one more.
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", uploadBody("fullevent1", "original")), 200)

	e.seedItem("fullevent1", ids[4], "jpg", "u-alice", testNow)
	*e.clock = e.clock.Add(countTTL + 1) // the remembered count is stale now
	w := e.do("POST", "/v1/groups/"+group+"/uploads", "alice", uploadBody("fullevent1", "thumbnail"))
	want(t, w, 409)
	if !strings.Contains(w.Body.String(), "event_full") {
		t.Errorf("body = %s", w.Body)
	}
	// Another event is not affected.
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", uploadBody("otherevent1", "original")), 200)
}
