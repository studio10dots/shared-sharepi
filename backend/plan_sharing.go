package main

import (
	"net/http"
	"slices"
)

// stopSharing suspends every active member except the caller's own row and marks
// the group suspended (docs/BACKEND_DESIGN.md section 14). Rows and user_ids are
// kept, so restoring needs no member action. The administrator's app calls this
// when a plan lapsed; the backend knows nothing about plans. Idempotent.
func (s *Server) stopSharing(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Roster) {
	_, err := s.updateRoster(r.Context(), g, func(ro *Roster) error {
		ro.Sharing = "suspended"
		for i := range ro.Members {
			m := &ro.Members[i]
			if m.Status == "active" && m.Sub != id.Sub {
				m.Status = "suspended"
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// restoreSharing makes suspended members active again: all of them, or only the
// listed user_ids. The group stops being suspended once none is left suspended.
// The app decides what the plan allows. Idempotent.
func (s *Server) restoreSharing(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Roster) {
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	_, err := s.updateRoster(r.Context(), g, func(ro *Roster) error {
		left := false
		for i := range ro.Members {
			m := &ro.Members[i]
			if m.Status != "suspended" {
				continue
			}
			if len(req.UserIDs) == 0 || slices.Contains(req.UserIDs, m.UserID) {
				m.Status = "active"
			} else {
				left = true
			}
		}
		if !left {
			ro.Sharing = ""
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
