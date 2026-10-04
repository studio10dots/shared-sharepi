package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateShareSignsOriginalAndMediumAndWritesManifest(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)

	body := `{"items":[{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg"}]}`
	w := e.do("POST", "/v1/groups/"+group+"/shares", "alice", body)
	want(t, w, 200)
	res := decodeBody[struct {
		ShareID   string `json:"share_id"`
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
	}](t, w)
	if res.ShareID == "" || res.URL == "" || res.ExpiresAt == "" {
		t.Fatalf("incomplete response: %+v", res)
	}
	if !strings.Contains(res.URL, group+"/shares/"+res.ShareID+".share.json") {
		t.Fatalf("url does not point at the manifest: %s", res.URL)
	}

	manifestName := group + "/shares/" + res.ShareID + ".share.json"
	if !e.st.has(manifestName) {
		t.Fatalf("manifest object not written: %s", manifestName)
	}
	raw, _, err := e.st.Read(context.Background(), manifestName)
	if err != nil {
		t.Fatal(err)
	}
	var manifest shareManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(manifest.Items))
	}
	item := manifest.Items[0]
	if item.Path != obj(group, "bbq00001", "original", pid, "jpg") {
		t.Fatalf("unexpected path: %s", item.Path)
	}
	if item.MediaType != "image" {
		t.Fatalf("want image, got %s", item.MediaType)
	}
	if !strings.Contains(item.OriginalURL, "original/"+pid+".jpg") {
		t.Fatalf("original url: %s", item.OriginalURL)
	}
	if !strings.Contains(item.MediumURL, "medium/"+pid+".jpg") {
		t.Fatalf("medium url: %s", item.MediumURL)
	}
}

func TestCreateShareVideoMediaType(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	e.seedItem("bbq00001", pid, "mp4", "u-alice", testNow)

	body := `{"items":[{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"mp4"}],"ttl_minutes":5}`
	w := e.do("POST", "/v1/groups/"+group+"/shares", "alice", body)
	want(t, w, 200)
}

func TestCreateShareRejectsBadInput(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")

	cases := []string{
		`{"items":[]}`,
		`{"items":[{"event_id":"bbq00001","photo_id":"not-a-uuid","ext":"jpg"}]}`,
		`{"items":[{"event_id":"invites","photo_id":"` + pid + `","ext":"jpg"}]}`,
		`{"items":[{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg"}],"ttl_minutes":7}`,
	}
	for _, body := range cases {
		w := e.do("POST", "/v1/groups/"+group+"/shares", "alice", body)
		want(t, w, 422)
	}
}

func TestCreateShareRejectsTooManyItems(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	items := ""
	for i := 0; i < maxShareItems+1; i++ {
		if items != "" {
			items += ","
		}
		items += `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg"}`
	}
	w := e.do("POST", "/v1/groups/"+group+"/shares", "alice", `{"items":[`+items+`]}`)
	want(t, w, 422)
}

func TestCreateShareRequiresMembership(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	body := `{"items":[{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg"}]}`
	// bob's row is "removed" in newEnv's fixture roster.
	w := e.do("POST", "/v1/groups/"+group+"/shares", "bob", body)
	want(t, w, 404)
}
