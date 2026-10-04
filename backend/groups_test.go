package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func createBody(id, name, nick string) string {
	return fmt.Sprintf(`{"group_id":%q,"display_name":%q,"nickname":%q}`, id, name, nick)
}

func TestCreateGroup(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("POST", "/v1/groups", "alice", createBody("grp_new", "New", "Al")), 403) // not an administrator
	want(t, e.do("POST", "/v1/groups", "", createBody("grp_new", "New", "Al")), 401)
	for _, bad := range []string{
		createBody("_hidden", "x", "Al"), createBody("UPPER", "x", "Al"), createBody("ab", "x", "Al"),
		createBody("grp_new", "", "Al"), createBody("grp_new", "x", ""), createBody("grp_new", "x", "a\x07b"),
	} {
		want(t, e.do("POST", "/v1/groups", "admin", bad), 422)
	}
	want(t, e.do("POST", "/v1/groups", "admin", createBody("grp_new", "New", "Boss")), 201)
	ro := e.roster(t, "grp_new")
	if ro.DisplayName != "New" || len(ro.Members) != 1 || ro.Members[0].Sub != e.hash(t, "sub-admin") || ro.Members[0].Status != "active" || ro.Members[0].Email != "" {
		t.Errorf("roster = %+v", ro)
	}
	want(t, e.do("POST", "/v1/groups", "admin", createBody("grp_new", "Again", "Boss")), 409) // create-if-absent
	if e.roster(t, "grp_new").DisplayName != "New" {
		t.Error("a duplicate create overwrote the roster")
	}
}

func TestGetGroupHidesEmailAndSub(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/v1/groups/"+group, "alice", "")
	want(t, w, 200)
	for _, secret := range []string{"alice@example.com", "sub-alice", "bob@example.com", "sub-bob", "\"email\"", "\"sub\""} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("response leaks %q: %s", secret, w.Body)
		}
	}
	got := decodeBody[struct {
		DisplayName string         `json:"display_name"`
		Members     []publicMember `json:"members"`
		Me          struct {
			UserID string `json:"user_id"`
		} `json:"me"`
	}](t, w)
	if got.DisplayName != "BBQ" || len(got.Members) != 2 || got.Me.UserID != "u-alice" {
		t.Errorf("got %+v", got)
	}
	// Removed members stay listed so old posts still show a nickname.
	if got.Members[1].Status != "removed" || got.Members[1].Nickname != "B" {
		t.Errorf("removed member = %+v", got.Members[1])
	}
	want(t, e.do("GET", "/v1/groups/"+group, "carol", ""), 404)
	// An administrator who is not a member can still look, but has no "me".
	w = e.do("GET", "/v1/groups/"+group, "admin", "")
	want(t, w, 200)
	if strings.Contains(w.Body.String(), `"me"`) {
		t.Error("non-member administrator should have no me")
	}
}

func TestRenameAndNickname(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("PATCH", "/v1/groups/"+group, "alice", `{"display_name":"Hijack"}`), 403) // member, not administrator
	want(t, e.do("PATCH", "/v1/groups/"+group, "carol", `{"display_name":"Hijack"}`), 404) // stranger
	want(t, e.do("PATCH", "/v1/groups/"+group, "admin", `{"display_name":""}`), 422)
	want(t, e.do("PATCH", "/v1/groups/"+group, "admin", `{"display_name":"Summer BBQ"}`), 200)
	if e.roster(t, group).DisplayName != "Summer BBQ" {
		t.Error("rename not stored")
	}
	want(t, e.do("PATCH", "/v1/groups/"+group+"/me", "alice", `{"nickname":"Ali"}`), 200)
	if e.roster(t, group).Members[0].Nickname != "Ali" {
		t.Error("nickname not stored")
	}
	want(t, e.do("PATCH", "/v1/groups/"+group+"/me", "alice", `{"nickname":"   "}`), 422)
	want(t, e.do("PATCH", "/v1/groups/"+group+"/me", "bob", `{"nickname":"B2"}`), 404) // removed
}

// invite returns the token inside an invitation string.
func (e *env) invite(t *testing.T, tok string, ttl int) (token string, expires time.Time) {
	t.Helper()
	body := ""
	if ttl != 0 {
		body = fmt.Sprintf(`{"ttl_minutes":%d}`, ttl)
	}
	w := e.do("POST", "/v1/groups/"+group+"/invitations", tok, body)
	want(t, w, 200)
	got := decodeBody[struct {
		Invitation string `json:"invitation"`
		ExpiresAt  string `json:"expires_at"`
	}](t, w)
	if !strings.HasPrefix(got.Invitation, invitationPrefix) {
		t.Fatalf("invitation = %q", got.Invitation)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(got.Invitation, invitationPrefix))
	if err != nil {
		t.Fatal(err)
	}
	var inv struct{ U, T string }
	_ = json.Unmarshal(raw, &inv)
	if inv.U == "" || inv.T == "" {
		t.Fatalf("invitation content = %s", raw)
	}
	exp, _ := time.Parse(time.RFC3339, got.ExpiresAt)
	return inv.T, exp
}

func redeemBody(token, nick string) string {
	return fmt.Sprintf(`{"token":%q,"nickname":%q,"terms_version":"1"}`, token, nick)
}

func TestInvitationPermissionsAndLifetime(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("POST", "/v1/groups/"+group+"/invitations", "alice", ""), 403) // member, not administrator
	want(t, e.do("POST", "/v1/groups/"+group+"/invitations", "carol", ""), 404) // stranger
	for _, bad := range []string{`{"ttl_minutes":-1}`, `{"ttl_minutes":1441}`} {
		want(t, e.do("POST", "/v1/groups/"+group+"/invitations", "admin", bad), 422)
	}
	// Any whole number of minutes from 1 to 24 hours; none means the default.
	for ttl, minutes := range map[int]int{0: 5, 1: 1, 5: 5, 7: 7, 90: 90, 1440: 1440} {
		_, exp := e.invite(t, "admin", ttl)
		if got := exp.Sub(testNow); got != time.Duration(minutes)*time.Minute {
			t.Errorf("ttl %d: expires in %v, want %d minutes", ttl, got, minutes)
		}
	}
}

func TestRedeemJoinsOnceAndOnlyOnce(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.invite(t, "admin", 0)

	w := e.do("POST", "/v1/invitations/redeem", "carol", redeemBody(tok, "Carol"))
	want(t, w, 200)
	got := decodeBody[struct {
		GroupID, DisplayName string
		UserID               string `json:"user_id"`
	}](t, w)
	ro := e.roster(t, group)
	m, ok := ro.active(e.hash(t, "sub-carol"))
	if !ok || m.Nickname != "Carol" || m.Email != "" || m.UserID == "" || m.UserID != got.UserID {
		t.Fatalf("carol not joined properly: %+v / %+v", m, got)
	}
	// She can now use the group; a second person cannot reuse the same invitation.
	want(t, e.do("GET", "/v1/groups/"+group, "carol", ""), 200)
	w = e.do("POST", "/v1/invitations/redeem", "dave", redeemBody(tok, "Dave"))
	want(t, w, 410)
	if !strings.Contains(w.Body.String(), "invitation_used") {
		t.Errorf("body = %s", w.Body)
	}
	if _, ok := e.roster(t, group).active(e.hash(t, "sub-dave")); ok {
		t.Error("dave joined with a spent invitation")
	}
	want(t, e.do("GET", "/v1/groups/"+group, "dave", ""), 404)
}

func TestRedeemRejections(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.invite(t, "admin", 0)
	want(t, e.do("POST", "/v1/invitations/redeem", "", redeemBody(tok, "C")), 401)
	want(t, e.do("POST", "/v1/invitations/redeem", "carol", redeemBody(tok, "")), 422)
	want(t, e.do("POST", "/v1/invitations/redeem", "carol", redeemBody("garbage", "C")), 422)
	// A flipped character in the signature or payload is rejected.
	want(t, e.do("POST", "/v1/invitations/redeem", "carol", redeemBody(tok[:len(tok)-2]+"AA", "C")), 422)
	payload, sig, _ := strings.Cut(tok, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	forged := base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), group, "grp_other", 1))) + "." + sig
	want(t, e.do("POST", "/v1/invitations/redeem", "carol", redeemBody(forged, "C")), 422)
	// Failed attempts must not have spent the invitation or added anyone.
	want(t, e.do("POST", "/v1/invitations/redeem", "carol", redeemBody(tok, "C")), 200)
}

func TestRedeemExpiry(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.invite(t, "admin", 5)
	*e.clock = testNow.Add(5*time.Minute + 30*time.Second)
	w := e.do("POST", "/v1/invitations/redeem", "carol", redeemBody(tok, "C"))
	want(t, w, 410)
	if !strings.Contains(w.Body.String(), "invitation_expired") {
		t.Errorf("body = %s", w.Body)
	}
	if _, ok := e.roster(t, group).active(e.hash(t, "sub-carol")); ok {
		t.Error("joined with an expired invitation")
	}
	// Just inside the window it still works.
	*e.clock = testNow
	tok, _ = e.invite(t, "admin", 5)
	*e.clock = testNow.Add(4 * time.Minute)
	want(t, e.do("POST", "/v1/invitations/redeem", "carol", redeemBody(tok, "C")), 200)
}

func TestRedeemIsIdempotentForMembersAndRestoresRemoved(t *testing.T) {
	e := newEnv(t)
	// An active member redeeming keeps the same user_id and nickname.
	tok, _ := e.invite(t, "admin", 0)
	want(t, e.do("POST", "/v1/invitations/redeem", "alice", redeemBody(tok, "Other")), 200)
	ro := e.roster(t, group)
	if len(ro.Members) != 2 || ro.Members[0].UserID != "u-alice" || ro.Members[0].Nickname != "A" {
		t.Errorf("alice changed: %+v", ro.Members)
	}
	// A removed member coming back reactivates the same row.
	tok, _ = e.invite(t, "admin", 0)
	want(t, e.do("POST", "/v1/invitations/redeem", "bob", redeemBody(tok, "Bobby")), 200)
	ro = e.roster(t, group)
	if len(ro.Members) != 2 || ro.Members[1].UserID != "u-bob" || ro.Members[1].Status != "active" || ro.Members[1].Nickname != "Bobby" {
		t.Errorf("bob not restored in place: %+v", ro.Members)
	}
	want(t, e.do("GET", "/v1/groups/"+group, "bob", ""), 200)
}

func TestRemoveMember(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("DELETE", "/v1/groups/"+group+"/members/u-alice", "alice", ""), 403)
	want(t, e.do("DELETE", "/v1/groups/"+group+"/members/u-alice", "carol", ""), 404)
	want(t, e.do("DELETE", "/v1/groups/"+group+"/members/nope", "admin", ""), 404)
	want(t, e.do("DELETE", "/v1/groups/"+group+"/members/u-alice", "admin", ""), 204)
	ro := e.roster(t, group)
	if len(ro.Members) != 2 || ro.Members[0].Status != "removed" || ro.Members[0].Nickname != "A" {
		t.Errorf("row not kept as removed: %+v", ro.Members)
	}
	// Access ends immediately, including for the signed-URL endpoints.
	want(t, e.do("GET", "/v1/groups/"+group+"/events", "alice", ""), 404)
	want(t, e.do("POST", "/v1/groups/"+group+"/downloads", "alice", `{"paths":["`+group+`/bbq00001/thumbnail/`+pid+`.jpg"]}`), 404)
}

func TestConcurrentJoinsAllLand(t *testing.T) {
	e := newEnv(t)
	const n = 5
	toks := make([]string, n)
	for i := range toks {
		toks[i], _ = e.invite(t, "admin", 0)
	}
	// Each joiner is a different account; they all race on the same roster.
	v := idents()
	for i := 0; i < n; i++ {
		v[fmt.Sprintf("u%d", i)] = Identity{Sub: fmt.Sprintf("sub-u%d", i), Email: fmt.Sprintf("u%d@example.com", i), EmailVerified: true}
	}
	e.s.verifier = v
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.do("POST", "/v1/invitations/redeem", fmt.Sprintf("u%d", i), redeemBody(toks[i], fmt.Sprintf("N%d", i))).Code
		}()
	}
	wg.Wait()
	ro := e.roster(t, group)
	for i := 0; i < n; i++ {
		if codes[i] != 200 {
			t.Errorf("joiner %d got %d", i, codes[i])
		}
		if _, ok := ro.active(e.hash(t, fmt.Sprintf("sub-u%d", i))); !ok {
			t.Errorf("joiner %d lost in a roster write conflict", i)
		}
	}
	if len(ro.Members) != 2+n {
		t.Errorf("members = %d, want %d", len(ro.Members), 2+n)
	}
}

func TestInviteKeyIsSharedAcrossInstances(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.invite(t, "admin", 0)
	// A second instance on the same bucket (a different Cloud Run instance) must
	// accept a token the first one issued: the key lives in the bucket.
	other := NewServer(e.s.cfg, idents(), e.st, e.sg)
	other.now = e.s.now
	p, err := other.parseInvite(t.Context(), tok)
	if err != nil || p.G != group {
		t.Fatalf("second instance rejected the token: %v %+v", err, p)
	}
	if got := len(e.st.names("_backend/")); got != 1 {
		t.Errorf("secret objects = %d", got)
	}
}

func TestMyGroupsRestoresOnlyExistingMemberships(t *testing.T) {
	e := newEnv(t)
	e.putRoster("grp_second", "Second", []Member{{UserID: "u-alice2", Sub: "sub-alice", Nickname: "Ali2", Status: "active"}})
	e.putRoster("grp_gone", "Gone", []Member{{UserID: "u-alice3", Sub: "sub-alice", Nickname: "x", Status: "removed"}})
	e.hash(t, "x") // creates _backend/secret.json, which must not be listed as a group
	e.st.put("stray/file.txt", 1, nil, testNow)

	type resp struct {
		Groups []struct {
			GroupID     string `json:"group_id"`
			DisplayName string `json:"display_name"`
			Nickname    string `json:"nickname"`
			UserID      string `json:"user_id"`
		}
	}
	got := decodeBody[resp](t, e.do("GET", "/v1/my/groups", "alice", ""))
	if len(got.Groups) != 2 || got.Groups[0].GroupID != group || got.Groups[1].GroupID != "grp_second" || got.Groups[1].Nickname != "Ali2" || got.Groups[0].UserID != "u-alice" {
		t.Errorf("alice groups = %+v", got.Groups)
	}
	for _, tok := range []string{"carol", "bob"} { // a stranger and a removed member
		w := e.do("GET", "/v1/my/groups", tok, "")
		want(t, w, 200)
		if g := decodeBody[resp](t, w).Groups; len(g) != 0 {
			t.Errorf("%s restored %+v", tok, g)
		}
	}
	want(t, e.do("GET", "/v1/my/groups", "", ""), 401)
}
