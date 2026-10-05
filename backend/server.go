package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// Identity is what a verified Google ID token yields.
// Identity is who the ID token says the caller is. Sub is what the rest of the
// backend uses and stores: a keyed hash of Google's user id (see hashSub), so a
// leaked roster does not name anyone. RawSub is Google's own id, held only for
// the length of the request (to recognise rosters written before hashing) and
// never stored or logged. Email is used to recognise administrators when no
// ADMIN_SUBS is configured, and is never stored.
type Identity struct {
	Sub           string
	RawSub        string
	Email         string
	EmailVerified bool
	// Nonce is the token's nonce claim: what the app asked Google to put in it.
	// An app that binds its tokens to one backend (see tokenBinding) puts a value
	// there that only that backend accepts.
	Nonce string
}

// Verifier checks a bearer ID token. It is an interface so tests need no Google.
type Verifier interface {
	Verify(ctx context.Context, token string) (Identity, error)
}

// SignRequest describes one signed URL. ContentType, Metadata and MaxSize are
// PUT-only: the signer turns them into whatever headers its cloud needs
// (e.g. Content-Type, x-goog-meta-*, a size-range condition); see gcsSigner.
type SignRequest struct {
	Object      string
	Method      string // GET or PUT
	Expires     time.Duration
	ContentType string
	Metadata    map[string]string // logical custom metadata, e.g. {"uploader": userID}
	MaxSize     int64             // PUT only; 0 means unbounded
}

type Signer interface {
	// SignURL returns the URL and the exact headers the client must send
	// verbatim (they are part of the signature).
	SignURL(ctx context.Context, r SignRequest) (url string, headers map[string]string, err error)
}

type Config struct {
	// AdminSubs are the Google user ids (the token's `sub`, unhashed) of the
	// administrators. When it is not empty it alone decides who is one and
	// AdminEmails is ignored: an email can change hands, a sub never does.
	AdminSubs map[string]bool
	// AdminEmails decides when AdminSubs is empty (the older setting).
	AdminEmails map[string]bool
	PublicURL   string // this backend's own URL; derived from the request when empty
	DownloadTTL time.Duration
	UploadTTL   time.Duration
	// MaxUploadSize is the largest object any upload URL allows (a video's
	// original may use all of it). The three below are tighter limits for what is
	// not a video: a thumbnail is a few KB, a medium image a few MB, a photo
	// original some tens of MB. Zero means no tighter limit than MaxUploadSize.
	// They exist because a member holds a signed URL for each upload, and the
	// owner pays for whatever is stored through it.
	MaxUploadSize    int64
	MaxThumbnailSize int64
	MaxMediumSize    int64
	MaxImageSize     int64

	// WebOrigin is the one browser origin allowed to call this backend
	// (its own Web build's Cloud Run URL); empty
	// disables all browser (CORS) access. See cors.go.
	WebOrigin string

	// MaxEventItems is the most photos and videos one event may hold.
	MaxEventItems int
	RosterTTL     time.Duration

	// RequireTokenBinding refuses an ID token that carries no nonce for this
	// backend. A token that carries a nonce for another backend is always refused,
	// whatever this says: that is a token someone is replaying. See tokenBinding.
	RequireTokenBinding bool
}

type Server struct {
	cfg      Config
	verifier Verifier
	store    Store
	signer   Signer
	now      func() time.Time

	mu     sync.Mutex
	roster map[string]cachedRoster
	secret []byte
	counts map[string]countEntry
	// settings is the last read of the owner's settings (settings.go).
	settings *cachedSettings

	// updateNotice is set once, in the background, by checkForUpdateAsync,
	// and consumed (cleared) at most once by consumeUpdateNotice.
	updateNotice atomic.Pointer[UpdateNotice]
}

type cachedRoster struct {
	r  Roster
	at time.Time
}

// Member is one row of members.json. Sub never leaves the backend. It is the
// keyed hash of the member's Google id, not the id. Email is no longer written;
// the field only lets rows from before be read, and is dropped on the next write.
type Member struct {
	UserID   string `json:"user_id"`
	Sub      string `json:"sub"`
	Email    string `json:"email,omitempty"`
	Nickname string `json:"nickname"`
	Status   string `json:"status"` // active | removed | left
	JoinedAt string `json:"joined_at"`
	// Admin marks the row of one of the backend's administrators. The roster
	// keeps no email, so the backend can only tell who is an administrator
	// when that person calls; it records it then (see syncAdmin), so the
	// member list can show it to everyone.
	Admin bool `json:"admin,omitempty"`
}

type Roster struct {
	Version     int    `json:"version"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
	// Sharing is "suspended" while the administrator's app has stopped sharing in
	// the group; empty otherwise.
	Sharing string   `json:"sharing,omitempty"`
	Members []Member `json:"members"`
}

// sharingState is the group's state as clients see it.
func (r Roster) sharingState() string {
	if r.Sharing == "suspended" {
		return "suspended"
	}
	return "active"
}

func (r Roster) active(sub string) (Member, bool) {
	for _, m := range r.Members {
		if m.Sub == sub && m.Status == "active" {
			return m, true
		}
	}
	return Member{}, false
}

// activeID finds the caller's active row: by hash, or by Google's raw id for a
// row written before hashing (legacy is true then, and the row is upgraded).
func (r Roster) activeID(id Identity) (m Member, legacy, ok bool) {
	if m, ok := r.active(id.Sub); ok {
		return m, false, true
	}
	if id.RawSub != "" {
		if m, ok := r.active(id.RawSub); ok {
			return m, true, true
		}
	}
	return Member{}, false, false
}

// hashSub is HMAC-SHA256 of Google's user id under a key derived from the
// backend's secret. Deterministic, so the same person matches on every request;
// keyed, so the roster alone reveals no Google id.
func (s *Server) hashSub(ctx context.Context, raw string) (string, error) {
	k, err := s.inviteKey(ctx)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, k)
	mac.Write([]byte("member-id:" + raw))
	return "h1:" + hex.EncodeToString(mac.Sum(nil)), nil
}

// upgradeLegacy rewrites a row that still holds a raw Google id to the hash and
// drops any stored email. Best effort: the request does not depend on it.
func (s *Server) upgradeLegacy(ctx context.Context, g string, id Identity) {
	_, _ = s.updateRoster(ctx, g, func(ro *Roster) error {
		for i := range ro.Members {
			if ro.Members[i].Sub == id.RawSub {
				ro.Members[i].Sub = id.Sub
			}
		}
		return nil
	})
}

// syncAdmin records on the caller's row whether they are an administrator,
// when that has changed (ADMIN_EMAILS was edited, or the row predates the
// flag). It costs a roster write only then. A failure is left for the next
// call: the flag is for display, never for authorization.
func (s *Server) syncAdmin(ctx context.Context, g string, m Member, id Identity) {
	admin := s.isAdmin(id)
	if m.Admin == admin {
		return
	}
	_, _ = s.updateRoster(ctx, g, func(ro *Roster) error {
		for i := range ro.Members {
			if ro.Members[i].UserID == m.UserID {
				ro.Members[i].Admin = admin
			}
		}
		return nil
	})
}

func NewServer(cfg Config, v Verifier, st Store, sg Signer) *Server {
	return &Server{cfg: cfg, verifier: v, store: st, signer: sg, now: time.Now, roster: map[string]cachedRoster{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", s.ping)
	mux.HandleFunc("GET /v1/me", s.auth(s.me))
	mux.HandleFunc("GET /v1/my/groups", s.auth(s.myGroups))
	mux.HandleFunc("GET /v1/settings", s.auth(s.getSettings))
	mux.HandleFunc("PUT /v1/settings", s.auth(s.putSettings))
	mux.HandleFunc("POST /v1/groups", s.auth(s.createGroup))
	mux.HandleFunc("GET /v1/groups/{g}", s.auth(s.viewer(s.getGroup)))
	mux.HandleFunc("PATCH /v1/groups/{g}", s.auth(s.admin(s.renameGroup)))
	mux.HandleFunc("POST /v1/groups/{g}/invitations", s.auth(s.admin(s.createInvitation)))
	mux.HandleFunc("POST /v1/invitations/redeem", s.auth(s.redeem))
	mux.HandleFunc("PATCH /v1/groups/{g}/me", s.auth(s.member(s.patchMe)))
	mux.HandleFunc("POST /v1/groups/{g}/leave", s.auth(s.member(s.leaveGroup)))
	mux.HandleFunc("POST /v1/groups/{g}/reports", s.auth(s.member(s.createReport)))
	mux.HandleFunc("GET /v1/groups/{g}/reports", s.auth(s.admin(s.listReports)))
	mux.HandleFunc("GET /v1/reports/open", s.auth(s.openReports))
	mux.HandleFunc("POST /v1/groups/{g}/reports/{report_id}/resolve", s.auth(s.admin(s.resolveReport)))
	mux.HandleFunc("DELETE /v1/groups/{g}/members/{user_id}", s.auth(s.admin(s.removeMember)))
	mux.HandleFunc("POST /v1/groups/{g}/stop-sharing", s.auth(s.admin(s.stopSharing)))
	mux.HandleFunc("POST /v1/groups/{g}/restore-sharing", s.auth(s.admin(s.restoreSharing)))
	mux.HandleFunc("GET /v1/groups/{g}/events", s.auth(s.member(s.listEvents)))
	mux.HandleFunc("POST /v1/groups/{g}/events", s.auth(s.member(s.createEvent)))
	mux.HandleFunc("PUT /v1/groups/{g}/events/{id}", s.auth(s.member(s.updateEvent)))
	mux.HandleFunc("DELETE /v1/groups/{g}/events/{id}", s.auth(s.member(s.deleteEvent)))
	mux.HandleFunc("GET /v1/groups/{g}/events/{id}/items", s.auth(s.member(s.listItems)))
	mux.HandleFunc("DELETE /v1/groups/{g}/events/{id}/items/{photo_id}", s.auth(s.member(s.deleteItem)))
	mux.HandleFunc("GET /v1/groups/{g}/favorites", s.auth(s.member(s.getFavorites)))
	mux.HandleFunc("POST /v1/groups/{g}/favorites/ops", s.auth(s.member(s.postFavoriteOps)))
	mux.HandleFunc("GET /v1/groups/{g}/trash", s.auth(s.member(s.listTrash)))
	mux.HandleFunc("GET /v1/groups/{g}/trash/thumbnail", s.auth(s.member(s.trashThumbnail)))
	mux.HandleFunc("POST /v1/groups/{g}/trash/restore", s.auth(s.member(s.restoreTrash)))
	mux.HandleFunc("POST /v1/groups/{g}/uploads", s.auth(s.member(s.uploads)))
	mux.HandleFunc("POST /v1/groups/{g}/downloads", s.auth(s.member(s.downloads)))
	mux.HandleFunc("POST /v1/groups/{g}/shares", s.auth(s.member(s.createShare)))
	return s.withCORS(mux)
}

type handler func(w http.ResponseWriter, r *http.Request, id Identity)
type memberHandler func(w http.ResponseWriter, r *http.Request, id Identity, group string, me Member)
type adminHandler func(w http.ResponseWriter, r *http.Request, id Identity, group string, ro Roster)
type viewerHandler func(w http.ResponseWriter, r *http.Request, id Identity, group string, ro Roster, me *Member)

func (s *Server) ping(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"api_versions": []int{1}, "build": version})
}

// auth verifies the token before anything else touches the bucket.
func (s *Server) auth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		id, err := s.verifier.Verify(r.Context(), tok)
		if err != nil || !id.EmailVerified || id.Sub == "" {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if !s.tokenBoundHere(id, r) {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		id.RawSub = id.Sub
		if id.Sub, err = s.hashSub(r.Context(), id.RawSub); err != nil {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		next(w, r, id)
	}
}

// tokenBinding is the nonce an app asks Google to put into an ID token meant for
// one backend: the unpadded base64url of SHA-256("sharepi-backend:" + host),
// where host is the lower-cased host of the URL the app talks to, as it arrives
// in the Host header, with a default port (":443", ":80") left out: HTTP clients
// differ in whether they send one, so neither side may depend on it.
//
// Every backend accepts tokens issued for the same OAuth client, so without this
// a backend that is handed a member's token (every backend is, on each call)
// could replay it against any other backend the member uses, and against the
// owner's own backend if the member is its administrator. A token whose nonce
// names this backend's host is useless anywhere else.
func tokenBinding(host string) string {
	host = strings.ToLower(host)
	host = strings.TrimSuffix(strings.TrimSuffix(host, ":443"), ":80")
	sum := sha256.Sum256([]byte("sharepi-backend:" + host))
	return b64.EncodeToString(sum[:])
}

// tokenBoundHere reports whether the token may be used on this request's host.
// A nonce that names another host is always refused; a token without one is
// refused only when the owner required binding.
func (s *Server) tokenBoundHere(id Identity, r *http.Request) bool {
	if id.Nonce == "" {
		return !s.cfg.RequireTokenBinding
	}
	return hmac.Equal([]byte(id.Nonce), []byte(tokenBinding(r.Host)))
}

// isAdmin reports whether the caller is one of the backend's administrators.
// With ADMIN_SUBS set it compares Google's own user id, which is never reused
// or reassigned; without it, it falls back to the (verified) email, which can
// change hands (a company or school address, a recreated account). It is
// deliberately all-or-nothing: once ADMIN_SUBS exists, a matching email grants
// nothing, so adding the ids can only narrow who is an administrator.
func (s *Server) isAdmin(id Identity) bool {
	if len(s.cfg.AdminSubs) > 0 {
		return id.RawSub != "" && s.cfg.AdminSubs[id.RawSub]
	}
	return s.cfg.AdminEmails[strings.ToLower(id.Email)]
}

// parseList splits a comma-separated environment value into a set, trimming
// blanks and dropping empty entries. lower folds case (emails are not
// case-sensitive; a sub is a number and is left as it is).
func parseList(csv string, lower bool) map[string]bool {
	out := map[string]bool{}
	for _, e := range strings.Split(csv, ",") {
		e = strings.TrimSpace(e)
		if lower {
			e = strings.ToLower(e)
		}
		if e != "" {
			out[e] = true
		}
	}
	return out
}

// adminModeLog says, without naming anyone, which setting decides who an
// administrator is — the thing an owner needs to see in the logs when "I am
// not shown as administrator".
func adminModeLog(c Config) string {
	if len(c.AdminSubs) > 0 {
		return fmt.Sprintf("administrators: %d by Google user id (ADMIN_SUBS); ADMIN_EMAILS is ignored", len(c.AdminSubs))
	}
	return fmt.Sprintf("administrators: %d by email (ADMIN_EMAILS); set ADMIN_SUBS to pin them to Google user ids", len(c.AdminEmails))
}

// member answers 404 for strangers, exactly as for a group that does not exist.
func (s *Server) member(next memberHandler) handler {
	return func(w http.ResponseWriter, r *http.Request, id Identity) {
		g := r.PathValue("g")
		ro, err := s.loadRoster(r.Context(), g)
		if err != nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		m, legacy, ok := ro.activeID(id)
		if !ok {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if legacy {
			s.upgradeLegacy(r.Context(), g, id)
		}
		s.syncAdmin(r.Context(), g, m, id)
		next(w, r, id, g, m)
	}
}

// admin lets the backend's administrators manage any group, member or not.
// A member who is not an administrator gets 403; a stranger gets 404.
func (s *Server) admin(next adminHandler) handler {
	return func(w http.ResponseWriter, r *http.Request, id Identity) {
		g := r.PathValue("g")
		ro, err := s.loadRoster(r.Context(), g)
		if err != nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if !s.isAdmin(id) {
			if _, _, ok := ro.activeID(id); ok {
				writeErr(w, http.StatusForbidden, "administrator only")
			} else {
				writeErr(w, http.StatusNotFound, "not found")
			}
			return
		}
		next(w, r, id, g, ro)
	}
}

// viewer serves members and administrators (an administrator may not be a member).
func (s *Server) viewer(next viewerHandler) handler {
	return func(w http.ResponseWriter, r *http.Request, id Identity) {
		g := r.PathValue("g")
		ro, err := s.loadRoster(r.Context(), g)
		if err != nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		m, legacy, ok := ro.activeID(id)
		if ok && legacy {
			s.upgradeLegacy(r.Context(), g, id)
		}
		if ok {
			s.syncAdmin(r.Context(), g, m, id)
		}
		if !ok && !s.isAdmin(id) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if ok {
			next(w, r, id, g, ro, &m)
		} else {
			next(w, r, id, g, ro, nil)
		}
	}
}

func rosterName(g string) string { return g + "/members.json" }

func (s *Server) loadRoster(ctx context.Context, g string) (Roster, error) {
	if !groupIDRe.MatchString(g) {
		return Roster{}, ErrNotFound
	}
	s.mu.Lock()
	c, ok := s.roster[g]
	s.mu.Unlock()
	if ok && s.now().Sub(c.at) < s.cfg.RosterTTL {
		return c.r, nil
	}
	ro, _, err := s.readRoster(ctx, g)
	if err != nil {
		return Roster{}, err
	}
	s.mu.Lock()
	s.roster[g] = cachedRoster{ro, s.now()}
	s.mu.Unlock()
	return ro, nil
}

func (s *Server) readRoster(ctx context.Context, g string) (Roster, Version, error) {
	b, generation, err := s.store.Read(ctx, rosterName(g))
	if err != nil {
		return Roster{}, "", err
	}
	var ro Roster
	if err := json.Unmarshal(b, &ro); err != nil {
		return Roster{}, "", err
	}
	return ro, generation, nil
}

func (s *Server) dropCachedRoster(g string) {
	s.mu.Lock()
	delete(s.roster, g)
	s.mu.Unlock()
}

// updateRoster is the only place a roster is rewritten. It reads, applies fn and
// writes with a generation precondition, retrying when another writer got there
// first.
func (s *Server) updateRoster(ctx context.Context, g string, fn func(*Roster) error) (Roster, error) {
	defer s.dropCachedRoster(g)
	for range 6 {
		ro, generation, err := s.readRoster(ctx, g)
		if err != nil {
			return Roster{}, err
		}
		if err := fn(&ro); err != nil {
			return Roster{}, err
		}
		for i := range ro.Members {
			ro.Members[i].Email = "" // no longer kept
		}
		b, err := json.Marshal(ro)
		if err != nil {
			return Roster{}, err
		}
		_, err = s.store.Write(ctx, rosterName(g), b, gen(generation))
		if errors.Is(err, ErrPrecondition) {
			continue
		}
		return ro, err
	}
	return Roster{}, errors.New("roster update kept conflicting")
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, id Identity) {
	writeJSON(w, http.StatusOK, map[string]any{"is_admin": s.isAdmin(id)})
}

// ---- validation

var (
	groupIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,39}$`)
	photoIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dateRe    = regexp.MustCompile(`^\d{4}/\d{2}/\d{2}$`)
	extRe     = regexp.MustCompile(`^[a-z0-9]{1,5}$`)
	// A media object under a group: {g}/{event}/(original|medium|thumbnail)/{file}
	mediaRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*/[a-z0-9]{8,32}/(original|medium|thumbnail)/[0-9a-f-]{36}\.[a-z0-9]{1,5}$`)
)

// validText: a trimmed, non-empty string of at most max runes without control characters.
func validText(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > max {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return s, true
}

// maxNameLen is the longest group name and maxEventNameLen the longest event
// name (in characters). Longer names scroll on their one line in the app.
const (
	maxNameLen      = 30
	maxEventNameLen = 64
)

func eventPrefix(g, id string) string { return g + "/" + id + "/" }

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// ---- responses

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "bad json")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	// Responses carry signed URLs and rosters: keep them out of shared caches.
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// parallel runs fn over items with at most n at a time.
func parallel[T any](n int, items []T, fn func(int, T)) {
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i, it)
		}()
	}
	wg.Wait()
}
