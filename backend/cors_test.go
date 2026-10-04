package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func (e *env) doWithOrigin(method, path, token, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, req)
	return w
}

func TestCORSDisabledByDefault(t *testing.T) {
	e := newEnv(t) // WebOrigin is unset
	w := e.doWithOrigin("GET", "/ping", "", "https://anything.example")
	want(t, w, 200)
	if h := w.Header().Get("Access-Control-Allow-Origin"); h != "" {
		t.Fatalf("want no CORS header when WebOrigin is unset, got %q", h)
	}
}

func TestCORSAllowsOnlyTheConfiguredWebOrigin(t *testing.T) {
	e := newEnv(t)
	e.s.cfg.WebOrigin = "https://chamagon-web-xyz.run.app"

	allowed := e.doWithOrigin("GET", "/ping", "", e.s.cfg.WebOrigin)
	want(t, allowed, 200)
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != e.s.cfg.WebOrigin {
		t.Fatalf("got Access-Control-Allow-Origin=%q, want %q", got, e.s.cfg.WebOrigin)
	}

	denied := e.doWithOrigin("GET", "/ping", "", "https://some-other-owners-web.run.app")
	want(t, denied, 200) // the request itself still succeeds...
	if h := denied.Header().Get("Access-Control-Allow-Origin"); h != "" {
		// ...but a browser would discard the response: no matching CORS header.
		t.Fatalf("want no CORS header for a foreign origin, got %q", h)
	}
}

func TestCORSPreflightNoMatchGetsNoAllowHeaderAndNeverReachesAHandler(t *testing.T) {
	e := newEnv(t)
	e.s.cfg.WebOrigin = "https://chamagon-web-xyz.run.app"

	w := e.doWithOrigin(
		http.MethodOptions,
		"/v1/groups/"+group+"/reports",
		"",
		"https://some-other-owners-web.run.app",
	)
	want(t, w, http.StatusNoContent)
	if h := w.Header().Get("Access-Control-Allow-Origin"); h != "" {
		t.Fatalf("want no CORS header for a foreign origin's preflight, got %q", h)
	}
}

func TestCORSPreflightForTheConfiguredOriginIsAllowed(t *testing.T) {
	e := newEnv(t)
	e.s.cfg.WebOrigin = "https://chamagon-web-xyz.run.app"

	w := e.doWithOrigin(http.MethodOptions, "/v1/reports/open", "", e.s.cfg.WebOrigin)
	want(t, w, http.StatusNoContent)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != e.s.cfg.WebOrigin {
		t.Fatalf("got %q, want %q", got, e.s.cfg.WebOrigin)
	}
	if w.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Fatal("want Access-Control-Allow-Headers to be set (Authorization must be allowed)")
	}
}

func TestCORSIgnoresRequestsWithNoOrigin(t *testing.T) {
	// The mobile app, server-to-server calls and pentest tooling send no
	// Origin header at all; CORS must never interfere with them.
	e := newEnv(t)
	e.s.cfg.WebOrigin = "https://chamagon-web-xyz.run.app"
	w := e.doWithOrigin("GET", "/v1/me", "admin", "")
	want(t, w, 200)
	if h := w.Header().Get("Access-Control-Allow-Origin"); h != "" {
		t.Fatalf("want no CORS header without an Origin header, got %q", h)
	}
}
