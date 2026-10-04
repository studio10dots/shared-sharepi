package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const secretObject = "_backend/secret.json"

// How long an invitation may be valid, in minutes: the administrator picks any
// whole number of minutes in this range (the default is the shortest useful
// one, a near-by person scans it at once).
const (
	defaultInviteMinutes = 5
	minInviteMinutes     = 1
	maxInviteMinutes     = 24 * 60
)

var b64 = base64.RawURLEncoding

// inviteKey returns the invitation-signing key, creating it the first time it is
// needed. Create-if-absent means two instances starting together cannot disagree:
// the loser of the race simply reads the winner's key.
func (s *Server) inviteKey(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	k := s.secret
	s.mu.Unlock()
	if k != nil {
		return k, nil
	}
	read := func() ([]byte, error) {
		data, _, err := s.store.Read(ctx, secretObject)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Key string `json:"invite_key"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		return b64.DecodeString(doc.Key)
	}
	k, err := read()
	if errors.Is(err, ErrNotFound) {
		nk := make([]byte, 32)
		if _, err := rand.Read(nk); err != nil {
			return nil, err
		}
		doc, _ := json.Marshal(map[string]string{"invite_key": b64.EncodeToString(nk)})
		if _, werr := s.store.Write(ctx, secretObject, doc, gen(createOnly)); werr != nil && !errors.Is(werr, ErrPrecondition) {
			return nil, werr
		}
		k, err = read()
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.secret = k
	s.mu.Unlock()
	return k, nil
}

type invitePayload struct {
	V int    `json:"v"`
	G string `json:"g"`
	N string `json:"n"`
	E int64  `json:"e"`
}

func (s *Server) signInvite(ctx context.Context, p invitePayload) (string, error) {
	key, err := s.inviteKey(ctx)
	if err != nil {
		return "", err
	}
	pb, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, key)
	mac.Write(pb)
	return b64.EncodeToString(pb) + "." + b64.EncodeToString(mac.Sum(nil)), nil
}

var errBadInvite = errors.New("bad invitation")

func (s *Server) parseInvite(ctx context.Context, tok string) (invitePayload, error) {
	pbs, sigs, ok := strings.Cut(tok, ".")
	if !ok {
		return invitePayload{}, errBadInvite
	}
	pb, err1 := b64.DecodeString(pbs)
	sig, err2 := b64.DecodeString(sigs)
	if err1 != nil || err2 != nil {
		return invitePayload{}, errBadInvite
	}
	key, err := s.inviteKey(ctx)
	if err != nil {
		return invitePayload{}, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(pb)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return invitePayload{}, errBadInvite
	}
	var p invitePayload
	if json.Unmarshal(pb, &p) != nil || p.V != 1 || !groupIDRe.MatchString(p.G) || p.N == "" {
		return invitePayload{}, errBadInvite
	}
	return p, nil
}

func (s *Server) publicURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" && strings.HasPrefix(r.Host, "127.0.0.1") {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

const invitationPrefix = "chamagon1."

// createInvitation returns the string shown as a QR code and copyable as text:
// a versioned prefix plus base64url({backend url, token}).
func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request, id Identity, g string, ro Roster) {
	if ro.sharingState() == "suspended" {
		writeErr(w, http.StatusConflict, "sharing_suspended")
		return
	}
	var req struct {
		TTLMinutes int `json:"ttl_minutes"`
	}
	if r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	if req.TTLMinutes == 0 {
		req.TTLMinutes = defaultInviteMinutes
	}
	if req.TTLMinutes < minInviteMinutes || req.TTLMinutes > maxInviteMinutes {
		writeErr(w, http.StatusUnprocessableEntity, "ttl_minutes must be between 1 and 1440")
		return
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	exp := s.now().Add(time.Duration(req.TTLMinutes) * time.Minute)
	tok, err := s.signInvite(r.Context(), invitePayload{V: 1, G: g, N: b64.EncodeToString(nonce), E: exp.Unix()})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	body, _ := json.Marshal(map[string]string{"u": s.publicURL(r), "t": tok})
	writeJSON(w, http.StatusOK, map[string]any{
		"invitation": invitationPrefix + b64.EncodeToString(body),
		"expires_at": exp.UTC().Format(time.RFC3339),
	})
}

func (s *Server) redeem(w http.ResponseWriter, r *http.Request, id Identity) {
	var req struct {
		Token        string `json:"token"`
		Nickname     string `json:"nickname"`
		TermsVersion string `json:"terms_version"`
	}
	if !decode(w, r, &req) {
		return
	}
	// Checked before the invitation is spent, so a client that skipped the terms
	// screen cannot join, and a refused call does not burn the invitation.
	if !termsVersion.MatchString(req.TermsVersion) {
		writeErr(w, http.StatusUnprocessableEntity, "terms_required")
		return
	}
	nick, ok := validText(req.Nickname, 30)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "invalid nickname")
		return
	}
	p, err := s.parseInvite(r.Context(), req.Token)
	if errors.Is(err, errBadInvite) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid invitation")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	if s.now().Unix() > p.E {
		writeErr(w, http.StatusGone, "invitation_expired")
		return
	}
	target, err := s.loadRoster(r.Context(), p.G)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	// Checked before the invitation is spent, like the terms above.
	if target.sharingState() == "suspended" {
		writeErr(w, http.StatusConflict, "sharing_suspended")
		return
	}
	// Spend the invitation first: if the roster write below fails the invitation is
	// burnt and the administrator issues another, never the other way round.
	if _, err := s.store.Write(r.Context(), p.G+"/invites/"+p.N, []byte{}, gen(createOnly)); err != nil {
		if errors.Is(err, ErrPrecondition) {
			writeErr(w, http.StatusGone, "invitation_used")
			return
		}
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	var userID string
	ro, err := s.updateRoster(r.Context(), p.G, func(ro *Roster) error {
		for i := range ro.Members {
			m := &ro.Members[i]
			if m.Sub != id.Sub && m.Sub != id.RawSub {
				continue
			}
			m.Sub = id.Sub            // upgrades a row written before hashing
			if m.Status != "active" { // a removed member coming back keeps the same user_id
				m.Status, m.Nickname = "active", nick
			}
			m.Admin = s.isAdmin(id)
			userID = m.UserID
			return nil
		}
		userID = newUUID()
		ro.Members = append(ro.Members, Member{UserID: userID, Sub: id.Sub, Nickname: nick, Status: "active", JoinedAt: s.nowRFC3339(), Admin: s.isAdmin(id)})
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group_id": p.G, "display_name": ro.DisplayName, "user_id": userID})
}
