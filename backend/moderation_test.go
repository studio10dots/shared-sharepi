package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// join has who (a token in idents()) redeem a fresh invitation and returns their user_id.
func (e *env) join(t *testing.T, who, nick string) string {
	t.Helper()
	tok, _ := e.invite(t, "admin", 0)
	w := e.do("POST", "/v1/invitations/redeem", who, redeemBody(tok, nick))
	want(t, w, 200)
	return decodeBody[struct {
		UserID string `json:"user_id"`
	}](t, w).UserID
}

func (e *env) rawRoster(t *testing.T, g string) string {
	t.Helper()
	b, _, err := e.st.Read(context.Background(), g+"/members.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTermsRequiredToJoin(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.invite(t, "admin", 0)
	for name, body := range map[string]string{
		"missing":      fmt.Sprintf(`{"token":%q,"nickname":"C"}`, tok),
		"empty":        fmt.Sprintf(`{"token":%q,"nickname":"C","terms_version":""}`, tok),
		"bad chars":    fmt.Sprintf(`{"token":%q,"nickname":"C","terms_version":"v 1"}`, tok),
		"too long":     fmt.Sprintf(`{"token":%q,"nickname":"C","terms_version":%q}`, tok, strings.Repeat("1", 33)),
		"wrong type":   fmt.Sprintf(`{"token":%q,"nickname":"C","terms_version":1}`, tok),
		"only a space": fmt.Sprintf(`{"token":%q,"nickname":"C","terms_version":" "}`, tok),
	} {
		w := e.do("POST", "/v1/invitations/redeem", "carol", body)
		if w.Code != 422 {
			t.Errorf("%s: got %d, want 422 (%s)", name, w.Code, w.Body)
		}
	}
	if _, ok := e.roster(t, group).active(e.hash(t, "sub-carol")); ok {
		t.Fatal("joined without accepting the terms")
	}
	// A refused call must not have burnt the invitation.
	want(t, e.do("POST", "/v1/invitations/redeem", "carol", fmt.Sprintf(`{"token":%q,"nickname":"C","terms_version":"2026-09"}`, tok)), 200)
}

func TestLeaveErasesIdentityAndKeepsTheRow(t *testing.T) {
	e := newEnv(t)
	uid := e.join(t, "carol", "Carol")
	e.seedItem("bbq00001", pid, "jpg", uid, testNow) // carol's photo
	if before := e.rawRoster(t, group); !strings.Contains(before, "Carol") || strings.Contains(before, "carol@example.com") || strings.Contains(before, "sub-carol") {
		t.Fatalf("setup: carol should be in the roster by nickname and hash only: %s", before)
	}

	want(t, e.do("POST", "/v1/groups/"+group+"/leave", "carol", ""), 204)

	raw := e.rawRoster(t, group)
	for _, gone := range []string{"carol@example.com", "sub-carol", "Carol"} {
		if strings.Contains(raw, gone) {
			t.Errorf("roster still holds %q after leaving: %s", gone, raw)
		}
	}
	var row Member
	for _, m := range e.roster(t, group).Members {
		if m.UserID == uid {
			row = m
		}
	}
	if row.Status != "left" || row.Sub != "" || row.Email != "" || row.Nickname != "" || row.JoinedAt == "" {
		t.Errorf("row = %+v", row)
	}
	// Nothing else in the roster changed.
	if got := e.roster(t, group).Members[0]; got.UserID != "u-alice" || got.Nickname != "A" || got.Status != "active" {
		t.Errorf("alice = %+v", got)
	}

	// Access ends at once; a second call looks like any non-member's.
	want(t, e.do("GET", "/v1/groups/"+group+"/events", "carol", ""), 404)
	want(t, e.do("POST", "/v1/groups/"+group+"/leave", "carol", ""), 404)
	want(t, e.do("POST", "/v1/groups/"+group+"/leave", "dave", ""), 404) // a stranger, for comparison

	// Clients get the row as a nameless "left" member; the photo still points at it.
	w := e.do("GET", "/v1/groups/"+group, "alice", "")
	want(t, w, 200)
	if strings.Contains(w.Body.String(), "carol") || strings.Contains(w.Body.String(), "Carol") {
		t.Errorf("response still mentions carol: %s", w.Body)
	}
	var found bool
	for _, m := range decodeBody[struct{ Members []publicMember }](t, w).Members {
		if m.UserID == uid {
			found = true
			if m.Status != "left" || m.Nickname != "" {
				t.Errorf("public row = %+v", m)
			}
		}
	}
	if !found {
		t.Error("left member's row was dropped, so old posts would lose their author")
	}
	items := decodeBody[struct{ Items []item }](t, e.do("GET", evPath("bbq00001")+"/items", "alice", "")).Items
	if len(items) != 1 || items[0].Uploader != uid {
		t.Errorf("photo no longer attributed: %+v", items)
	}

	// An administrator removing a left member changes nothing.
	want(t, e.do("DELETE", "/v1/groups/"+group+"/members/"+uid, "admin", ""), 204)
	for _, m := range e.roster(t, group).Members {
		if m.UserID == uid && m.Status != "left" {
			t.Errorf("remove turned a left row into %q", m.Status)
		}
	}
}

func TestJoiningAgainAfterLeavingCreatesANewMember(t *testing.T) {
	e := newEnv(t)
	old := e.join(t, "carol", "Carol")
	want(t, e.do("POST", "/v1/groups/"+group+"/leave", "carol", ""), 204)
	fresh := e.join(t, "carol", "Carol again")
	if fresh == old {
		t.Fatal("rejoining reused the erased row")
	}
	byID := map[string]Member{}
	for _, m := range e.roster(t, group).Members {
		byID[m.UserID] = m
	}
	if byID[old].Status != "left" || byID[fresh].Status != "active" || byID[fresh].Nickname != "Carol again" {
		t.Errorf("rows = %+v", byID)
	}
	want(t, e.do("GET", "/v1/groups/"+group, "carol", ""), 200)
}

func TestRemovedMemberCannotLeave(t *testing.T) {
	e := newEnv(t)
	want(t, e.do("POST", "/v1/groups/"+group+"/leave", "bob", ""), 404) // bob was removed by an administrator
	if m := e.roster(t, group).Members[1]; m.Status != "removed" || m.Email == "" {
		t.Errorf("a removed member's row must stay for the administrator: %+v", m)
	}
}

func reportBody(target, reason, note string) string {
	return fmt.Sprintf(`{"target":%s,"reason":%q,"note":%q}`, target, reason, note)
}

func itemTarget(ev, id string) string {
	return fmt.Sprintf(`{"type":"item","event":%q,"photo_id":%q}`, ev, id)
}

func userTarget(uid string) string { return fmt.Sprintf(`{"type":"user","user_id":%q}`, uid) }

func (e *env) report(t *testing.T, who, body string, code int) string {
	t.Helper()
	w := e.do("POST", "/v1/groups/"+group+"/reports", who, body)
	want(t, w, code)
	if code != 200 && code != 201 {
		return ""
	}
	return decodeBody[struct {
		ReportID string `json:"report_id"`
	}](t, w).ReportID
}

func TestReportAnItem(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-bob", testNow)
	ev := "bbq00001"

	id := e.report(t, "alice", reportBody(itemTarget(ev, pid), "inappropriate", "line one\nline two"), 201)
	data, _, err := e.st.Read(context.Background(), group+"/reports/"+id+".json")
	if err != nil {
		t.Fatalf("report object not stored: %v", err)
	}
	var rp Report
	_ = json.Unmarshal(data, &rp)
	if rp.ReportID != id || rp.ReporterUserID != "u-alice" || rp.Status != "open" || rp.Reason != "inappropriate" ||
		rp.Target != (reportTarget{Type: "item", Event: ev, PhotoID: pid}) || rp.Note != "line one\nline two" || rp.CreatedAt == "" {
		t.Errorf("report = %+v", rp)
	}

	if rp.Original != group+"/"+ev+"/original/"+pid+".jpg" {
		t.Errorf("original = %q", rp.Original)
	}

	// Exactly the same report twice is a no-op that returns the first one.
	if again := e.report(t, "alice", reportBody(itemTarget(ev, pid), "inappropriate", "line one\nline two"), 200); again != id {
		t.Errorf("duplicate report created %s, want %s", again, id)
	}
	if n := len(e.st.names(group + "/reports/")); n != 1 {
		t.Errorf("report objects = %d", n)
	}
	// Another reason (or note) for the same item is a new report, so the
	// administrator hears it.
	if other := e.report(t, "alice", reportBody(itemTarget(ev, pid), "spam", ""), 201); other == id {
		t.Error("a report with another reason was dropped")
	}
	if other := e.report(t, "alice", reportBody(itemTarget(ev, pid), "spam", "more"), 201); other == id {
		t.Error("a report with another note was dropped")
	}

	for name, body := range map[string]string{
		"bad reason":      reportBody(itemTarget(ev, pid), "boring", ""),
		"empty reason":    reportBody(itemTarget(ev, pid), "", ""),
		"note too long":   reportBody(itemTarget(ev, pid), "spam", strings.Repeat("x", maxReportNote+1)),
		"control in note": reportBody(itemTarget(ev, pid), "spam", "a\x07b"),
		"bad event":       reportBody(itemTarget("short", pid), "spam", ""),
		"event with ..":   reportBody(itemTarget("..", pid), "spam", ""),
		"bad photo id":    reportBody(itemTarget(ev, "nope"), "spam", ""),
		"item with user":  reportBody(`{"type":"item","event":"`+ev+`","photo_id":"`+pid+`","user_id":"u-bob"}`, "spam", ""),
		"unknown type":    reportBody(`{"type":"group"}`, "spam", ""),
		"no target":       `{"reason":"spam"}`,
	} {
		if w := e.do("POST", "/v1/groups/"+group+"/reports", "alice", body); w.Code != 422 {
			t.Errorf("%s: got %d, want 422", name, w.Code)
		}
	}
	e.report(t, "alice", reportBody(itemTarget(ev, pid2), "spam", ""), 404)        // no such item
	e.report(t, "alice", reportBody(itemTarget("other001", pid), "spam", ""), 404) // no such event
	e.report(t, "carol", reportBody(itemTarget(ev, pid), "spam", ""), 404)         // a stranger
	e.report(t, "bob", reportBody(itemTarget(ev, pid), "spam", ""), 404)           // a removed member
	e.report(t, "", reportBody(itemTarget(ev, pid), "spam", ""), 401)
}

func TestReportAUser(t *testing.T) {
	e := newEnv(t)
	carol := e.join(t, "carol", "Carol")
	e.report(t, "alice", reportBody(userTarget(carol), "harassment", "rude nickname"), 201)
	e.report(t, "alice", reportBody(userTarget("u-alice"), "spam", ""), 422) // yourself
	e.report(t, "alice", reportBody(userTarget("ghost"), "spam", ""), 404)   // unknown user
	e.report(t, "alice", reportBody(userTarget(""), "spam", ""), 422)
	e.report(t, "alice", reportBody(`{"type":"user","user_id":"`+carol+`","photo_id":"`+pid+`"}`, "spam", ""), 422)
	e.report(t, "carol", reportBody(userTarget("u-alice"), "spam", ""), 201) // reporting is symmetrical

	want(t, e.do("POST", "/v1/groups/"+group+"/leave", "carol", ""), 204)
	// Someone who left has no identity to act on.
	e.report(t, "alice", reportBody(userTarget(carol), "spam", ""), 404)
	// A member the administrator removed can still be reported (the row is kept for moderation).
	e.report(t, "alice", reportBody(userTarget("u-bob"), "spam", ""), 201)
}

func TestReportCapPerReporter(t *testing.T) {
	e := newEnv(t)
	ids := make([]string, maxOpenReportsPerReporter+1)
	for i := range ids {
		ids[i] = newUUID()
		e.seedItem("bbq00001", ids[i], "jpg", "u-bob", testNow)
	}
	for i := 0; i < maxOpenReportsPerReporter; i++ {
		e.report(t, "alice", reportBody(itemTarget("bbq00001", ids[i]), "spam", ""), 201)
	}
	e.report(t, "alice", reportBody(itemTarget("bbq00001", ids[maxOpenReportsPerReporter]), "spam", ""), 429)
	// Repeating one already reported still succeeds, and another member is unaffected.
	e.report(t, "alice", reportBody(itemTarget("bbq00001", ids[0]), "spam", ""), 200)
	carol := e.join(t, "carol", "Carol")
	_ = carol
	e.report(t, "carol", reportBody(itemTarget("bbq00001", ids[maxOpenReportsPerReporter]), "spam", ""), 201)
}

func TestOnlyAdministratorsListReports(t *testing.T) {
	e := newEnv(t)
	carol := e.join(t, "carol", "Carol")
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	e.seedItem("bbq00001", pid2, "jpg", "u-alice", testNow)

	*e.clock = testNow.Add(time.Minute)
	first := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid), "spam", "one"), 201)
	*e.clock = testNow.Add(2 * time.Minute)
	second := e.report(t, "carol", reportBody(userTarget("u-alice"), "harassment", "two"), 201)
	*e.clock = testNow.Add(3 * time.Minute)
	third := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid2), "other", ""), 201)

	want(t, e.do("GET", "/v1/groups/"+group+"/reports", "alice", ""), 403) // a member, not an administrator
	want(t, e.do("GET", "/v1/groups/"+group+"/reports", "dave", ""), 404)  // a stranger
	want(t, e.do("GET", "/v1/groups/"+group+"/reports", "", ""), 401)

	w := e.do("GET", "/v1/groups/"+group+"/reports", "admin", "") // not even a member
	want(t, w, 200)
	for _, secret := range []string{"alice@example.com", "carol@example.com", "sub-alice", "sub-carol", `"email"`, `"sub"`} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("report list leaks %q", secret)
		}
	}
	type entry struct {
		ReportID string `json:"report_id"`
		Reporter struct {
			UserID   string `json:"user_id"`
			Nickname string `json:"nickname"`
		} `json:"reporter"`
		Target         reportTarget `json:"target"`
		TargetNickname string       `json:"target_nickname"`
		Reason         string       `json:"reason"`
		Note           string       `json:"note"`
	}
	got := decodeBody[struct{ Reports []entry }](t, w).Reports
	if len(got) != 3 || got[0].ReportID != third || got[1].ReportID != second || got[2].ReportID != first {
		t.Fatalf("not newest first: %+v", got)
	}
	if got[1].Reporter.UserID != carol || got[1].Reporter.Nickname != "Carol" || got[1].TargetNickname != "A" || got[1].Reason != "harassment" || got[1].Note != "two" {
		t.Errorf("second = %+v", got[1])
	}
	if got[2].Reporter.Nickname != "A" || got[2].Target.PhotoID != pid {
		t.Errorf("first = %+v", got[2])
	}
}

func TestResolveReport(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-bob", testNow)
	id := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid), "spam", ""), 201)
	path := "/v1/groups/" + group + "/reports/" + id + "/resolve"

	want(t, e.do("POST", path, "alice", ""), 403) // a member cannot close the report they filed
	want(t, e.do("POST", path, "dave", ""), 404)
	want(t, e.do("POST", path, "", ""), 401)
	want(t, e.do("POST", "/v1/groups/"+group+"/reports/"+newUUID()+"/resolve", "admin", ""), 404)
	want(t, e.do("POST", "/v1/groups/"+group+"/reports/not-a-uuid/resolve", "admin", ""), 404)
	if list := decodeBody[struct{ Reports []any }](t, e.do("GET", "/v1/groups/"+group+"/reports", "admin", "")).Reports; len(list) != 1 {
		t.Fatalf("refused calls closed the report: %d open", len(list))
	}

	*e.clock = testNow.Add(time.Hour)
	want(t, e.do("POST", path, "admin", ""), 200)
	want(t, e.do("POST", path, "admin", ""), 200) // resolving twice is harmless

	data, _, _ := e.st.Read(context.Background(), group+"/reports/"+id+".json")
	var rp Report
	_ = json.Unmarshal(data, &rp)
	if rp.Status != "resolved" || rp.ResolvedAt != testNow.Add(time.Hour).UTC().Format(time.RFC3339) || rp.Reason != "spam" {
		t.Errorf("stored report = %+v", rp)
	}
	if list := decodeBody[struct{ Reports []any }](t, e.do("GET", "/v1/groups/"+group+"/reports", "admin", "")).Reports; len(list) != 0 {
		t.Errorf("resolved report still listed: %v", list)
	}
	// Resolving frees the reporter's slot: the same item can be reported again as a new report.
	if again := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid), "spam", ""), 201); again == id {
		t.Error("a resolved report was reused")
	}
}

func TestReportsAreNotPhotosOrEvents(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	id := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid), "spam", ""), 201)

	got := decodeBody[struct{ Events []event }](t, e.do("GET", "/v1/groups/"+group+"/events", "alice", "")).Events
	if len(got) != 1 || got[0].Name != "BBQ" {
		t.Errorf("events = %+v", got)
	}
	// Members cannot read report files through the signed-URL endpoint either.
	w := e.do("POST", "/v1/groups/"+group+"/downloads", "alice", `{"paths":["`+group+`/reports/`+id+`.json"]}`)
	want(t, w, 422)
	if len(e.sg.calls) != 0 {
		t.Error("signed a report file for a member")
	}
}

func TestDeletingAReportedItemLeavesTheReportForTheAdministrator(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-bob", testNow)
	id := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid), "inappropriate", ""), 201)
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "admin", ""), 404) // the administrator is not a member
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "alice", ""), 204)
	if !e.st.has(group + "/reports/" + id + ".json") {
		t.Error("deleting the item removed the evidence of the report")
	}
	want(t, e.do("POST", "/v1/groups/"+group+"/reports/"+id+"/resolve", "admin", ""), 200)
}

func openReportsOf(t *testing.T, e *env, who string) map[string][]string {
	t.Helper()
	w := e.do("GET", "/v1/reports/open", who, "")
	want(t, w, 200)
	return decodeBody[struct {
		Groups map[string][]string `json:"groups"`
	}](t, w).Groups
}

func TestOpenReportsListsOnlyOpenOnes(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-bob", testNow)
	e.seedItem("bbq00001", pid2, "jpg", "u-bob", testNow)
	first := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid), "spam", ""), 201)
	second := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid2), "spam", ""), 201)

	want(t, e.do("GET", "/v1/reports/open", "alice", ""), 403) // not an administrator
	want(t, e.do("GET", "/v1/reports/open", "", ""), 401)

	got := openReportsOf(t, e, "admin")[group]
	if len(got) != 2 || !contains(got, first) || !contains(got, second) {
		t.Fatalf("open = %v", got)
	}
	want(t, e.do("POST", "/v1/groups/"+group+"/reports/"+first+"/resolve", "admin", ""), 200)
	if got := openReportsOf(t, e, "admin")[group]; len(got) != 1 || got[0] != second {
		t.Errorf("after resolving: %v", got)
	}
	// The group's own answer carries them too, for an administrator only.
	w := e.do("GET", "/v1/groups/"+group, "admin", "")
	want(t, w, 200)
	if ids := decodeBody[struct {
		OpenReports []string `json:"open_reports"`
	}](t, w).OpenReports; len(ids) != 1 || ids[0] != second {
		t.Errorf("group open_reports = %v", ids)
	}
	if strings.Contains(e.do("GET", "/v1/groups/"+group, "alice", "").Body.String(), "open_reports") {
		t.Error("a member was told about reports")
	}
	// The markers are not reports and not events.
	if list := decodeBody[struct{ Reports []any }](t, e.do("GET", "/v1/groups/"+group+"/reports?include=resolved", "admin", "")).Reports; len(list) != 2 {
		t.Errorf("reports with resolved = %d", len(list))
	}
}

func TestAReportResolvedBeforeMarkersIsNeverCountedAsOpen(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-bob", testNow)
	id := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid), "spam", ""), 201)
	// As an older backend left it: resolved in the file, no marker.
	data, generation, _ := e.st.Read(context.Background(), group+"/reports/"+id+".json")
	var rp Report
	_ = json.Unmarshal(data, &rp)
	rp.Status = "resolved"
	b, _ := json.Marshal(rp)
	if _, err := e.st.Write(context.Background(), group+"/reports/"+id+".json", b, gen(generation)); err != nil {
		t.Fatal(err)
	}
	if got := openReportsOf(t, e, "admin")[group]; len(got) != 0 {
		t.Fatalf("a resolved report counted as open: %v", got)
	}
	if !e.st.has(group + "/reports/resolved/" + id) {
		t.Error("the check did not write the missing marker")
	}
}

func TestListReportsWithResolved(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-bob", testNow)
	id := e.report(t, "alice", reportBody(itemTarget("bbq00001", pid), "spam", ""), 201)
	*e.clock = testNow.Add(time.Hour)
	want(t, e.do("POST", "/v1/groups/"+group+"/reports/"+id+"/resolve", "admin", ""), 200)

	type entry struct {
		ReportID   string `json:"report_id"`
		Status     string `json:"status"`
		ResolvedAt string `json:"resolved_at"`
		Original   string `json:"original"`
	}
	if open := decodeBody[struct{ Reports []entry }](t, e.do("GET", "/v1/groups/"+group+"/reports", "admin", "")).Reports; len(open) != 0 {
		t.Errorf("default list shows resolved: %+v", open)
	}
	all := decodeBody[struct{ Reports []entry }](t, e.do("GET", "/v1/groups/"+group+"/reports?include=resolved", "admin", "")).Reports
	if len(all) != 1 || all[0].Status != "resolved" || all[0].ResolvedAt == "" || all[0].Original != group+"/bbq00001/original/"+pid+".jpg" {
		t.Errorf("with resolved = %+v", all)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestTheMemberListMarksAdministrators(t *testing.T) {
	e := newEnv(t)
	carol := e.join(t, "carol", "Carol")
	type member struct {
		UserID string `json:"user_id"`
		Admin  bool   `json:"admin"`
	}
	members := func() map[string]bool {
		w := e.do("GET", "/v1/groups/"+group, "alice", "")
		want(t, w, 200)
		out := map[string]bool{}
		for _, m := range decodeBody[struct{ Members []member }](t, w).Members {
			out[m.UserID] = m.Admin
		}
		return out
	}
	if got := members(); got[carol] {
		t.Errorf("an ordinary member marked as administrator: %v", got)
	}
	// Carol becomes an administrator: her next call records it.
	e.s.cfg.AdminEmails["carol@example.com"] = true
	want(t, e.do("GET", "/v1/groups/"+group, "carol", ""), 200)
	if got := members(); !got[carol] {
		t.Errorf("administrator not marked: %v", got)
	}
	// And no longer: the mark goes on her next call.
	delete(e.s.cfg.AdminEmails, "carol@example.com")
	want(t, e.do("GET", "/v1/groups/"+group+"/events", "carol", ""), 200)
	if got := members(); got[carol] {
		t.Errorf("mark kept after ADMIN_EMAILS changed: %v", got)
	}
}
