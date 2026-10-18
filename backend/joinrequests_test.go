package main

import (
	"strings"
	"testing"
)

type listedJoinRequest struct {
	ID          string `json:"id"`
	Nickname    string `json:"nickname"`
	Note        string `json:"note"`
	RequestedAt string `json:"requested_at"`
}

func joinPath(g string) string { return "/v1/groups/" + g + "/join-requests" }

func (e *env) askToJoin(who, nick, note string) int {
	body := `{"nickname":"` + nick + `","note":"` + note + `"}`
	return e.do("POST", joinPath(group), who, body).Code
}

func (e *env) openRequests(t *testing.T) []listedJoinRequest {
	t.Helper()
	w := e.do("GET", joinPath(group), "admin", "")
	want(t, w, 200)
	return decodeBody[struct {
		Requests []listedJoinRequest `json:"requests"`
	}](t, w).Requests
}

func (e *env) joinObjects() int {
	n := 0
	for name := range e.st.objs {
		if strings.HasPrefix(name, group+"/joins/") {
			n++
		}
	}
	return n
}

func TestAStrangerCanAskToBeInvited(t *testing.T) {
	e := newEnv(t)

	if got := e.askToJoin("carol", "Carol", "from Alice's link"); got != 202 {
		t.Fatalf("got %d, want 202", got)
	}

	reqs := e.openRequests(t)
	if len(reqs) != 1 {
		t.Fatalf("requests = %+v", reqs)
	}
	r := reqs[0]
	if r.Nickname != "Carol" || r.Note != "from Alice's link" || r.RequestedAt == "" {
		t.Errorf("request = %+v", r)
	}
	// The id is part of the keyed member id, never the Google id or the email.
	if !joinKeyRe.MatchString(r.ID) || strings.Contains(r.ID, "carol") {
		t.Errorf("id %q is not an opaque key", r.ID)
	}
}

func TestAskingAgainReplacesTheRequest(t *testing.T) {
	e := newEnv(t)
	e.askToJoin("carol", "Carol", "first")

	e.askToJoin("carol", "Carol C", "second")

	reqs := e.openRequests(t)
	if len(reqs) != 1 || reqs[0].Nickname != "Carol C" || reqs[0].Note != "second" {
		t.Errorf("requests = %+v, want one, replaced", reqs)
	}
}

func TestTwoPeopleMakeTwoRequestsOldestFirst(t *testing.T) {
	e := newEnv(t)
	e.askToJoin("carol", "Carol", "")
	*e.clock = e.clock.Add(60e9)
	e.askToJoin("dave", "Dave", "")

	reqs := e.openRequests(t)

	if len(reqs) != 2 || reqs[0].Nickname != "Carol" || reqs[1].Nickname != "Dave" {
		t.Errorf("requests = %+v", reqs)
	}
}

func TestAMemberIsToldTheyAreOneAndLeavesNoRequest(t *testing.T) {
	e := newEnv(t)

	w := e.do("POST", joinPath(group), "alice", `{"nickname":"Alice"}`)

	want(t, w, 200)
	if !strings.Contains(w.Body.String(), `"member"`) {
		t.Errorf("body = %s", w.Body)
	}
	if e.joinObjects() != 0 {
		t.Error("a member left a request behind")
	}
}

func TestARemovedMemberMayAskAgain(t *testing.T) {
	e := newEnv(t)

	if got := e.askToJoin("bob", "Bob", ""); got != 202 {
		t.Fatalf("got %d, want 202", got)
	}
	if len(e.openRequests(t)) != 1 {
		t.Error("the removed member's request is missing")
	}
}

func TestAGroupThatDoesNotExistAnswersLikeOneThatDoes(t *testing.T) {
	e := newEnv(t)

	w := e.do("POST", joinPath("grp_nowhere"), "carol", `{"nickname":"Carol"}`)

	// The same "sent" a real group gives a stranger, and nothing is written.
	want(t, w, 202)
	if !strings.Contains(w.Body.String(), `"sent"`) {
		t.Errorf("body = %s", w.Body)
	}
	for name := range e.st.objs {
		if strings.Contains(name, "grp_nowhere") {
			t.Errorf("wrote %s for a group that does not exist", name)
		}
	}
}

func TestAnInvalidGroupIdOrNicknameOrNoteIsRefused(t *testing.T) {
	e := newEnv(t)

	want(t, e.do("POST", joinPath("Bad_Group"), "carol", `{"nickname":"C"}`), 422)
	want(t, e.do("POST", joinPath(group), "carol", `{"nickname":""}`), 422)
	want(t, e.do("POST", joinPath(group), "carol", `{"nickname":"`+strings.Repeat("a", 31)+`"}`), 422)
	want(t, e.do("POST", joinPath(group), "carol", `{"nickname":"C","note":"`+strings.Repeat("n", maxJoinNote+1)+`"}`), 422)
	want(t, e.do("POST", joinPath(group), "carol", `not json`), 422)
	if e.joinObjects() != 0 {
		t.Error("a refused request was stored")
	}
}

func TestAskingNeedsASignIn(t *testing.T) {
	e := newEnv(t)

	want(t, e.do("POST", joinPath(group), "", `{"nickname":"C"}`), 401)
	want(t, e.do("POST", joinPath(group), "unverf", `{"nickname":"C"}`), 401)
}

func TestTheNumberOfRequestsIsBoundedAndAnswersStaySilent(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < maxJoinRequests; i++ {
		e.st.put(group+"/joins/"+strings.Repeat("0", 30)+twoDigits(i)+".json", 1, nil, testNow)
	}

	// A new person is not let in, and is not told so.
	if got := e.askToJoin("carol", "Carol", ""); got != 202 {
		t.Fatalf("got %d, want 202", got)
	}
	if e.joinObjects() != maxJoinRequests {
		t.Errorf("%d requests, want %d", e.joinObjects(), maxJoinRequests)
	}
}

func twoDigits(i int) string {
	const d = "0123456789"
	return string(d[i/10%10]) + string(d[i%10])
}

func TestOnlyTheOwnerReadsAndDismissesRequests(t *testing.T) {
	e := newEnv(t)
	e.askToJoin("carol", "Carol", "")
	id := e.openRequests(t)[0].ID

	// A member who is not the owner: 403. A stranger: 404, as everywhere.
	want(t, e.do("GET", joinPath(group), "alice", ""), 403)
	want(t, e.do("DELETE", joinPath(group)+"/"+id, "alice", ""), 403)
	want(t, e.do("GET", joinPath(group), "carol", ""), 404)
	want(t, e.do("GET", joinPath(group), "", ""), 401)
	if len(e.openRequests(t)) != 1 {
		t.Fatal("a refused call changed the requests")
	}

	want(t, e.do("DELETE", joinPath(group)+"/"+id, "admin", ""), 204)
	if len(e.openRequests(t)) != 0 {
		t.Error("the request is still there")
	}
	// Dismissing twice is fine; a malformed id is refused.
	want(t, e.do("DELETE", joinPath(group)+"/"+id, "admin", ""), 204)
	want(t, e.do("DELETE", joinPath(group)+"/not-a-key", "admin", ""), 422)
}

func TestTheOwnerSeesHowManyRequestsEachGroupHas(t *testing.T) {
	e := newEnv(t)
	e.askToJoin("carol", "Carol", "")
	e.askToJoin("dave", "Dave", "")
	// Another group, with none.
	e.putRoster("grp_quiet", "Quiet", []Member{{UserID: "u-x", Sub: "sub-x", Status: "active"}})

	w := e.do("GET", "/v1/join-requests/open", "admin", "")

	want(t, w, 200)
	got := decodeBody[struct {
		Groups map[string]int `json:"groups"`
	}](t, w).Groups
	if len(got) != 1 || got[group] != 2 {
		t.Errorf("groups = %+v, want only %s with 2", got, group)
	}
	want(t, e.do("GET", "/v1/join-requests/open", "alice", ""), 403)
	want(t, e.do("GET", "/v1/join-requests/open", "", ""), 401)
}

func TestJoiningWithAnInvitationDismissesTheRequest(t *testing.T) {
	e := newEnv(t)
	e.askToJoin("carol", "Carol", "")
	e.askToJoin("dave", "Dave", "")
	tok, _ := e.invite(t, "admin", 0)

	want(t, e.do("POST", "/v1/invitations/redeem", "carol", redeemBody(tok, "Carol")), 200)

	reqs := e.openRequests(t)
	if len(reqs) != 1 || reqs[0].Nickname != "Dave" {
		t.Errorf("requests = %+v, want only Dave's left", reqs)
	}
}

func TestARequestIsNotAnEventNorAGroupAndGrantsNothing(t *testing.T) {
	e := newEnv(t)
	e.askToJoin("carol", "Carol", "")

	// "joins" is a reserved folder, not an event (event ids have 8 or more
	// characters), so it is not listed as one.
	if validEventID("joins") {
		t.Error("joins is a valid event id")
	}
	for _, ev := range e.events(t, "alice") {
		if ev.ID == "joins" {
			t.Error("joins/ listed as an event")
		}
	}
	// Asking opened nothing for the asker.
	want(t, e.do("GET", "/v1/groups/"+group+"/events", "carol", ""), 404)
}
