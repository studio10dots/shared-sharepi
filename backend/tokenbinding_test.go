package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// callAt sends GET /v1/me as the given token to the backend reached at host.
func (e *env) callAt(host, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/v1/me", strings.NewReader(""))
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, req)
	return w
}

// boundEnv adds tokens whose nonce binds them to one host, and one with no nonce.
func boundEnv(t *testing.T, require bool) *env {
	t.Helper()
	e := newEnv(t)
	e.s.cfg.RequireTokenBinding = require
	v := e.s.verifier.(fakeVerifier)
	v["bound-a"] = Identity{Sub: "sub-alice", Email: "alice@example.com", EmailVerified: true, Nonce: tokenBinding("a.example.run.app")}
	v["bound-b"] = Identity{Sub: "sub-alice", Email: "alice@example.com", EmailVerified: true, Nonce: tokenBinding("b.example.run.app")}
	v["unbound"] = Identity{Sub: "sub-alice", Email: "alice@example.com", EmailVerified: true}
	v["junk"] = Identity{Sub: "sub-alice", Email: "alice@example.com", EmailVerified: true, Nonce: "not-a-binding"}
	return e
}

func TestATokenBoundToAnotherBackendIsRefused(t *testing.T) {
	for _, require := range []bool{false, true} {
		e := boundEnv(t, require)
		// The token a member sent to backend A, replayed against backend B.
		want(t, e.callAt("b.example.run.app", "bound-a"), 401)
		want(t, e.callAt("a.example.run.app", "bound-b"), 401)
		// A nonce that is not a binding at all is refused too.
		want(t, e.callAt("a.example.run.app", "junk"), 401)
	}
}

func TestATokenBoundToThisBackendIsAccepted(t *testing.T) {
	for _, require := range []bool{false, true} {
		e := boundEnv(t, require)
		want(t, e.callAt("a.example.run.app", "bound-a"), 200)
		want(t, e.callAt("b.example.run.app", "bound-b"), 200)
	}
}

func TestTheHostIsComparedWithoutRegardToCase(t *testing.T) {
	e := boundEnv(t, true)
	want(t, e.callAt("A.Example.Run.App", "bound-a"), 200)
}

func TestADefaultPortInTheHostIsIgnored(t *testing.T) {
	e := boundEnv(t, true)
	want(t, e.callAt("a.example.run.app:443", "bound-a"), 200)
	if tokenBinding("a.example.run.app:443") != tokenBinding("a.example.run.app") ||
		tokenBinding("a.example.run.app:80") != tokenBinding("a.example.run.app") {
		t.Error("a default port changed the binding")
	}
	if tokenBinding("a.example.run.app:8080") == tokenBinding("a.example.run.app") {
		t.Error("a non-default port did not change the binding")
	}
}

func TestATokenWithNoNonceIsAcceptedOnlyUntilBindingIsRequired(t *testing.T) {
	want(t, boundEnv(t, false).callAt("a.example.run.app", "unbound"), 200)
	want(t, boundEnv(t, true).callAt("a.example.run.app", "unbound"), 401)
}

func TestBindingIsBoundToTheHostIncludingItsPort(t *testing.T) {
	e := boundEnv(t, true)
	e.s.verifier.(fakeVerifier)["bound-port"] = Identity{Sub: "sub-alice", Email: "alice@example.com", EmailVerified: true, Nonce: tokenBinding("127.0.0.1:8080")}
	want(t, e.callAt("127.0.0.1:8080", "bound-port"), 200)
	want(t, e.callAt("127.0.0.1:9090", "bound-port"), 401)
}

// The value an app has to compute, fixed so the two sides cannot drift apart.
func TestTokenBindingValue(t *testing.T) {
	// openssl: printf 'sharepi-backend:backend.example.com' | openssl dgst -sha256 -binary
	//          | base64 | tr '+/' '-_' | tr -d '='
	const wantValue = "AGIXpX5NC1tyeFkQuazuUD9YPaRXN8nT36quMkWrA2o"
	if got := tokenBinding("backend.example.com"); got != wantValue {
		t.Fatalf("binding = %q, want %q", got, wantValue)
	}
}
