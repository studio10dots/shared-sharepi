package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// event is a day plus an id. The id is the object-path segment (random, chosen
// by the app), so the name and date can change without moving any object; the
// name and date live in {id}/event.json.
type event struct {
	Date string `json:"date"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type eventFile struct {
	Name string `json:"name"`
	Date string `json:"date"`
}

// eventIDRe is the only shape an event id may have. Event folders sit in the
// same namespace as the group's reserved folders "invites/", "reports/" and
// "members/" (all 7 characters), so the 8-character floor keeps them apart: an
// event id can never be one of those.
var eventIDRe = regexp.MustCompile(`^[a-z0-9]{8,32}$`)

// validEventID also names the reserved folders explicitly, for clarity.
func validEventID(id string) bool {
	return eventIDRe.MatchString(id) && id != "invites" && id != "reports" && id != "members" && id != "shares"
}

// validEventTitle is the display name: any text, not empty, not too long.
func validEventTitle(n string) (string, bool) { return validText(n, maxEventNameLen) }

// validEventDate accepts YYYY/MM/DD naming a real calendar day.
func validEventDate(d string) bool {
	if !dateRe.MatchString(d) {
		return false
	}
	_, err := time.Parse("2006/01/02", d)
	return err == nil
}

func eventFilePath(g, id string) string { return eventPrefix(g, id) + "event.json" }

// parseEventFile returns a valid event file, or false: an event exists only if
// its event.json is readable and holds a valid name and date.
func parseEventFile(b []byte) (eventFile, bool) {
	var f eventFile
	if json.Unmarshal(b, &f) != nil {
		return f, false
	}
	t, ok := validEventTitle(f.Name)
	if !ok || !validEventDate(f.Date) {
		return f, false
	}
	f.Name = t
	return f, true
}

func marshalEventFile(name, date string) []byte {
	b, _ := json.Marshal(eventFile{Name: name, Date: date})
	return b
}

// listEvents lists {group}/ once with a "/" delimiter, so the cost follows the
// number of events, not photos, and reads each event.json. A folder without a
// valid one (an orphaned derivative, an old layout) is not an event.
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	ctx := r.Context()
	top, err := s.store.List(ctx, g+"/", "/", false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	var ids []string
	for _, p := range top.Prefixes {
		if b := path.Base(strings.TrimSuffix(p, "/")); validEventID(b) {
			ids = append(ids, b)
		}
	}
	found := make([]*event, len(ids))
	var mu sync.Mutex
	var firstErr error
	parallel(8, ids, func(i int, eid string) {
		b, _, err := s.store.Read(ctx, eventFilePath(g, eid))
		if errors.Is(err, ErrNotFound) {
			return
		}
		if err != nil {
			mu.Lock()
			firstErr = err
			mu.Unlock()
			return
		}
		if f, ok := parseEventFile(b); ok {
			found[i] = &event{Date: f.Date, ID: eid, Name: f.Name}
		}
	})
	if firstErr != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	out := make([]event, 0, len(ids))
	for _, e := range found {
		if e != nil {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

func (s *Server) createEvent(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	var req event
	if !decode(w, r, &req) {
		return
	}
	title, ok := validEventTitle(req.Name)
	if !validEventDate(req.Date) || !validEventID(req.ID) || !ok {
		writeErr(w, http.StatusUnprocessableEntity, "invalid event")
		return
	}
	// Create-only: creating it twice (a retry) keeps the first content, which is
	// what the response then reports.
	_, err := s.store.Write(r.Context(), eventFilePath(g, req.ID), marshalEventFile(title, req.Date), gen(createOnly))
	if errors.Is(err, ErrPrecondition) {
		b, _, rerr := s.store.Read(r.Context(), eventFilePath(g, req.ID))
		if f, ok := parseEventFile(b); rerr == nil && ok {
			writeJSON(w, http.StatusOK, event{Date: f.Date, ID: req.ID, Name: f.Name})
			return
		}
		// The existing file is unreadable or invalid, so it is not an event and
		// there is nothing to keep.
		writeErr(w, http.StatusConflict, "event file invalid")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, event{Date: req.Date, ID: req.ID, Name: title})
}

// updateEvent changes an event's name and/or date for everyone. Any member may:
// like deleting a photo, it is not an administrator's power. event.json is a
// small shared object, so it is read with its generation and written with a
// precondition; a concurrent name edit and date edit therefore both survive.
func (s *Server) updateEvent(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	eid, ok := eventFromPath(r)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "invalid event")
		return
	}
	var req struct {
		Name *string `json:"name"`
		Date *string `json:"date"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Name == nil && req.Date == nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid event")
		return
	}
	var title string
	if req.Name != nil {
		if title, ok = validEventTitle(*req.Name); !ok {
			writeErr(w, http.StatusUnprocessableEntity, "invalid name")
			return
		}
	}
	if req.Date != nil && !validEventDate(*req.Date) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid date")
		return
	}
	name := eventFilePath(g, eid)
	for range 6 {
		b, generation, err := s.store.Read(r.Context(), name)
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		f, valid := parseEventFile(b)
		if !valid {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if req.Name != nil {
			f.Name = title
		}
		if req.Date != nil {
			f.Date = *req.Date
		}
		_, err = s.store.Write(r.Context(), name, marshalEventFile(f.Name, f.Date), gen(generation))
		if errors.Is(err, ErrPrecondition) {
			continue
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		writeJSON(w, http.StatusOK, event{Date: f.Date, ID: eid, Name: f.Name})
		return
	}
	writeErr(w, http.StatusConflict, "event update kept conflicting")
}

// eventFromPath validates the {id} path segment.
func eventFromPath(r *http.Request) (id string, ok bool) {
	id = r.PathValue("id")
	return id, validEventID(id)
}

// deleteEvent removes every object under the event. The bucket's soft delete keeps
// them recoverable for the retention window.
func (s *Server) deleteEvent(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	eid, ok := eventFromPath(r)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "invalid event")
		return
	}
	l, err := s.store.List(r.Context(), eventPrefix(g, eid), "", false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	var failed bool
	var mu sync.Mutex
	parallel(8, l.Objects, func(_ int, o ObjectInfo) {
		if err := s.store.Delete(r.Context(), o.Name); err != nil {
			mu.Lock()
			failed = true
			mu.Unlock()
		}
	})
	if failed {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": len(l.Objects)})
}

type item struct {
	PhotoID   string `json:"photo_id"`
	MediaType string `json:"media_type"` // image | video
	Ext       string `json:"ext"`
	Size      int64  `json:"size"`
	Uploaded  string `json:"uploaded_at"`
	// TakenAt is when the picture was taken (the EXIF time or the file's
	// creation time, as the app reported it at upload), falling back to the
	// upload time for an object that carries none.
	TakenAt    string `json:"taken_at"`
	Uploader   string `json:"uploader,omitempty"`
	Compressed bool   `json:"compressed,omitempty"`
}

const timeLayout = "2006-01-02T15:04:05Z"

// parseTakenAt accepts the capture time an app sends: RFC 3339, not before 1990
// and not more than a day ahead. Anything else is ignored rather than refused,
// because it only orders the grid.
func parseTakenAt(v string, now time.Time) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil || t.Year() < 1990 || t.After(now.Add(24*time.Hour)) {
		return time.Time{}, false
	}
	return t.UTC(), true
}

var videoExts = map[string]bool{"mp4": true, "mov": true, "m4v": true, "3gp": true, "webm": true, "mkv": true}

const maxItems = 5000

// listItems lists the event's originals: an item exists once its original does.
func (s *Server) listItems(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	eid, ok := eventFromPath(r)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "invalid event")
		return
	}
	pre := eventPrefix(g, eid) + "original/"
	l, err := s.store.List(r.Context(), pre, "", true)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	now := s.now()
	taken := func(o ObjectInfo) time.Time {
		if t, ok := parseTakenAt(o.Metadata["taken-at"], now); ok {
			return t
		}
		return o.Updated
	}
	sort.SliceStable(l.Objects, func(i, j int) bool { return taken(l.Objects[i]).Before(taken(l.Objects[j])) })
	items := []item{}
	for _, o := range l.Objects {
		file := strings.TrimPrefix(o.Name, pre)
		base, ext, found := strings.Cut(file, ".")
		if !found || !photoIDRe.MatchString(base) || !extRe.MatchString(strings.ToLower(ext)) {
			continue
		}
		ext = strings.ToLower(ext)
		mt := "image"
		if videoExts[ext] {
			mt = "video"
		}
		items = append(items, item{PhotoID: base, MediaType: mt, Ext: ext, Size: o.Size, Uploaded: o.Updated.UTC().Format(timeLayout), TakenAt: taken(o).UTC().Format(timeLayout), Uploader: o.Metadata["uploader"], Compressed: o.Metadata["compressed"] == "true"})
		if len(items) >= maxItems {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// deleteItem soft-deletes an item's original, medium and thumbnail.
func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	eid, ok := eventFromPath(r)
	pid := r.PathValue("photo_id")
	if !ok || !photoIDRe.MatchString(pid) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid item")
		return
	}
	pre := eventPrefix(g, eid)
	orig, err := s.store.List(r.Context(), pre+"original/"+pid+".", "", false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	if len(orig.Objects) == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	names := []string{pre + "medium/" + pid + ".jpg", pre + "thumbnail/" + pid + ".jpg"}
	for _, o := range orig.Objects {
		names = append(names, o.Name)
	}
	// The original goes last so a half-deleted item disappears from listings only
	// once its derivatives are gone (the mirror of the upload order).
	for _, n := range names {
		if err := s.store.Delete(r.Context(), n); err != nil {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- signed URLs

type countEntry struct {
	n  int
	at time.Time
}

const countTTL = 30 * time.Second

// eventIsFull reports whether the event already holds MaxEventItems items (its
// originals). The count is remembered for a short while so a bulk upload does
// not list the event for every file; while it is far from the limit that is
// safe, and near the limit every call counts afresh.
func (s *Server) eventIsFull(ctx context.Context, g, id string) (bool, error) {
	limit := s.cfg.MaxEventItems
	if limit <= 0 {
		return false, nil
	}
	key := g + "|" + id
	margin := limit / 50
	s.mu.Lock()
	c, ok := s.counts[key]
	s.mu.Unlock()
	if ok && s.now().Sub(c.at) < countTTL && c.n < limit-margin {
		return false, nil
	}
	l, err := s.store.List(ctx, eventPrefix(g, id)+"original/", "", false)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.counts == nil {
		s.counts = map[string]countEntry{}
	}
	if len(s.counts) > 1000 {
		s.counts = map[string]countEntry{}
	}
	s.counts[key] = countEntry{n: len(l.Objects), at: s.now()}
	s.mu.Unlock()
	return len(l.Objects) >= limit, nil
}

type uploadReq struct {
	EventID string `json:"event_id"`
	// EventDate and EventName are optional and only used together: when both are
	// valid they create the event's event.json if it has none, so an upload to an
	// event whose creation call never got through still shows under its name and
	// date.
	EventDate string   `json:"event_date"`
	EventName string   `json:"event_name"`
	PhotoID   string   `json:"photo_id"`
	Ext       string   `json:"ext"`
	Kinds     []string `json:"kinds"`
	// TakenAt is optional (RFC 3339) and only recorded on the original.
	TakenAt string `json:"taken_at"`
	// Compressed marks the original as the app's on-device re-encode; it is
	// signed into the original's metadata so it cannot be mislabelled later.
	Compressed bool `json:"compressed"`
}

// maxUploadKinds is how many upload URLs one request may ask for: an item has
// an original, a medium image and a thumbnail.
const maxUploadKinds = 3

// heldByAnother reports whether an object of this item and kind already exists
// that somebody other than userID uploaded. An object without an uploader (one
// that predates the field) counts as someone else's: nobody may silently
// replace what the backend cannot attribute.
func (s *Server) heldByAnother(ctx context.Context, g, eventID, kind, photoID, userID string) (bool, error) {
	l, err := s.store.List(ctx, eventPrefix(g, eventID)+kind+"/"+photoID+".", "", true)
	if err != nil {
		return false, err
	}
	for _, o := range l.Objects {
		if o.Metadata["uploader"] != userID {
			return true, nil
		}
	}
	return false, nil
}

type signedURL struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (s *Server) uploads(w http.ResponseWriter, r *http.Request, id Identity, g string, me Member) {
	var req uploadReq
	if !decode(w, r, &req) {
		return
	}
	// At most one URL per kind: each one is a signing call to the IAM Credentials
	// API, whose quota every group on this backend shares.
	if !photoIDRe.MatchString(req.PhotoID) || !extRe.MatchString(req.Ext) ||
		!validEventID(req.EventID) || len(req.Kinds) == 0 || len(req.Kinds) > maxUploadKinds {
		writeErr(w, http.StatusUnprocessableEntity, "invalid item")
		return
	}
	if full, err := s.eventIsFull(r.Context(), g, req.EventID); err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	} else if full {
		// 409 with a code the app turns into "create a new event".
		writeErr(w, http.StatusConflict, "event_full")
		return
	}
	if t, ok := validEventTitle(req.EventName); ok && validEventDate(req.EventDate) {
		_, _ = s.store.Write(r.Context(), eventFilePath(g, req.EventID), marshalEventFile(t, req.EventDate), gen(createOnly))
	}
	out := map[string]signedURL{}
	for _, kind := range req.Kinds {
		ext, ctype := "jpg", "image/jpeg"
		switch kind {
		case "original":
			ext, ctype = req.Ext, contentTypeFor(req.Ext)
		case "medium", "thumbnail":
		default:
			writeErr(w, http.StatusUnprocessableEntity, "invalid kind")
			return
		}
		if _, done := out[kind]; done {
			continue // the same kind twice: one URL is enough
		}
		// An upload replaces whatever is at the path, so refuse to sign one over an
		// object another member (or an unknown uploader) put there: a retry of
		// one's own upload is the only overwrite that is legitimate.
		if held, err := s.heldByAnother(r.Context(), g, req.EventID, kind, req.PhotoID, me.UserID); err != nil {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		} else if held {
			writeErr(w, http.StatusConflict, "item_exists")
			return
		}
		obj := eventPrefix(g, req.EventID) + kind + "/" + req.PhotoID + "." + ext
		// The uploader is part of the signature, so it cannot be forged.
		meta := map[string]string{"uploader": me.UserID}
		if t, ok := parseTakenAt(req.TakenAt, s.now()); ok && kind == "original" {
			meta["taken-at"] = t.Format(timeLayout)
		}
		if kind == "original" && req.Compressed {
			meta["compressed"] = "true"
		}
		maxSize := s.derivativeLimit(kind)
		if kind == "original" {
			var err error
			if maxSize, err = s.uploadLimit(r.Context(), req.Ext); err != nil {
				writeErr(w, http.StatusBadGateway, "storage error")
				return
			}
		}
		u, hdr, err := s.signer.SignURL(r.Context(), SignRequest{
			Object: obj, Method: "PUT", Expires: s.cfg.UploadTTL,
			ContentType: ctype, Metadata: meta, MaxSize: maxSize,
		})
		if err != nil {
			writeErr(w, http.StatusBadGateway, "sign failed")
			return
		}
		out[kind] = signedURL{URL: u, Headers: hdr}
	}
	writeJSON(w, http.StatusOK, map[string]any{"urls": out})
}

// contentTypeFor is what an original is stored as, so a browser or a player that
// opens the object directly knows what it is. It is part of the signature, so
// the client has to send exactly this.
func contentTypeFor(ext string) string {
	switch strings.ToLower(ext) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "heic", "heif":
		return "image/heic"
	case "mp4", "m4v":
		return "video/mp4"
	case "mov":
		return "video/quicktime"
	case "webm":
		return "video/webm"
	case "3gp":
		return "video/3gpp"
	case "mkv":
		return "video/x-matroska"
	case "avi":
		return "video/x-msvideo"
	}
	return "application/octet-stream"
}

const maxBatch = 200

func (s *Server) downloads(w http.ResponseWriter, r *http.Request, id Identity, g string, me Member) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Paths) == 0 || len(req.Paths) > maxBatch {
		writeErr(w, http.StatusUnprocessableEntity, "bad request")
		return
	}
	for _, p := range req.Paths {
		// Every path must be a media object inside the caller's own group.
		if !strings.HasPrefix(p, g+"/") || !mediaRe.MatchString(p) {
			writeErr(w, http.StatusUnprocessableEntity, "invalid path")
			return
		}
	}
	urls := make([]string, len(req.Paths))
	errs := make([]error, len(req.Paths))
	parallel(32, req.Paths, func(i int, p string) {
		urls[i], _, errs[i] = s.signer.SignURL(r.Context(), SignRequest{Object: p, Method: "GET", Expires: s.cfg.DownloadTTL})
	})
	res := map[string]string{}
	for i, p := range req.Paths {
		if errs[i] != nil {
			writeErr(w, http.StatusBadGateway, "sign failed")
			return
		}
		res[p] = urls[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{"urls": res})
}
