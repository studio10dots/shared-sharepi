package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// publicMember is what clients may see of a member: never the email or sub.
type publicMember struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Status   string `json:"status"`
	Admin    bool   `json:"admin,omitempty"`
}

func publicMembers(ro Roster) []publicMember {
	out := make([]publicMember, 0, len(ro.Members))
	for _, m := range ro.Members {
		out = append(out, publicMember{m.UserID, m.Nickname, m.Status, m.Admin})
	}
	return out
}

func (s *Server) nowRFC3339() string { return s.now().UTC().Format(time.RFC3339) }

type createGroupReq struct {
	GroupID     string `json:"group_id"`
	DisplayName string `json:"display_name"`
	Nickname    string `json:"nickname"`
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request, id Identity) {
	if !s.isAdmin(id) {
		writeErr(w, http.StatusForbidden, "administrator only")
		return
	}
	var req createGroupReq
	if !decode(w, r, &req) {
		return
	}
	name, okName := validText(req.DisplayName, maxNameLen)
	nick, okNick := validText(req.Nickname, 30)
	if !groupIDRe.MatchString(req.GroupID) || !okName || !okNick {
		writeErr(w, http.StatusUnprocessableEntity, "invalid group")
		return
	}
	me := Member{UserID: newUUID(), Sub: id.Sub, Nickname: nick, Status: "active", JoinedAt: s.nowRFC3339(), Admin: true}
	ro := Roster{Version: 1, DisplayName: name, CreatedAt: s.nowRFC3339(), Members: []Member{me}}
	b, _ := json.Marshal(ro)
	// Create-if-absent: two people picking the same id cannot both succeed.
	if _, err := s.store.Write(r.Context(), rosterName(req.GroupID), b, gen(createOnly)); err != nil {
		if errors.Is(err, ErrPrecondition) {
			writeErr(w, http.StatusConflict, "group exists")
			return
		}
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	s.dropCachedRoster(req.GroupID)
	writeJSON(w, http.StatusCreated, map[string]any{"group_id": req.GroupID, "display_name": name, "user_id": me.UserID})
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request, id Identity, g string, ro Roster, me *Member) {
	out := map[string]any{
		"group_id": g, "display_name": ro.DisplayName, "members": publicMembers(ro),
		"created_at": ro.CreatedAt, "sharing": ro.sharingState(),
	}
	if me != nil {
		out["me"] = map[string]string{"user_id": me.UserID}
	}
	// An administrator opening a group learns its open reports with the same
	// answer, so the app need not wait for its next once-a-minute check.
	if s.isAdmin(id) {
		if ids, err := s.openReportIDs(r.Context(), g); err == nil {
			out["open_reports"] = ids
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) renameGroup(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Roster) {
	var req struct {
		DisplayName string `json:"display_name"`
	}
	if !decode(w, r, &req) {
		return
	}
	name, ok := validText(req.DisplayName, maxNameLen)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "invalid name")
		return
	}
	if _, err := s.updateRoster(r.Context(), g, func(ro *Roster) error { ro.DisplayName = name; return nil }); err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"display_name": name})
}

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request, id Identity, g string, me Member) {
	var req struct {
		Nickname string `json:"nickname"`
	}
	if !decode(w, r, &req) {
		return
	}
	nick, ok := validText(req.Nickname, 30)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "invalid nickname")
		return
	}
	_, err := s.updateRoster(r.Context(), g, func(ro *Roster) error {
		for i := range ro.Members {
			if ro.Members[i].UserID == me.UserID && ro.Members[i].Status == "active" {
				ro.Members[i].Nickname = nick
				return nil
			}
		}
		return ErrNotFound
	})
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"nickname": nick})
}

// removeMember keeps the row with status "removed" so old posts still resolve to a nickname.
func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Roster) {
	target := r.PathValue("user_id")
	_, err := s.updateRoster(r.Context(), g, func(ro *Roster) error {
		for i := range ro.Members {
			if ro.Members[i].UserID == target {
				// A member who already left has no identity left to remove; keep it that way.
				// A suspended member can be removed too: the administrator trims a
				// suspended group's members to bring it back within a smaller size.
				if st := ro.Members[i].Status; st == "active" || st == "suspended" {
					ro.Members[i].Status = "removed"
				}
				return nil
			}
		}
		return ErrNotFound
	})
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// leaveGroup erases the caller's identity from the roster. The row stays
// with only user_id and joined_at, so their photos remain attributed to a "left
// member"; joining again later creates a new row with a new user_id. A second call
// gets 404, like any non-member: the row no longer carries who the caller was.
func (s *Server) leaveGroup(w http.ResponseWriter, r *http.Request, id Identity, g string, me Member) {
	_, err := s.updateRoster(r.Context(), g, func(ro *Roster) error {
		for i := range ro.Members {
			if ro.Members[i].UserID == me.UserID && ro.Members[i].Status == "active" {
				m := &ro.Members[i]
				m.Status, m.Sub, m.Email, m.Nickname = "left", "", "", ""
				return nil
			}
		}
		return ErrNotFound
	})
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	// A member who leaves takes their personal favorites with them (best
	// effort: the file names no one but is theirs).
	_ = s.store.Delete(r.Context(), favName(g, me.UserID))
	w.WriteHeader(http.StatusNoContent)
}

const maxGroupsScanned = 200

// myGroups restores a device's group list: every group where the caller is an
// active member. It cannot join anything new (a stranger gets an empty list).
func (s *Server) myGroups(w http.ResponseWriter, r *http.Request, id Identity) {
	l, err := s.store.List(r.Context(), "", "/", false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	var ids []string
	for _, p := range l.Prefixes {
		g := strings.TrimSuffix(p, "/")
		if groupIDRe.MatchString(g) {
			ids = append(ids, g)
		}
	}
	sort.Strings(ids)
	if len(ids) > maxGroupsScanned {
		ids = ids[:maxGroupsScanned]
	}
	type entry struct {
		GroupID     string `json:"group_id"`
		DisplayName string `json:"display_name"`
		UserID      string `json:"user_id"`
		Nickname    string `json:"nickname"`
		CreatedAt   string `json:"created_at"`
		Sharing     string `json:"sharing"`
	}
	found := make([]*entry, len(ids))
	parallel(8, ids, func(i int, g string) {
		ro, err := s.loadRoster(r.Context(), g)
		if err != nil {
			return
		}
		if m, _, ok := ro.activeID(id); ok {
			found[i] = &entry{g, ro.DisplayName, m.UserID, m.Nickname, ro.CreatedAt, ro.sharingState()}
		}
	})
	out := []*entry{}
	for _, e := range found {
		if e != nil {
			out = append(out, e)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}
