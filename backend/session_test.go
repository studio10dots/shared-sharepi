package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sessionReply struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// sessionFor asks the backend at host for a session as the given ID token.
func (e *env) sessionFor(t *testing.T, host, token string) sessionReply {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/session", strings.NewReader(""))
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, req)
	want(t, w, 200)
	return decodeBody[sessionReply](t, w)
}

func (e *env) meAt(host, token string) *httptest.ResponseRecorder {
	return e.callAt(host, token)
}

func TestASessionTokenStandsForTheCallerWhoMadeIt(t *testing.T) {
	e := boundEnv(t, true)
	s := e.sessionFor(t, "a.example.run.app", "bound-a")

	if !strings.HasPrefix(s.Token, sessionPrefix) {
		t.Fatalf("token %q has no session prefix", s.Token)
	}
	want(t, e.meAt("a.example.run.app", s.Token), 200)
}

func TestASessionNeedsNoGoogleTokenAndSurvivesWhenGoogleWouldRefuse(t *testing.T) {
	e := boundEnv(t, true)
	s := e.sessionFor(t, "a.example.run.app", "bound-a")

	// The ID token is gone (expired): the session still works until its own end.
	delete(e.s.verifier.(fakeVerifier), "bound-a")
	want(t, e.meAt("a.example.run.app", s.Token), 200)
}

func TestASessionLastsADayAndNoLonger(t *testing.T) {
	e := boundEnv(t, true)
	s := e.sessionFor(t, "a.example.run.app", "bound-a")

	exp, err := time.Parse(time.RFC3339, s.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if got := exp.Sub(*e.clock); got != DefaultSessionTTL {
		t.Errorf("lifetime %v, want %v", got, DefaultSessionTTL)
	}

	*e.clock = e.clock.Add(DefaultSessionTTL - time.Minute)
	want(t, e.meAt("a.example.run.app", s.Token), 200)
	*e.clock = e.clock.Add(2 * time.Minute)
	want(t, e.meAt("a.example.run.app", s.Token), 401)
}

func TestTheOwnerCanSetTheLifetime(t *testing.T) {
	e := boundEnv(t, true)
	e.s.cfg.SessionTTL = time.Hour
	s := e.sessionFor(t, "a.example.run.app", "bound-a")

	*e.clock = e.clock.Add(61 * time.Minute)
	want(t, e.meAt("a.example.run.app", s.Token), 401)
}

func TestASessionIsRefusedByAnotherBackend(t *testing.T) {
	e := boundEnv(t, true)
	s := e.sessionFor(t, "a.example.run.app", "bound-a")

	// Even where the secret were the same, the token names its own host.
	want(t, e.meAt("b.example.run.app", s.Token), 401)
}

// renew asks for a new session with an existing one, returning the status and,
// when it worked, the reply.
func (e *env) renew(host, token string) (int, sessionReply) {
	req := httptest.NewRequest("POST", "/v1/session", strings.NewReader(""))
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, req)
	var reply sessionReply
	if w.Code == 200 {
		_ = json.Unmarshal(w.Body.Bytes(), &reply)
	}
	return w.Code, reply
}

func TestAValidSessionRenewsWithoutGoogle(t *testing.T) {
	e := boundEnv(t, true)
	first := e.sessionFor(t, "a.example.run.app", "bound-a")
	// No ID token is needed (or even valid) for the renewal.
	delete(e.s.verifier.(fakeVerifier), "bound-a")

	*e.clock = e.clock.Add(20 * time.Hour)
	code, second := e.renew("a.example.run.app", first.Token)

	if code != 200 {
		t.Fatalf("renewal refused: %d", code)
	}
	if second.Token == first.Token {
		t.Error("renewal returned the same token")
	}
	exp, _ := time.Parse(time.RFC3339, second.ExpiresAt)
	if got := exp.Sub(*e.clock); got != DefaultSessionTTL {
		t.Errorf("renewed lifetime %v, want %v", got, DefaultSessionTTL)
	}
	// The old one still stands until its own end; the new one works.
	want(t, e.meAt("a.example.run.app", second.Token), 200)
}

func TestARenewalChainEndsAWeekAfterTheGoogleSignIn(t *testing.T) {
	e := boundEnv(t, true)
	s := e.sessionFor(t, "a.example.run.app", "bound-a")
	start := *e.clock

	// Renewed every 20 hours, as a regular user would be.
	for i := 0; i < 8; i++ {
		*e.clock = e.clock.Add(20 * time.Hour)
		if e.clock.Sub(start) >= DefaultSessionMaxAge {
			break
		}
		code, next := e.renew("a.example.run.app", s.Token)
		if code != 200 {
			t.Fatalf("renewal %d refused: %d", i, code)
		}
		s = next
		if exp, _ := time.Parse(time.RFC3339, s.ExpiresAt); exp.Sub(start) > DefaultSessionMaxAge {
			t.Fatalf("a session ends %v after the sign-in, past the limit", exp.Sub(start))
		}
	}

	// Past the week, the chain is spent: neither the token nor a renewal works.
	*e.clock = start.Add(DefaultSessionMaxAge + time.Minute)
	want(t, e.meAt("a.example.run.app", s.Token), 401)
	if code, _ := e.renew("a.example.run.app", s.Token); code != 401 {
		t.Errorf("a spent chain renewed: %d", code)
	}

	// A new Google sign-in starts a new chain.
	fresh := e.sessionFor(t, "a.example.run.app", "bound-a")
	want(t, e.meAt("a.example.run.app", fresh.Token), 200)
}

func TestTheLastSessionOfAChainEndsAtTheLimit(t *testing.T) {
	e := boundEnv(t, true)
	s := e.sessionFor(t, "a.example.run.app", "bound-a")
	start := *e.clock

	// Six days and a half in: a day's worth would pass the limit.
	*e.clock = start.Add(DefaultSessionMaxAge - 12*time.Hour)
	// (the token made at the start has expired by now; renew along the way)
	s = e.chain(t, s, start)
	exp, _ := time.Parse(time.RFC3339, s.ExpiresAt)
	if exp.Sub(start) != DefaultSessionMaxAge {
		t.Errorf("the last session ends %v after the sign-in, want %v", exp.Sub(start), DefaultSessionMaxAge)
	}
}

// chain renews s once a day up to the current clock, as an app in daily use
// would, and returns the last session.
func (e *env) chain(t *testing.T, s sessionReply, start time.Time) sessionReply {
	t.Helper()
	target := *e.clock
	*e.clock = start
	for e.clock.Add(20 * time.Hour).Before(target) {
		*e.clock = e.clock.Add(20 * time.Hour)
		code, next := e.renew("a.example.run.app", s.Token)
		if code != 200 {
			t.Fatalf("renewal refused at %v: %d", e.clock.Sub(start), code)
		}
		s = next
	}
	*e.clock = target
	code, next := e.renew("a.example.run.app", s.Token)
	if code != 200 {
		t.Fatalf("last renewal refused: %d", code)
	}
	return next
}

func TestAnExpiredSessionCannotRenew(t *testing.T) {
	e := boundEnv(t, true)
	s := e.sessionFor(t, "a.example.run.app", "bound-a")

	*e.clock = e.clock.Add(DefaultSessionTTL + time.Minute)

	if code, _ := e.renew("a.example.run.app", s.Token); code != 401 {
		t.Errorf("an expired session renewed: %d", code)
	}
}

func TestASessionOfAnotherBackendCannotRenewHere(t *testing.T) {
	e := boundEnv(t, true)
	s := e.sessionFor(t, "a.example.run.app", "bound-a")

	if code, _ := e.renew("b.example.run.app", s.Token); code != 401 {
		t.Errorf("another backend's session renewed: %d", code)
	}
}

func TestTheOwnerCanSetTheLimitOfTheChain(t *testing.T) {
	e := boundEnv(t, true)
	e.s.cfg.SessionMaxAge = 30 * time.Hour
	s := e.sessionFor(t, "a.example.run.app", "bound-a")
	start := *e.clock

	*e.clock = start.Add(20 * time.Hour)
	_, s = e.renew("a.example.run.app", s.Token)
	exp, _ := time.Parse(time.RFC3339, s.ExpiresAt)
	if exp.Sub(start) != 30*time.Hour {
		t.Errorf("ends %v after the sign-in, want 30h", exp.Sub(start))
	}
}

func TestASessionIsMadeOnlyFromATokenBoundToThisBackend(t *testing.T) {
	e := boundEnv(t, true)

	for _, tok := range []string{"bound-b", "unbound", "junk", "unverf", "nobody", ""} {
		req := httptest.NewRequest("POST", "/v1/session", strings.NewReader(""))
		req.Host = "a.example.run.app"
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		e.s.Handler().ServeHTTP(w, req)
		want(t, w, 401)
	}
}

func TestATamperedSessionIsRefused(t *testing.T) {
	e := boundEnv(t, true)
	s := e.sessionFor(t, "a.example.run.app", "bound-a")

	body, sig, _ := strings.Cut(strings.TrimPrefix(s.Token, sessionPrefix), ".")
	other := e.sessionFor(t, "a.example.run.app", "bound-a")
	otherBody, _, _ := strings.Cut(strings.TrimPrefix(other.Token, sessionPrefix), ".")

	for name, tok := range map[string]string{
		"signature cut":   sessionPrefix + body + ".",
		"no signature":    sessionPrefix + body,
		"signature wrong": sessionPrefix + body + "." + strings.Repeat("A", len(sig)),
		"body swapped":    sessionPrefix + otherBody + "x." + sig,
		"nothing":         sessionPrefix,
		"junk":            sessionPrefix + "!!!.???",
	} {
		if w := e.meAt("a.example.run.app", tok); w.Code != 401 {
			t.Errorf("%s: got %d, want 401", name, w.Code)
		}
	}
}

func TestASessionOfAnotherSecretIsRefused(t *testing.T) {
	a := boundEnv(t, true)
	b := boundEnv(t, true)
	s := a.sessionFor(t, "a.example.run.app", "bound-a")

	// b has its own secret (every backend does).
	want(t, b.meAt("a.example.run.app", s.Token), 401)
}

func TestWhoIsAnAdministratorIsDecidedOnEveryRequest(t *testing.T) {
	e := newEnv(t)
	e.s.verifier.(fakeVerifier)["admin-a"] = Identity{
		Sub: "sub-admin", Email: "admin@example.com", EmailVerified: true,
		Nonce: tokenBinding("a.example.run.app"),
	}
	s := e.sessionFor(t, "a.example.run.app", "admin-a")

	me := func() bool {
		w := e.meAt("a.example.run.app", s.Token)
		want(t, w, 200)
		return strings.Contains(w.Body.String(), `"is_admin":true`)
	}
	if !me() {
		t.Fatal("the administrator is not one through a session")
	}

	// The owner changes who the administrators are: the session follows at once.
	e.s.cfg.AdminEmails = map[string]bool{}
	if me() {
		t.Error("a session kept administrator rights the owner took away")
	}
}

func TestAnAdministratorByUserIdIsRecognisedThroughASession(t *testing.T) {
	e := newEnv(t)
	e.s.cfg.AdminSubs = map[string]bool{"sub-admin": true}
	e.s.cfg.AdminEmails = map[string]bool{}
	e.s.verifier.(fakeVerifier)["admin-a"] = Identity{
		Sub: "sub-admin", Email: "someone-else@example.com", EmailVerified: true,
		Nonce: tokenBinding("a.example.run.app"),
	}
	s := e.sessionFor(t, "a.example.run.app", "admin-a")

	w := e.meAt("a.example.run.app", s.Token)
	want(t, w, 200)
	if !strings.Contains(w.Body.String(), `"is_admin":true`) {
		t.Errorf("not recognised by user id: %s", w.Body)
	}
}

func TestAMembersRemovalTakesEffectThroughASession(t *testing.T) {
	e := newEnv(t)
	e.s.verifier.(fakeVerifier)["alice-a"] = Identity{
		Sub: "sub-alice", Email: "alice@example.com", EmailVerified: true,
		Nonce: tokenBinding("a.example.run.app"),
	}
	s := e.sessionFor(t, "a.example.run.app", "alice-a")

	// The roster holds hashes; alice is active.
	e.putRoster(group, "BBQ", []Member{
		{UserID: "u-alice", Sub: e.hash(t, "sub-alice"), Nickname: "A", Status: "active"},
	})
	req := func() int {
		r := httptest.NewRequest("GET", "/v1/groups/"+group+"/events", strings.NewReader(""))
		r.Host = "a.example.run.app"
		r.Header.Set("Authorization", "Bearer "+s.Token)
		w := httptest.NewRecorder()
		e.s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if got := req(); got != 200 {
		t.Fatalf("member refused: %d", got)
	}

	e.putRoster(group, "BBQ", []Member{
		{UserID: "u-alice", Sub: e.hash(t, "sub-alice"), Nickname: "A", Status: "removed"},
	})
	if got := req(); got != 404 {
		t.Errorf("a removed member still got in: %d", got)
	}
}
