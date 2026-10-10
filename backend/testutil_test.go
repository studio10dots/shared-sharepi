package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeVerifier map[string]Identity

func (f fakeVerifier) Verify(_ context.Context, t string) (Identity, error) {
	if id, ok := f[t]; ok {
		return id, nil
	}
	return Identity{}, errors.New("bad token")
}

// memStore is an in-memory bucket with GCS's generation semantics.
type memStore struct {
	mu      sync.Mutex
	objs    map[string]*memObj
	next    int64
	deleted []memDeleted
}

type memDeleted struct {
	name string
	obj  *memObj
	at   time.Time
}

type memObj struct {
	data    []byte
	gen     int64
	meta    map[string]string
	updated time.Time
}

func newMemStore() *memStore { return &memStore{objs: map[string]*memObj{}, next: 1} }

// ver is how memStore exposes a generation as a Store Version, as GCS does.
func ver(g int64) Version { return Version(strconv.FormatInt(g, 10)) }

func (m *memStore) Read(_ context.Context, n string) ([]byte, Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objs[n]
	if !ok {
		return nil, "", ErrNotFound
	}
	return append([]byte(nil), o.data...), ver(o.gen), nil
}

func (m *memStore) Write(_ context.Context, n string, data []byte, cond *Version) (Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, exists := m.objs[n]
	if cond != nil {
		if *cond == createOnly && exists {
			return "", ErrPrecondition
		}
		if *cond != createOnly && (!exists || ver(cur.gen) != *cond) {
			return "", ErrPrecondition
		}
	}
	m.next++
	m.objs[n] = &memObj{data: append([]byte(nil), data...), gen: m.next, updated: time.Unix(1_700_000_000+m.next, 0)}
	return ver(m.next), nil
}

// put seeds an object with metadata and an explicit upload time.
func (m *memStore) put(n string, size int, meta map[string]string, updated time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	m.objs[n] = &memObj{data: make([]byte, size), gen: m.next, meta: meta, updated: updated}
}

func (m *memStore) has(n string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objs[n]
	return ok
}

func (m *memStore) names(prefix string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for n := range m.objs {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// List always fills Metadata, as GCS does whatever withMetadata says.
func (m *memStore) List(_ context.Context, prefix, delim string, _ bool) (Listing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var l Listing
	seen := map[string]bool{}
	for n, o := range m.objs {
		if !strings.HasPrefix(n, prefix) {
			continue
		}
		rest := n[len(prefix):]
		if delim != "" {
			if i := strings.Index(rest, delim); i >= 0 {
				p := prefix + rest[:i+len(delim)]
				if !seen[p] {
					seen[p] = true
					l.Prefixes = append(l.Prefixes, p)
				}
				continue
			}
		}
		l.Objects = append(l.Objects, ObjectInfo{Name: n, Size: int64(len(o.data)), Updated: o.updated, Metadata: o.meta})
	}
	sort.Strings(l.Prefixes)
	sort.Slice(l.Objects, func(i, j int) bool { return l.Objects[i].Name < l.Objects[j].Name })
	return l, nil
}

func (m *memStore) Delete(_ context.Context, n string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.objs[n]; ok {
		m.deleted = append(m.deleted, memDeleted{n, o, time.Unix(1_800_000_000+o.gen, 0)})
		delete(m.objs, n)
	}
	return nil
}

// ListDeleted lists the objects that are deleted now: one that was restored
// (or written again) is live and no longer in the trash, but its deleted
// generation stays readable and can be restored again once it is deleted.
func (m *memStore) ListDeleted(_ context.Context, prefix string) ([]DeletedObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DeletedObject
	for _, d := range m.deleted {
		if _, live := m.objs[d.name]; live {
			continue
		}
		if strings.HasPrefix(d.name, prefix) {
			out = append(out, DeletedObject{Name: d.name, Version: ver(d.obj.gen), DeletedAt: d.at})
		}
	}
	return out, nil
}

func (m *memStore) Restore(_ context.Context, n string, version Version) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.deleted {
		if d.name == n && ver(d.obj.gen) == version {
			if _, live := m.objs[n]; live {
				return ErrPrecondition
			}
			// The restored object is a copy: the deleted generation stays as it
			// was, so it is still found by the generation the trash listed.
			m.next++
			m.objs[n] = &memObj{data: d.obj.data, gen: m.next, meta: d.obj.meta, updated: d.obj.updated}
			return nil
		}
	}
	return ErrNotFound
}

func (m *memStore) ReadDeleted(_ context.Context, n string, version Version) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.deleted {
		if d.name == n && ver(d.obj.gen) == version {
			return append([]byte(nil), d.obj.data...), nil
		}
	}
	return nil, ErrNotFound
}

type fakeSigner struct {
	mu    sync.Mutex
	calls []SignRequest
}

// SignURL records the request; tests check its ContentType, Metadata and
// MaxSize, which is what the server decides (the cloud-specific header names
// are the real signer's business).
func (f *fakeSigner) SignURL(_ context.Context, r SignRequest) (string, map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r)
	return "https://signed.example/" + r.Object, nil, nil
}

const (
	pid   = "550e8400-e29b-41d4-a716-446655440000"
	pid2  = "660e8400-e29b-41d4-a716-446655440000"
	group = "grp_bbq"
)

var testNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

type env struct {
	s  *Server
	st *memStore
	sg *fakeSigner
	// clock is advanced by tests that care about expiry.
	clock *time.Time
}

func idents() fakeVerifier {
	return fakeVerifier{
		"alice":  {Sub: "sub-alice", Email: "alice@example.com", EmailVerified: true},
		"bob":    {Sub: "sub-bob", Email: "bob@example.com", EmailVerified: true},
		"carol":  {Sub: "sub-carol", Email: "carol@example.com", EmailVerified: true},
		"dave":   {Sub: "sub-dave", Email: "dave@example.com", EmailVerified: true},
		"admin":  {Sub: "sub-admin", Email: "Admin@Example.com", EmailVerified: true},
		"unverf": {Sub: "sub-alice", Email: "alice@example.com", EmailVerified: false},
	}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := newMemStore()
	sg := &fakeSigner{}
	clock := testNow
	s := NewServer(Config{
		AdminEmails: map[string]bool{"admin@example.com": true},
		DownloadTTL: time.Hour, UploadTTL: 15 * time.Minute, MaxUploadSize: 1000, MaxEventItems: 5, RosterTTL: 0,
	}, idents(), st, sg)
	s.now = func() time.Time { return clock }
	e := &env{s: s, st: st, sg: sg, clock: &clock}
	// grp_bbq: alice active, bob removed.
	e.putRoster(group, "BBQ", []Member{
		{UserID: "u-alice", Sub: "sub-alice", Email: "alice@example.com", Nickname: "A", Status: "active"},
		{UserID: "u-bob", Sub: "sub-bob", Email: "bob@example.com", Nickname: "B", Status: "removed"},
	})
	return e
}

func (e *env) putRoster(g, name string, ms []Member) {
	b, _ := json.Marshal(Roster{Version: 1, DisplayName: name, Members: ms})
	_, _ = e.st.Write(context.Background(), g+"/members.json", b, nil)
	e.s.dropCachedRoster(g)
}

func (e *env) roster(t *testing.T, g string) Roster {
	t.Helper()
	ro, _, err := e.s.readRoster(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	return ro
}

func (e *env) do(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, req)
	return w
}

func want(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("got %d, want %d: %s", w.Code, code, w.Body)
	}
}

func decodeBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("bad json %q: %v", w.Body, err)
	}
	return v
}

var _ = http.StatusOK

// hash is what the backend stores in a roster for a Google user id.
func (e *env) hash(t *testing.T, raw string) string {
	t.Helper()
	h, err := e.s.hashSub(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
