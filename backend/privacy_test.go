package main

import (
	"strings"
	"testing"
)

func TestRosterKeepsNeitherEmailNorGoogleID(t *testing.T) {
	e := newEnv(t)
	e.join(t, "carol", "Carol")
	want(t, e.do("POST", "/v1/groups", "admin", createBody("grp_new", "New", "Boss")), 201)

	for _, g := range []string{group, "grp_new"} {
		raw := e.rawRoster(t, g)
		for _, secret := range []string{"@example.com", "sub-carol", "sub-admin", "sub-alice"} {
			if strings.Contains(raw, secret) && !(g == group && secret == "sub-alice") {
				t.Errorf("%s roster holds %q: %s", g, secret, raw)
			}
		}
		if !strings.Contains(raw, `"h1:`) {
			t.Errorf("%s roster has no hashed id: %s", g, raw)
		}
	}
}

func TestHashIsStablePerBackendAndDiffersBetweenPeople(t *testing.T) {
	e := newEnv(t)
	a, b := e.hash(t, "sub-carol"), e.hash(t, "sub-dave")
	if a == b || a != e.hash(t, "sub-carol") {
		t.Errorf("hashes: %s %s", a, b)
	}
	// Another backend (another secret) hashes the same person differently.
	other := newEnv(t)
	if other.hash(t, "sub-carol") == a {
		t.Error("two backends produced the same hash: the key is not used")
	}
}

func TestRostersFromBeforeHashingAreUpgradedOnUse(t *testing.T) {
	e := newEnv(t)
	// alice's row holds her raw Google id and an email, as older rosters did.
	e.putRoster(group, "BBQ", []Member{
		{UserID: "u-alice", Sub: "sub-alice", Email: "alice@example.com", Nickname: "A", Status: "active"},
		{UserID: "u-old", Sub: "sub-old", Email: "old@example.com", Nickname: "Old", Status: "active"},
	})

	want(t, e.do("GET", "/v1/groups/"+group, "alice", ""), 200) // still recognised

	raw := e.rawRoster(t, group)
	if strings.Contains(raw, "sub-alice") {
		t.Errorf("alice's raw id is still stored: %s", raw)
	}
	if strings.Contains(raw, "@example.com") {
		t.Errorf("an email is still stored: %s", raw)
	}
	if _, ok := e.roster(t, group).active(e.hash(t, "sub-alice")); !ok {
		t.Error("alice's row was not upgraded to her hash")
	}
	want(t, e.do("GET", "/v1/groups/"+group, "alice", ""), 200) // and works after the upgrade
}

func TestARemovedMemberComingBackKeepsTheirUserIDAcrossTheUpgrade(t *testing.T) {
	e := newEnv(t)
	// bob is "removed" with a raw id in the old roster; a new invitation reactivates him.
	tok, _ := e.invite(t, "admin", 5)
	want(t, e.do("POST", "/v1/invitations/redeem", "bob", redeemBody(tok, "Bobby")), 200)

	var row Member
	for _, m := range e.roster(t, group).Members {
		if m.UserID == "u-bob" {
			row = m
		}
	}
	if row.Status != "active" || row.Sub != e.hash(t, "sub-bob") || row.Email != "" {
		t.Errorf("row = %+v", row)
	}
}
