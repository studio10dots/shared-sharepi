package main

import (
	"strings"
	"testing"
)

// planRoster is a group where the administrator is a member, with two more
// active members and one who was removed earlier.
func planRoster(t *testing.T, e *env) {
	t.Helper()
	e.putRoster("grp_plan", "Plan", []Member{
		{UserID: "u-admin", Sub: e.hash(t, "sub-admin"), Nickname: "Boss", Status: "active"},
		{UserID: "u-alice", Sub: e.hash(t, "sub-alice"), Nickname: "A", Status: "active"},
		{UserID: "u-carol", Sub: e.hash(t, "sub-carol"), Nickname: "C", Status: "active"},
		{UserID: "u-bob", Sub: e.hash(t, "sub-bob"), Nickname: "B", Status: "removed"},
	})
}

func TestStopSharingSuspendsEveryoneButTheAdministrator(t *testing.T) {
	e := newEnv(t)
	planRoster(t, e)
	want(t, e.do("POST", "/v1/groups/grp_plan/stop-sharing", "alice", ""), 403) // a member, not an administrator
	want(t, e.do("POST", "/v1/groups/grp_plan/stop-sharing", "dave", ""), 404)  // a stranger
	want(t, e.do("POST", "/v1/groups/grp_plan/stop-sharing", "admin", ""), 204)
	ro := e.roster(t, "grp_plan")
	got := map[string]string{}
	for _, m := range ro.Members {
		got[m.UserID] = m.Status
	}
	if ro.Sharing != "suspended" || got["u-admin"] != "active" || got["u-alice"] != "suspended" || got["u-carol"] != "suspended" || got["u-bob"] != "removed" {
		t.Errorf("roster after stop = %+v", ro)
	}
	// Suspended members are locked out like removed ones, and rows and ids survive.
	want(t, e.do("GET", "/v1/groups/grp_plan/events", "alice", ""), 404)
	want(t, e.do("GET", "/v1/groups/grp_plan", "admin", ""), 200)
	// Stopping twice changes nothing.
	want(t, e.do("POST", "/v1/groups/grp_plan/stop-sharing", "admin", ""), 204)
	if again := e.roster(t, "grp_plan"); len(again.Members) != 4 || again.Members[1].Status != "suspended" {
		t.Errorf("second stop changed the roster: %+v", again)
	}
}

func TestSuspendedGroupRefusesInvitationsAndJoins(t *testing.T) {
	e := newEnv(t)
	planRoster(t, e)
	want(t, e.do("POST", "/v1/groups/grp_plan/stop-sharing", "admin", ""), 204)
	w := e.do("POST", "/v1/groups/grp_plan/invitations", "admin", "")
	want(t, w, 409)
	if !strings.Contains(w.Body.String(), "sharing_suspended") {
		t.Errorf("body = %s", w.Body)
	}
}

func TestSuspendedMembersCanBeRemovedAndTheGroupReportsItsState(t *testing.T) {
	e := newEnv(t)
	planRoster(t, e)
	want(t, e.do("POST", "/v1/groups/grp_plan/stop-sharing", "admin", ""), 204)
	want(t, e.do("DELETE", "/v1/groups/grp_plan/members/u-alice", "admin", ""), 204)
	if st := e.roster(t, "grp_plan").Members[1].Status; st != "removed" {
		t.Errorf("suspended member not removed: %s", st)
	}
	type detail struct {
		Sharing string `json:"sharing"`
	}
	if d := decodeBody[detail](t, e.do("GET", "/v1/groups/grp_plan", "admin", "")); d.Sharing != "suspended" {
		t.Errorf("sharing = %q, want suspended", d.Sharing)
	}
}

func TestRestoreSharing(t *testing.T) {
	e := newEnv(t)
	planRoster(t, e)
	want(t, e.do("POST", "/v1/groups/grp_plan/stop-sharing", "admin", ""), 204)
	want(t, e.do("POST", "/v1/groups/grp_plan/restore-sharing", "alice", ""), 404) // suspended: not even a member now

	// A partial restore leaves the group suspended.
	want(t, e.do("POST", "/v1/groups/grp_plan/restore-sharing", "admin", `{"user_ids":["u-alice"]}`), 204)
	ro := e.roster(t, "grp_plan")
	if ro.Sharing != "suspended" || ro.Members[1].Status != "active" || ro.Members[2].Status != "suspended" {
		t.Errorf("after partial restore: %+v", ro)
	}
	want(t, e.do("GET", "/v1/groups/grp_plan/events", "alice", ""), 200)

	// Restoring the rest lifts the suspension; removed members stay removed.
	want(t, e.do("POST", "/v1/groups/grp_plan/restore-sharing", "admin", ""), 204)
	ro = e.roster(t, "grp_plan")
	if ro.Sharing != "" || ro.Members[2].Status != "active" || ro.Members[3].Status != "removed" {
		t.Errorf("after full restore: %+v", ro)
	}
	want(t, e.do("POST", "/v1/groups/grp_plan/invitations", "admin", ""), 200)
	want(t, e.do("POST", "/v1/groups/grp_plan/restore-sharing", "admin", ""), 204) // idempotent
}

func TestMyGroupsCarriesCreationTimeAndSharing(t *testing.T) {
	e := newEnv(t)
	planRoster(t, e)
	type resp struct {
		Groups []struct {
			GroupID string `json:"group_id"`
			Sharing string `json:"sharing"`
		}
	}
	state := func() string {
		for _, g := range decodeBody[resp](t, e.do("GET", "/v1/my/groups", "admin", "")).Groups {
			if g.GroupID == "grp_plan" {
				return g.Sharing
			}
		}
		return "missing"
	}
	if got := state(); got != "active" {
		t.Errorf("live group reported %q", got)
	}
	want(t, e.do("POST", "/v1/groups/grp_plan/stop-sharing", "admin", ""), 204)
	if got := state(); got != "suspended" {
		t.Errorf("suspended group reported %q (the administrator must still see their own group)", got)
	}
}
