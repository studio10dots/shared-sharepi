package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Moderation lives here because the publisher runs no server: reports go to the
// backend's administrator, as objects under the group prefix.

var (
	reportReasons = map[string]bool{"inappropriate": true, "sexual": true, "minor": true, "harassment": true, "spam": true, "other": true}
	reportIDRe    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	termsVersion  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
)

const (
	maxReportNote             = 300
	maxOpenReportsPerReporter = 20  // a member cannot flood the administrator
	maxReportsScanned         = 500 // bounds the work of one request
)

type reportTarget struct {
	Type    string `json:"type"` // item | user
	Event   string `json:"event,omitempty"`
	PhotoID string `json:"photo_id,omitempty"`
	UserID  string `json:"user_id,omitempty"`
}

// Report is the object stored at {group}/reports/{report_id}.json.
type Report struct {
	ReportID       string       `json:"report_id"`
	ReporterUserID string       `json:"reporter_user_id"`
	Target         reportTarget `json:"target"`
	Reason         string       `json:"reason"`
	Note           string       `json:"note"`
	CreatedAt      string       `json:"created_at"`
	Status         string       `json:"status"` // open | resolved
	ResolvedAt     string       `json:"resolved_at,omitempty"`
	// Original is the reported item's original object, found when the report
	// was filed, so the administrator can see and open it. Empty for a user
	// report and for reports filed before it was kept.
	Original string `json:"original,omitempty"`
}

func reportName(g, id string) string { return g + "/reports/" + id + ".json" }

// resolvedMarkerName is an empty object written when a report is resolved. The
// report file stays the record; the markers let the open reports be found from
// two listings, without reading every report ever filed (the administrator's
// app asks about once a minute).
func resolvedMarkerName(g, id string) string { return g + "/reports/resolved/" + id }

// openReportIDs lists the ids of g's open reports. A report without a marker
// is read to be sure it is open: one resolved before markers existed (or whose
// marker write failed) gets its marker then and is not counted. So the reports
// read on each check are only the open ones, and each old one once.
func (s *Server) openReportIDs(ctx context.Context, g string) ([]string, error) {
	files, err := s.store.List(ctx, g+"/reports/", "/", false)
	if err != nil {
		return nil, err
	}
	markers, err := s.store.List(ctx, g+"/reports/resolved/", "", false)
	if err != nil {
		return nil, err
	}
	resolved := map[string]bool{}
	for _, o := range markers.Objects {
		resolved[strings.TrimPrefix(o.Name, g+"/reports/resolved/")] = true
	}
	var unmarked []string
	for _, o := range files.Objects {
		id := strings.TrimSuffix(strings.TrimPrefix(o.Name, g+"/reports/"), ".json")
		if reportIDRe.MatchString(id) && !resolved[id] {
			unmarked = append(unmarked, id)
		}
	}
	open := make([]bool, len(unmarked))
	var mu sync.Mutex
	var firstErr error
	parallel(8, unmarked, func(i int, id string) {
		data, _, err := s.store.Read(ctx, reportName(g, id))
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				mu.Lock()
				firstErr = err
				mu.Unlock()
			}
			return
		}
		var rp Report
		if json.Unmarshal(data, &rp) != nil {
			return
		}
		if rp.Status == "resolved" {
			_ = s.markResolved(ctx, g, id)
			return
		}
		open[i] = true
	})
	if firstErr != nil {
		return nil, firstErr
	}
	ids := []string{}
	for i, id := range unmarked {
		if open[i] {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// markResolved writes the marker; one that exists already is fine.
func (s *Server) markResolved(ctx context.Context, g, id string) error {
	_, err := s.store.Write(ctx, resolvedMarkerName(g, id), []byte{}, gen(createOnly))
	if errors.Is(err, ErrPrecondition) {
		return nil
	}
	return err
}

// validNote allows an empty note and newlines, but no other control characters.
func validNote(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxReportNote {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", false
		}
	}
	return s, true
}

func (s *Server) loadReports(ctxr *http.Request, g string) ([]Report, error) {
	ctx := ctxr.Context()
	// The delimiter leaves out the resolved markers below reports/resolved/.
	l, err := s.store.List(ctx, g+"/reports/", "/", false)
	if err != nil {
		return nil, err
	}
	objs := l.Objects
	if len(objs) > maxReportsScanned {
		objs = objs[:maxReportsScanned]
	}
	out := make([]*Report, len(objs))
	var mu sync.Mutex
	var firstErr error
	parallel(8, objs, func(i int, o ObjectInfo) {
		data, _, err := s.store.Read(ctx, o.Name)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				mu.Lock()
				firstErr = err
				mu.Unlock()
			}
			return
		}
		var rp Report
		if json.Unmarshal(data, &rp) == nil && reportIDRe.MatchString(rp.ReportID) {
			out[i] = &rp
		}
	})
	if firstErr != nil {
		return nil, firstErr
	}
	var res []Report
	for _, r := range out {
		if r != nil {
			res = append(res, *r)
		}
	}
	return res, nil
}

func (s *Server) createReport(w http.ResponseWriter, r *http.Request, id Identity, g string, me Member) {
	var req struct {
		Target reportTarget `json:"target"`
		Reason string       `json:"reason"`
		Note   string       `json:"note"`
	}
	if !decode(w, r, &req) {
		return
	}
	note, okNote := validNote(req.Note)
	if !reportReasons[req.Reason] || !okNote {
		writeErr(w, http.StatusUnprocessableEntity, "invalid report")
		return
	}
	t := req.Target
	original := ""
	switch t.Type {
	case "item":
		ok := validEventID(t.Event)
		if !ok || !photoIDRe.MatchString(t.PhotoID) || t.UserID != "" {
			writeErr(w, http.StatusUnprocessableEntity, "invalid report")
			return
		}
		l, err := s.store.List(r.Context(), eventPrefix(g, t.Event)+"original/"+t.PhotoID+".", "", false)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		if len(l.Objects) == 0 {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		original = l.Objects[0].Name
		t = reportTarget{Type: "item", Event: t.Event, PhotoID: t.PhotoID}
	case "user":
		if t.UserID == "" || t.Event != "" || t.PhotoID != "" || t.UserID == me.UserID {
			writeErr(w, http.StatusUnprocessableEntity, "invalid report")
			return
		}
		ro, err := s.loadRoster(r.Context(), g)
		if err != nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		found := false
		for _, m := range ro.Members {
			// A member who left has no identity to act on; report their items instead.
			if m.UserID == t.UserID && m.Status != "left" {
				found = true
			}
		}
		if !found {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		t = reportTarget{Type: "user", UserID: t.UserID}
	default:
		writeErr(w, http.StatusUnprocessableEntity, "invalid report")
		return
	}

	existing, err := s.loadReports(r, g)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	open := 0
	for _, e := range existing {
		if e.Status != "open" || e.ReporterUserID != me.UserID {
			continue
		}
		// Sending exactly the same report again (a double tap, a retry) is a
		// no-op. The same target with another reason or note is a new report:
		// the member has something more to tell the administrator.
		if e.Target == t && e.Reason == req.Reason && e.Note == note {
			writeJSON(w, http.StatusOK, map[string]string{"report_id": e.ReportID})
			return
		}
		open++
	}
	if open >= maxOpenReportsPerReporter {
		writeErr(w, http.StatusTooManyRequests, "too many open reports")
		return
	}

	rp := Report{ReportID: newUUID(), ReporterUserID: me.UserID, Target: t, Reason: req.Reason, Note: note, CreatedAt: s.nowRFC3339(), Status: "open", Original: original}
	b, _ := json.Marshal(rp)
	if _, err := s.store.Write(r.Context(), reportName(g, rp.ReportID), b, gen(createOnly)); err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"report_id": rp.ReportID})
}

// listReports shows open reports, newest first, to administrators only. With
// ?include=resolved the resolved ones come too (an older app never asks, so it
// never shows a resolved report as open).
func (s *Server) listReports(w http.ResponseWriter, r *http.Request, id Identity, g string, ro Roster) {
	withResolved := r.URL.Query().Get("include") == "resolved"
	all, err := s.loadReports(r, g)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	nick := map[string]string{}
	for _, m := range ro.Members {
		nick[m.UserID] = m.Nickname
	}
	type reporter struct {
		UserID   string `json:"user_id"`
		Nickname string `json:"nickname"`
	}
	type entry struct {
		ReportID       string       `json:"report_id"`
		Reporter       reporter     `json:"reporter"`
		Target         reportTarget `json:"target"`
		TargetNickname string       `json:"target_nickname,omitempty"`
		Reason         string       `json:"reason"`
		Note           string       `json:"note"`
		CreatedAt      string       `json:"created_at"`
		Status         string       `json:"status"`
		ResolvedAt     string       `json:"resolved_at,omitempty"`
		Original       string       `json:"original,omitempty"`
	}
	out := []entry{}
	for _, rp := range all {
		if rp.Status != "open" && !withResolved {
			continue
		}
		e := entry{ReportID: rp.ReportID, Reporter: reporter{rp.ReporterUserID, nick[rp.ReporterUserID]}, Target: rp.Target, Reason: rp.Reason, Note: rp.Note, CreatedAt: rp.CreatedAt, Status: rp.Status, ResolvedAt: rp.ResolvedAt, Original: rp.Original}
		if rp.Target.Type == "user" {
			e.TargetNickname = nick[rp.Target.UserID]
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	writeJSON(w, http.StatusOK, map[string]any{"reports": out})
}

// resolveReport marks a report resolved. The file has a single writer (the
// backend) and is rewritten with a generation precondition.
func (s *Server) resolveReport(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Roster) {
	rid := r.PathValue("report_id")
	if !reportIDRe.MatchString(rid) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	for range 6 {
		data, generation, err := s.store.Read(r.Context(), reportName(g, rid))
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		var rp Report
		if err != nil || json.Unmarshal(data, &rp) != nil {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		if rp.Status == "resolved" {
			_ = s.markResolved(r.Context(), g, rid)
			writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
			return
		}
		rp.Status, rp.ResolvedAt = "resolved", s.nowRFC3339()
		b, _ := json.Marshal(rp)
		if _, err := s.store.Write(r.Context(), reportName(g, rid), b, gen(generation)); err != nil {
			if errors.Is(err, ErrPrecondition) {
				continue
			}
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		// The report file already says resolved; a missing marker is written
		// again the next time the full list is read.
		_ = s.markResolved(r.Context(), g, rid)
		writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
		return
	}
	writeErr(w, http.StatusConflict, "report kept changing")
}

// openReports tells a backend administrator which reports are open in every
// group of the backend: {"groups": {"<group>": ["<report_id>", ...]}}. It only
// lists objects (two listings per group), since the administrator's app asks
// about once a minute while it is in the foreground.
func (s *Server) openReports(w http.ResponseWriter, r *http.Request, id Identity) {
	if !s.isAdmin(id) {
		writeErr(w, http.StatusForbidden, "administrator only")
		return
	}
	top, err := s.store.List(r.Context(), "", "/", false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	var groups []string
	for _, p := range top.Prefixes {
		if g := strings.TrimSuffix(p, "/"); groupIDRe.MatchString(g) {
			groups = append(groups, g)
		}
	}
	out := map[string][]string{}
	var mu sync.Mutex
	var firstErr error
	parallel(8, groups, func(_ int, g string) {
		ids, err := s.openReportIDs(r.Context(), g)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			firstErr = err
			return
		}
		if len(ids) > 0 {
			out[g] = ids
		}
	})
	if firstErr != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	resp := map[string]any{"groups": out}
	// Piggybacks on this call rather than adding an endpoint: administrators
	// already poll it about once a minute while in the foreground, and no
	// member ever needs to know a newer backend image was published.
	if n := s.consumeUpdateNotice(); n != nil {
		resp["update_available"] = n
	}
	writeJSON(w, http.StatusOK, resp)
}
