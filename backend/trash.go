package main

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The trash is the bucket's own soft delete: a deleted object stays recoverable
// for the bucket's retention window. These endpoints only expose media objects
// of the caller's own group, so the roster, invitations and reports can never
// be listed or restored through them.

const (
	maxTrashListed    = 2000
	maxRestoreObjects = 50
)

// trashEvent says which event a deleted item goes back to. Restore is set only
// when the event itself is gone (its event.json was deleted with it): restoring
// the item then restores that file too, so the event reappears with it.
type trashEvent struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Date    string       `json:"date"`
	Restore *trashObject `json:"restore,omitempty"`
}

type trashObject struct {
	Path string `json:"path"`
	// Generation is the object's Store.Version, opaque to clients: a decimal
	// generation number on GCS. It is a string because a GCS
	// generation is a 64-bit integer, not safe in every JSON client.
	Generation string `json:"generation"`
	DeletedAt  string `json:"deleted_at,omitempty"`
}

func (s *Server) listTrash(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	deleted, err := s.store.ListDeleted(r.Context(), g+"/")
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	out := []trashObject{}
	eventIDs := map[string]bool{}
	newestEventFile := map[string]DeletedObject{}
	for _, d := range deleted {
		parts := strings.Split(d.Name, "/")
		if len(parts) == 3 && parts[2] == "event.json" && validEventID(parts[1]) {
			if cur, ok := newestEventFile[parts[1]]; !ok || d.DeletedAt.After(cur.DeletedAt) {
				newestEventFile[parts[1]] = d
			}
			continue
		}
		if !mediaRe.MatchString(d.Name) {
			continue
		}
		o := trashObject{Path: d.Name, Generation: string(d.Version)}
		if !d.DeletedAt.IsZero() {
			o.DeletedAt = d.DeletedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, o)
		eventIDs[parts[1]] = true
		if len(out) >= maxTrashListed {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt > out[j].DeletedAt })
	events := []trashEvent{}
	for id := range eventIDs {
		e := trashEvent{ID: id}
		if b, _, err := s.store.Read(r.Context(), eventFilePath(g, id)); err == nil {
			if f, ok := parseEventFile(b); ok {
				e.Name, e.Date = f.Name, f.Date
			}
		} else if d, ok := newestEventFile[id]; ok {
			if b, err := s.store.ReadDeleted(r.Context(), d.Name, d.Version); err == nil {
				if f, ok := parseEventFile(b); ok {
					e.Name, e.Date = f.Name, f.Date
					e.Restore = &trashObject{Path: d.Name, Generation: string(d.Version)}
				}
			}
		}
		events = append(events, e)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"objects": out, "events": events})
}

// trashThumbnail returns the bytes of one deleted item's thumbnail (a 100 px
// JPEG, a few KB): a soft-deleted object cannot be served through a signed
// URL, so this is the one place photo bytes pass through the backend.
func (s *Server) trashThumbnail(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	path := r.URL.Query().Get("path")
	generation := r.URL.Query().Get("generation")
	if generation == "" || !strings.HasPrefix(path, g+"/") || !mediaRe.MatchString(path) || !strings.Contains(path, "/thumbnail/") {
		writeErr(w, http.StatusUnprocessableEntity, "invalid object")
		return
	}
	b, err := s.store.ReadDeleted(r.Context(), path, Version(generation))
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	// Image bytes with a fixed image type and nosniff, never HTML: a browser will
	// not run them (gosec G705 cannot see that).
	_, _ = w.Write(b) // #nosec G705
}

func isEventFile(g, p string) bool {
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == g && parts[2] == "event.json" && validEventID(parts[1])
}

// restoreTrash restores the given versions, derivatives first and the original
// last, so an item only reappears in listings once its thumbnail is back (the
// same order as an upload).
func (s *Server) restoreTrash(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	var req struct {
		Objects []trashObject `json:"objects"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Objects) == 0 || len(req.Objects) > maxRestoreObjects {
		writeErr(w, http.StatusUnprocessableEntity, "bad request")
		return
	}
	type job struct {
		path    string
		version Version
	}
	jobs := make([]job, 0, len(req.Objects))
	for _, o := range req.Objects {
		if o.Generation == "" || !strings.HasPrefix(o.Path, g+"/") || !(mediaRe.MatchString(o.Path) || isEventFile(g, o.Path)) {
			writeErr(w, http.StatusUnprocessableEntity, "invalid object")
			return
		}
		jobs = append(jobs, job{o.Path, Version(o.Generation)})
	}
	rank := func(p string) int {
		if strings.Contains(p, "/original/") || strings.HasSuffix(p, "/event.json") {
			return 1
		}
		return 0
	}
	sort.SliceStable(jobs, func(i, j int) bool { return rank(jobs[i].path) < rank(jobs[j].path) })
	restored := 0
	for _, j := range jobs {
		err := s.store.Restore(r.Context(), j.path, j.version)
		switch {
		case err == nil:
			restored++
		case errors.Is(err, ErrPrecondition): // a live object is already there: nothing to do
		case errors.Is(err, ErrNotFound): // past the retention window
			writeErr(w, http.StatusNotFound, "not found")
			return
		default:
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"restored": restored})
}
