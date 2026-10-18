package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Session tokens spare the app a Google sign-in round trip on every hour.
//
// A Google ID token lives about an hour and a phone has nothing to refresh it with,
// so an app that sends one to each backend must ask Google for a new
// one (which can show the account panel) every hour, for every backend. Instead,
// the app sends one ID token — bound to this backend, as always — to
// POST /v1/session and gets back a token only this backend accepts, valid for
// SessionTTL (a day by default). Everything else is checked as before: the
// caller's membership of a group and their being an administrator are looked up
// on every request, so removing a member or changing ADMIN_SUBS takes effect at
// once; a session token proves who the caller is, nothing more.
//
// The token is stateless: "s1." + base64url(claims) + "." + base64url(HMAC),
// keyed from the backend's own secret (_backend/secret.json). Nothing is stored.
// Replacing that secret ends every session.
//
// A still-valid session can be renewed with POST /v1/session (a sliding
// window): the new token lives another SessionTTL, but never past SessionMaxAge
// (a week by default) after the Google sign-in the first one was made from.
// Past that, only a fresh ID token makes a session. So the app asks Google once
// per backend and SessionMaxAge, not once per day, and a stolen token dies at
// the latest SessionMaxAge after its owner last signed in with Google — and
// stops working at once if its owner is removed from the group.

const sessionPrefix = "s1."

// DefaultSessionTTL is how long one session token lives, unless Config says
// otherwise; DefaultSessionMaxAge is how long a chain of renewals may go on.
const (
	DefaultSessionTTL    = 24 * time.Hour
	DefaultSessionMaxAge = 7 * 24 * time.Hour
)

type sessionClaims struct {
	// Sub is Google's own user id, as in the ID token (hashed on use, like there).
	Sub   string `json:"sub"`
	Email string `json:"email,omitempty"`
	// Host is tokenBinding(host) of the backend that made it.
	Host string `json:"h"`
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp"`
	// Oat is when the Google sign-in this chain of sessions started from was
	// proven; renewals keep it, and it bounds the whole chain.
	Oat int64 `json:"oat"`
}

func (s *Server) sessionTTL() time.Duration {
	if s.cfg.SessionTTL > 0 {
		return s.cfg.SessionTTL
	}
	return DefaultSessionTTL
}

func (s *Server) sessionMaxAge() time.Duration {
	if s.cfg.SessionMaxAge > 0 {
		return s.cfg.SessionMaxAge
	}
	return DefaultSessionMaxAge
}

// sessionKey is derived from the backend's secret for this one purpose, so it
// is not the key that signs invitations or hashes members' ids.
func (s *Server) sessionKey(ctx context.Context) ([]byte, error) {
	k, err := s.inviteKey(ctx)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, k)
	mac.Write([]byte("session-key:v1"))
	return mac.Sum(nil), nil
}

func signSession(key, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return b64.EncodeToString(mac.Sum(nil))
}

// mintSession makes a session token for an identity that has just been proven,
// by an ID token or by a session being renewed. started is when the Google
// sign-in at the head of the chain happened (now, for an ID token); the token
// ends SessionTTL from now, but never after started + SessionMaxAge.
func (s *Server) mintSession(ctx context.Context, id Identity, host string, started time.Time) (string, time.Time, error) {
	key, err := s.sessionKey(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	exp := now.Add(s.sessionTTL())
	if last := started.Add(s.sessionMaxAge()); last.Before(exp) {
		exp = last
	}
	payload, err := json.Marshal(sessionClaims{
		Sub:   id.RawSub,
		Email: strings.ToLower(id.Email),
		Host:  tokenBinding(host),
		Iat:   now.Unix(),
		Exp:   exp.Unix(),
		Oat:   started.Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	enc := b64.EncodeToString(payload)
	return sessionPrefix + enc + "." + signSession(key, []byte(enc)), exp, nil
}

// verifySession returns the identity a session token stands for. ok is false
// for anything wrong with it: a bad signature, another backend's token, an
// expired one. err is set only when the backend's own secret cannot be read.
func (s *Server) verifySession(ctx context.Context, tok, host string) (id Identity, ok bool, err error) {
	rest, found := strings.CutPrefix(tok, sessionPrefix)
	if !found {
		return Identity{}, false, nil
	}
	enc, sig, found := strings.Cut(rest, ".")
	if !found || enc == "" || sig == "" {
		return Identity{}, false, nil
	}
	key, err := s.sessionKey(ctx)
	if err != nil {
		return Identity{}, false, err
	}
	if !hmac.Equal([]byte(sig), []byte(signSession(key, []byte(enc)))) {
		return Identity{}, false, nil
	}
	raw, derr := b64.DecodeString(enc)
	if derr != nil {
		return Identity{}, false, nil
	}
	var c sessionClaims
	if json.Unmarshal(raw, &c) != nil || c.Sub == "" || c.Oat == 0 {
		return Identity{}, false, nil
	}
	if !hmac.Equal([]byte(c.Host), []byte(tokenBinding(host))) || !s.now().Before(time.Unix(c.Exp, 0)) {
		return Identity{}, false, nil
	}
	return Identity{
		RawSub:        c.Sub,
		Email:         c.Email,
		EmailVerified: true, // it was, when the session was made
		Nonce:         c.Host,
		SessionStart:  time.Unix(c.Oat, 0),
	}, true, nil
}

// createSession is POST /v1/session. With an ID token it starts a chain of
// sessions; with a session token it renews that chain, until SessionMaxAge
// after the ID token it began with. The identity is the caller's either way.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request, id Identity) {
	started := s.now()
	if !id.SessionStart.IsZero() {
		started = id.SessionStart
		if !s.now().Before(started.Add(s.sessionMaxAge())) {
			// The chain is spent: a fresh Google sign-in is needed.
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
	}
	tok, exp, err := s.mintSession(r.Context(), id, r.Host, started)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"expires_at": exp.UTC().Format(time.RFC3339),
	})
}
