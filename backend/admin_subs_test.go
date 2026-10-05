package main

import (
	"strings"
	"testing"
)

type meResp struct {
	IsAdmin bool `json:"is_admin"`
}

func isAdminNow(t *testing.T, e *env, token string) bool {
	t.Helper()
	return decodeBody[meResp](t, e.do("GET", "/v1/me", token, "")).IsAdmin
}

// With ADMIN_SUBS set, the Google user id alone decides. A caller whose email
// is in ADMIN_EMAILS but whose id is not listed is no administrator: that is
// the point — an address can change hands (a recreated company account), an id
// cannot.
func TestAdminSubsDecideInsteadOfEmails(t *testing.T) {
	e := newEnv(t)
	e.s.cfg.AdminSubs = map[string]bool{"sub-alice": true}

	if !isAdminNow(t, e, "alice") {
		t.Error("alice's id is listed: she must be an administrator")
	}
	if isAdminNow(t, e, "admin") {
		t.Error("an email in ADMIN_EMAILS must grant nothing once ADMIN_SUBS is set")
	}
}

func TestAdminSubsGateTheAdminOnlyEndpoints(t *testing.T) {
	e := newEnv(t)
	e.s.cfg.AdminSubs = map[string]bool{"sub-alice": true}

	want(t, e.do("POST", "/v1/groups", "admin", createBody("grp_new", "New", "Ad")), 403) // email only
	want(t, e.do("POST", "/v1/groups", "alice", createBody("grp_new", "New", "Al")), 201) // id listed
}

// Without ADMIN_SUBS nothing changes: the email decides, case-insensitively.
func TestAdminEmailsStillDecideWithoutSubs(t *testing.T) {
	e := newEnv(t)
	if len(e.s.cfg.AdminSubs) != 0 {
		t.Fatal("the default test backend must have no ADMIN_SUBS")
	}
	if !isAdminNow(t, e, "admin") {
		t.Error("admin by email not recognised")
	}
	if isAdminNow(t, e, "alice") {
		t.Error("alice must not be admin")
	}
}

// Rosters keep a keyed hash of the id, never the id. ADMIN_SUBS holds Google's
// own id, so the hash must not be accepted in its place.
func TestAdminSubsAreTheRawIdNotTheStoredHash(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("POST", "/v1/groups", "admin", createBody("grp_new", "New", "Ad")), 201)
	var hashed string
	for _, m := range e.roster(t, "grp_new").Members {
		hashed = m.Sub
	}
	if hashed == "" || hashed == "sub-admin" {
		t.Fatalf("the roster must hold a hash, not the raw id (got %q)", hashed)
	}

	e.s.cfg.AdminSubs = map[string]bool{hashed: true}

	if isAdminNow(t, e, "admin") {
		t.Error("the stored hash must not count as an administrator id")
	}
}

func TestParseList(t *testing.T) {
	got := parseList(" A@X.com ,, b@y.com,\t", true)
	if len(got) != 2 || !got["a@x.com"] || !got["b@y.com"] {
		t.Errorf("emails: got %v", got)
	}
	got = parseList("110571000531995686849, 109538459286029168202", false)
	if len(got) != 2 || !got["110571000531995686849"] || !got["109538459286029168202"] {
		t.Errorf("subs: got %v", got)
	}
	if len(parseList("", false)) != 0 || len(parseList(" , ", true)) != 0 {
		t.Error("blank input must give an empty set")
	}
}

func TestAdminModeLogNamesTheModeNotThePeople(t *testing.T) {
	bySub := adminModeLog(Config{
		AdminSubs:   map[string]bool{"110571000531995686849": true},
		AdminEmails: map[string]bool{"a@x.com": true},
	})
	if !strings.Contains(bySub, "ADMIN_SUBS") || !strings.Contains(bySub, "ignored") {
		t.Errorf("sub mode: %q", bySub)
	}
	byEmail := adminModeLog(Config{AdminEmails: map[string]bool{"a@x.com": true}})
	if !strings.Contains(byEmail, "ADMIN_EMAILS") {
		t.Errorf("email mode: %q", byEmail)
	}
	for _, line := range []string{bySub, byEmail} {
		if strings.Contains(line, "110571000531995686849") || strings.Contains(line, "a@x.com") {
			t.Errorf("the log line must not name anyone: %q", line)
		}
	}
}
