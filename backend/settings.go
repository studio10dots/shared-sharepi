package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// The owner's settings for this backend, kept as one small object in the
// bucket (docs/BACKEND_DESIGN.md section 17). Today they are the limits on
// videos: the app asks for them before it uploads a video and offers the
// user a choice (or refuses) when the video is over them.
const settingsObject = "_backend/settings.json"

// settingsTTL is how long a read of the settings is reused. A change made by
// the owner is seen at once by this instance (the cache is dropped on write),
// and by another instance within this time.
const settingsTTL = 30 * time.Second

// VideoSettings are limits on videos. Zero means "no limit" for every field.
//
//   - MaxSeconds / MaxBytes: a longer or larger video is not uploaded. The
//     backend enforces MaxBytes itself (it is part of the signed upload URL);
//     it cannot see a video's length, so MaxSeconds is the app's check, like
//     the plan limits (Agents.md section 20).
//   - ConfirmSeconds / ConfirmBytes: over these the app asks the user whether
//     to upload the video as it is or compressed.
type VideoSettings struct {
	MaxSeconds     int   `json:"max_seconds"`
	MaxBytes       int64 `json:"max_bytes"`
	ConfirmSeconds int   `json:"confirm_seconds"`
	ConfirmBytes   int64 `json:"confirm_bytes"`
}

type Settings struct {
	Version int           `json:"version"`
	Video   VideoSettings `json:"video"`
}

// defaultSettings is what a backend that was never configured uses: at most 5
// minutes, and a question for anything over 2 (what the app always did).
func defaultSettings() Settings {
	return Settings{Version: 1, Video: VideoSettings{MaxSeconds: 5 * 60, ConfirmSeconds: 2 * 60}}
}

const maxVideoSeconds = 24 * 60 * 60

// valid says why v cannot be saved, or "" when it can.
func (v VideoSettings) valid(maxUpload int64) string {
	switch {
	case v.MaxSeconds < 0 || v.MaxSeconds > maxVideoSeconds ||
		v.ConfirmSeconds < 0 || v.ConfirmSeconds > maxVideoSeconds:
		return "seconds out of range"
	case v.MaxBytes < 0 || v.ConfirmBytes < 0 ||
		(maxUpload > 0 && (v.MaxBytes > maxUpload || v.ConfirmBytes > maxUpload)):
		return "bytes out of range"
	case v.MaxSeconds > 0 && v.ConfirmSeconds > v.MaxSeconds:
		return "confirm_seconds is above max_seconds"
	case v.MaxBytes > 0 && v.ConfirmBytes > v.MaxBytes:
		return "confirm_bytes is above max_bytes"
	}
	return ""
}

type cachedSettings struct {
	s  Settings
	at time.Time
}

// loadSettings returns the owner's settings, or the defaults when none were
// ever saved. An unreadable object is an error: silently falling back to the
// defaults would lift a limit the owner set.
func (s *Server) loadSettings(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	c := s.settings
	s.mu.Unlock()
	if c != nil && s.now().Sub(c.at) < settingsTTL {
		return c.s, nil
	}
	out := defaultSettings()
	b, _, err := s.store.Read(ctx, settingsObject)
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return Settings{}, err
	default:
		if err := json.Unmarshal(b, &out); err != nil {
			return Settings{}, err
		}
	}
	s.mu.Lock()
	s.settings = &cachedSettings{out, s.now()}
	s.mu.Unlock()
	return out, nil
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request, id Identity) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// putSettings replaces the settings. Only the backend's administrators may. The
// object is the single writer's (this endpoint's), written with a generation
// precondition like every shared object (Agents.md section 7).
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request, id Identity) {
	if !s.isAdmin(id) {
		writeErr(w, http.StatusForbidden, "administrator only")
		return
	}
	var req struct {
		Video VideoSettings `json:"video"`
	}
	if !decode(w, r, &req) {
		return
	}
	if msg := req.Video.valid(s.cfg.MaxUploadSize); msg != "" {
		writeErr(w, http.StatusUnprocessableEntity, msg)
		return
	}
	next := Settings{Version: 1, Video: req.Video}
	b, _ := json.Marshal(next)
	defer func() {
		s.mu.Lock()
		s.settings = nil
		s.mu.Unlock()
	}()
	for range 6 {
		cond := createOnly
		if _, v, err := s.store.Read(r.Context(), settingsObject); err == nil {
			cond = v
		} else if !errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		_, err := s.store.Write(r.Context(), settingsObject, b, gen(cond))
		if errors.Is(err, ErrPrecondition) {
			continue
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		writeJSON(w, http.StatusOK, next)
		return
	}
	writeErr(w, http.StatusConflict, "settings update kept conflicting")
}

// isVideoExt reports whether an original with this extension is a video.
func isVideoExt(ext string) bool {
	return strings.HasPrefix(contentTypeFor(ext), "video/")
}

// uploadLimit is the size limit signed into an original's upload URL: the
// backend's own, or the owner's video limit when that is lower.
func (s *Server) uploadLimit(ctx context.Context, ext string) (int64, error) {
	limit := s.cfg.MaxUploadSize
	if !isVideoExt(ext) {
		return limit, nil
	}
	st, err := s.loadSettings(ctx)
	if err != nil {
		return 0, err
	}
	if m := st.Video.MaxBytes; m > 0 && (limit == 0 || m < limit) {
		limit = m
	}
	return limit, nil
}
