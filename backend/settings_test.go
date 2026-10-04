package main

import (
	"context"
	"testing"
	"time"
)

type settingsBody struct {
	Version int           `json:"version"`
	Video   VideoSettings `json:"video"`
}

func getSettingsAs(t *testing.T, e *env, token string) settingsBody {
	t.Helper()
	w := e.do("GET", "/v1/settings", token, "")
	want(t, w, 200)
	return decodeBody[settingsBody](t, w)
}

func TestSettingsDefaultToFiveMinutesAndAskingOverTwo(t *testing.T) {
	e := newEnv(t)
	// Anyone signed in can read the limits (a member of no group too: the app
	// reads them before an upload, and they say nothing private).
	got := getSettingsAs(t, e, "carol")
	want := VideoSettings{MaxSeconds: 300, ConfirmSeconds: 120}
	if got.Video != want {
		t.Errorf("defaults = %+v, want %+v", got.Video, want)
	}
}

func TestSettingsNeedASignedInCaller(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("GET", "/v1/settings", "", ""), 401)
	want(t, e.do("GET", "/v1/settings", "unverf", ""), 401)
	want(t, e.do("PUT", "/v1/settings", "", `{"video":{}}`), 401)
}

func TestOnlyAdministratorsChangeSettings(t *testing.T) {
	e := newEnv(t)
	body := `{"video":{"max_seconds":600,"max_bytes":1000,"confirm_seconds":60,"confirm_bytes":500}}`

	want(t, e.do("PUT", "/v1/settings", "alice", body), 403) // a member
	want(t, e.do("PUT", "/v1/settings", "carol", body), 403) // a stranger
	if got := getSettingsAs(t, e, "alice").Video; got.MaxSeconds != 300 {
		t.Fatalf("a refused change was saved: %+v", got)
	}

	w := e.do("PUT", "/v1/settings", "admin", body)
	want(t, w, 200)
	saved := VideoSettings{MaxSeconds: 600, MaxBytes: 1000, ConfirmSeconds: 60, ConfirmBytes: 500}
	if got := decodeBody[settingsBody](t, w).Video; got != saved {
		t.Errorf("answer = %+v", got)
	}
	// Seen at once, not after the cache runs out.
	if got := getSettingsAs(t, e, "alice").Video; got != saved {
		t.Errorf("read back = %+v", got)
	}
}

func TestSettingsCanBeChangedAgainAndSwitchedOff(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("PUT", "/v1/settings", "admin", `{"video":{"max_seconds":600}}`), 200)
	want(t, e.do("PUT", "/v1/settings", "admin", `{"video":{}}`), 200)

	// Zero everywhere is "no limit", which is not the same as never configured.
	if got := getSettingsAs(t, e, "alice").Video; got != (VideoSettings{}) {
		t.Errorf("limits = %+v, want none", got)
	}
}

func TestSettingsAreValidated(t *testing.T) {
	e := newEnv(t)
	for name, body := range map[string]string{
		"negative seconds":       `{"video":{"max_seconds":-1}}`,
		"over a day":             `{"video":{"max_seconds":86401}}`,
		"negative bytes":         `{"video":{"max_bytes":-1}}`,
		"over the backend limit": `{"video":{"max_bytes":1001}}`, // test backend: 1000
		"confirm above max time": `{"video":{"max_seconds":60,"confirm_seconds":61}}`,
		"confirm above max size": `{"video":{"max_bytes":100,"confirm_bytes":101}}`,
		"not json":               `nope`,
	} {
		t.Run(name, func(t *testing.T) {
			want(t, e.do("PUT", "/v1/settings", "admin", body), 422)
		})
	}
	if got := getSettingsAs(t, e, "alice").Video; got.MaxSeconds != 300 {
		t.Errorf("an invalid change was saved: %+v", got)
	}
}

func TestOwnersVideoSizeLimitIsSignedIntoTheUploadOfAnOriginalVideo(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("PUT", "/v1/settings", "admin", `{"video":{"max_bytes":400}}`), 200)

	upload := func(ext string) []SignRequest {
		e.sg.calls = nil
		body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"` + ext + `","kinds":["thumbnail","medium","original"]}`
		want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 200)
		return e.sg.calls
	}

	video := upload("mp4")
	if video[2].Object[len(video[2].Object)-4:] != ".mp4" || video[2].MaxSize != 400 {
		t.Errorf("video original = %+v, want MaxSize 400", video[2])
	}
	if video[0].MaxSize != 1000 || video[1].MaxSize != 1000 {
		t.Errorf("a video's derivatives must keep the backend's limit: %+v %+v", video[0], video[1])
	}
	if photo := upload("jpg"); photo[2].MaxSize != 1000 {
		t.Errorf("a photo is not limited by the video setting: %+v", photo[2])
	}
}

func TestAHigherOwnerLimitNeverRaisesTheBackendsOwn(t *testing.T) {
	e := newEnv(t)
	e.s.cfg.MaxUploadSize = 300 // lower than what the owner allows
	// Saved straight to the bucket: the endpoint itself refuses this value.
	b := []byte(`{"version":1,"video":{"max_bytes":900}}`)
	if _, err := e.st.Write(context.Background(), settingsObject, b, nil); err != nil {
		t.Fatal(err)
	}

	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"mp4","kinds":["original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 200)

	if got := e.sg.calls[0].MaxSize; got != 300 {
		t.Errorf("MaxSize = %d, want the backend's own 300", got)
	}
}

func TestAnUnreadableSettingsObjectIsNotSilentlyTheDefaults(t *testing.T) {
	e := newEnv(t)
	if _, err := e.st.Write(context.Background(), settingsObject, []byte("{broken"), nil); err != nil {
		t.Fatal(err)
	}

	want(t, e.do("GET", "/v1/settings", "alice", ""), 502)
	body := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"mp4","kinds":["original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", body), 502)
	// A photo does not depend on the video settings.
	photo := `{"event_id":"bbq00001","photo_id":"` + pid + `","ext":"jpg","kinds":["original"]}`
	want(t, e.do("POST", "/v1/groups/"+group+"/uploads", "alice", photo), 200)
}

func TestSettingsReadIsCachedForAShortTime(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("PUT", "/v1/settings", "admin", `{"video":{"max_seconds":600}}`), 200)
	if got := getSettingsAs(t, e, "alice").Video.MaxSeconds; got != 600 {
		t.Fatalf("max_seconds = %d", got)
	}

	// Another instance writes behind this one's back.
	other := []byte(`{"version":1,"video":{"max_seconds":900}}`)
	if _, err := e.st.Write(context.Background(), settingsObject, other, nil); err != nil {
		t.Fatal(err)
	}
	if got := getSettingsAs(t, e, "alice").Video.MaxSeconds; got != 600 {
		t.Errorf("read before the cache ran out = %d, want the cached 600", got)
	}

	*e.clock = e.clock.Add(settingsTTL + time.Second)
	if got := getSettingsAs(t, e, "alice").Video.MaxSeconds; got != 900 {
		t.Errorf("read after the cache ran out = %d, want 900", got)
	}
}
