package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestBackendTag(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{
			name: "picks the highest semver, ignoring unrelated tags",
			body: `[{"name":"backend-v1.2.0"},{"name":"backend-v1.10.0"},{"name":"backend-v1.9.9"},{"name":"web-v9.9.9"},{"name":"v1.99.0"}]`,
			want: "1.10.0",
			ok:   true,
		},
		{
			name: "no matching tag",
			body: `[{"name":"web-v1.0.0"}]`,
			want: "",
			ok:   false,
		},
		{
			name: "empty list",
			body: `[]`,
			want: "",
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			got, ok := latestBackendTagFrom(t, srv.URL)
			if ok != c.ok || got != c.want {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestLatestBackendTagHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, ok := latestBackendTagFrom(t, srv.URL); ok {
		t.Fatal("want ok=false on a non-200 response")
	}
}

// latestBackendTagFrom calls latestBackendTag against a test server's own URL
// path (github.com/repos/<repo>/tags is baked into latestBackendTag, so the
// test points "repo" at the test server's own host:port instead).
func latestBackendTagFrom(t *testing.T, serverURL string) (string, bool) {
	t.Helper()
	return latestBackendTagAt(context.Background(), http.DefaultClient, serverURL)
}

func TestCheckForUpdateAsyncSkipsDevBuilds(t *testing.T) {
	e := newEnv(t)
	// version defaults to "dev" and repo to "" in tests (no ldflags), so the
	// check must not even try to reach the network.
	e.s.checkForUpdateAsync(http.DefaultClient)
	if n := e.s.consumeUpdateNotice(); n != nil {
		t.Fatalf("dev build must never produce an update notice, got %+v", n)
	}
}

func TestOpenReportsCarriesUpdateNoticeOnce(t *testing.T) {
	e := newEnv(t)
	e.s.updateNotice.Store(&UpdateNotice{Version: "1.4.0"})

	w := e.do("GET", "/v1/reports/open", "admin", "")
	want(t, w, 200)
	got := decodeBody[struct {
		UpdateAvailable *UpdateNotice `json:"update_available"`
	}](t, w)
	if got.UpdateAvailable == nil || got.UpdateAvailable.Version != "1.4.0" {
		t.Fatalf("want update_available.version=1.4.0, got %+v", got.UpdateAvailable)
	}

	w2 := e.do("GET", "/v1/reports/open", "admin", "")
	want(t, w2, 200)
	got2 := decodeBody[struct {
		UpdateAvailable *UpdateNotice `json:"update_available"`
	}](t, w2)
	if got2.UpdateAvailable != nil {
		t.Fatalf("want the notice consumed after the first call, got %+v", got2.UpdateAvailable)
	}
}

func TestOpenReportsOmitsUpdateNoticeWhenNoneIsPending(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/v1/reports/open", "admin", "")
	want(t, w, 200)
	got := decodeBody[struct {
		UpdateAvailable *UpdateNotice `json:"update_available"`
	}](t, w)
	if got.UpdateAvailable != nil {
		t.Fatalf("want no update_available, got %+v", got.UpdateAvailable)
	}
}
