package main

import (
	"strings"
	"testing"
)

// sharingRoster is a group where the administrator is a member, with two more
// active members and one who was removed earlier.
func sharingRoster(t *testing.T, e *env) {
	t.Helper()
	e.putRoster("grp_sharing", "Sharing", []Member{
		{UserID: "u-admin", Sub: e.hash(t, "sub-admin"), Nickname: "Boss", Status: "active"},
		{UserID: "u-alice", Sub: e.hash(t, "sub-alice"), Nickname: "A", Status: "active"},
		{UserID: "u-carol", Sub: e.hash(t, "sub-carol"), Nickname: "C", Status: "active"},
		{UserID: "u-bob", Sub: e.hash(t, "sub-bob"), Nickname: "B", Status: "removed"},
	})
}

func TestStopSharingSuspendsEveryoneButTheAdministrator(t *testing.T) {
	e := newEnv(t)
	sharingRoster(t, e)
	want(t, e.do("POST", "/v1/groups/grp_sharing/stop-sharing", "alice", ""), 403) // a member, not an administrator
	want(t, e.do("POST", "/v1/groups/grp_sharing/stop-sharing", "dave", ""), 404)  // a stranger
	want(t, e.do("POST", "/v1/groups/grp_sharing/stop-sharing", "admin", ""), 204)
	ro := e.roster(t, "grp_sharing")
	got := map[string]string{}
	for _, m := range ro.Members {
		got[m.UserID] = m.Status
	}
	if ro.Sharing != "suspended" || got["u-admin"] != "active" || got["u-alice"] != "suspended" || got["u-carol"] != "suspended" || got["u-bob"] != "removed" {
		t.Errorf("roster after stop = %+v", ro)
	}
	// Suspended members are locked out like removed ones, and rows and ids survive.
	want(t, e.do("GET", "/v1/groups/grp_sharing/events", "alice", ""), 404)
	want(t, e.do("GET", "/v1/groups/grp_sharing", "admin", ""), 200)
	// Stopping twice changes nothing.
	want(t, e.do("POST", "/v1/groups/grp_sharing/stop-sharing", "admin", ""), 204)
	if again := e.roster(t, "grp_sharing"); len(again.Members) != 4 || again.Members[1].Status != "suspended" {
		t.Errorf("second stop changed the roster: %+v", again)
	}
}

func TestSuspendedGroupRefusesInvitationsAndJoins(t *testing.T) {
	e := newEnv(t)
	sharingRoster(t, e)
	want(t, e.do("POST", "/v1/groups/grp_sharing/stop-sharing", "admin", ""), 204)
	w := e.do("POST", "/v1/groups/grp_sharing/invitations", "admin", "")
	want(t, w, 409)
	if !strings.Contains(w.Body.String(), "sharing_suspended") {
		t.Errorf("body = %s", w.Body)
	}
}

func TestSuspendedMembersCanBeRemovedAndTheGroupReportsItsState(t *testing.T) {
	e := newEnv(t)
	sharingRoster(t, e)
	want(t, e.do("POST", "/v1/groups/grp_sharing/stop-sharing", "admin", ""), 204)
	want(t, e.do("DELETE", "/v1/groups/grp_sharing/members/u-alice", "admin", ""), 204)
	if st := e.roster(t, "grp_sharing").Members[1].Status; st != "removed" {
		t.Errorf("suspended member not removed: %s", st)
	}
	type detail struct {
		Sharing string `json:"sharing"`
	}
	if d := decodeBody[detail](t, e.do("GET", "/v1/groups/grp_sharing", "admin", "")); d.Sharing != "suspended" {
		t.Errorf("sharing = %q, want suspended", d.Sharing)
	}
}

func TestRestoreSharing(t *testing.T) {
	e := newEnv(t)
	sharingRoster(t, e)
	want(t, e.do("POST", "/v1/groups/grp_sharing/stop-sharing", "admin", ""), 204)
	want(t, e.do("POST", "/v1/groups/grp_sharing/restore-sharing", "alice", ""), 404) // suspended: not even a member now

	// A partial restore leaves the group suspended.
	want(t, e.do("POST", "/v1/groups/grp_sharing/restore-sharing", "admin", `{"user_ids":["u-alice"]}`), 204)
	ro := e.roster(t, "grp_sharing")
	if ro.Sharing != "suspended" || ro.Members[1].Status != "active" || ro.Members[2].Status != "suspended" {
		t.Errorf("after partial restore: %+v", ro)
	}
	want(t, e.do("GET", "/v1/groups/grp_sharing/events", "alice", ""), 200)

	// Restoring the rest lifts the suspension; removed members stay removed.
	want(t, e.do("POST", "/v1/groups/grp_sharing/restore-sharing", "admin", ""), 204)
	ro = e.roster(t, "grp_sharing")
	if ro.Sharing != "" || ro.Members[2].Status != "active" || ro.Members[3].Status != "removed" {
		t.Errorf("after full restore: %+v", ro)
	}
	want(t, e.do("POST", "/v1/groups/grp_sharing/invitations", "admin", ""), 200)
	want(t, e.do("POST", "/v1/groups/grp_sharing/restore-sharing", "admin", ""), 204) // idempotent
}

func TestMyGroupsCarriesCreationTimeAndSharing(t *testing.T) {
	e := newEnv(t)
	sharingRoster(t, e)
	type resp struct {
		Groups []struct {
			GroupID string `json:"group_id"`
			Sharing string `json:"sharing"`
		}
	}
	state := func() string {
		for _, g := range decodeBody[resp](t, e.do("GET", "/v1/my/groups", "admin", "")).Groups {
			if g.GroupID == "grp_sharing" {
				return g.Sharing
			}
		}
		return "missing"
	}
	if got := state(); got != "active" {
		t.Errorf("live group reported %q", got)
	}
	want(t, e.do("POST", "/v1/groups/grp_sharing/stop-sharing", "admin", ""), 204)
	if got := state(); got != "suspended" {
		t.Errorf("suspended group reported %q (the administrator must still see their own group)", got)
	}
}
