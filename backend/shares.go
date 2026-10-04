package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// Share links let a member hand a few photos or videos to someone outside the
// group without granting group membership (docs/BACKEND_DESIGN.md section
// 16). The backend signs each item's original (and, for the app's own quick
// preview, its medium) exactly as it already does for a member's downloads,
// and wraps them in a small manifest object so the deep link itself stays
// short regardless of how many items it carries. The manifest's own signed
// URL *is* the access grant: whoever holds it can read those objects until it
// expires, independent of group membership. No new authorization mechanism is
// introduced; this is the existing signed-URL model with one extra layer of
// indirection.
//
// "shares" is a reserved folder name in the group's namespace, like
// "invites", "reports" and "members" (see validEventID): 6 characters, below
// the 8-character floor of an event id, so it can never collide with one.

const (
	maxShareItems       = 30
	shareManifestSuffix = ".share.json"
)

type shareItemReq struct {
	EventID string `json:"event_id"`
	PhotoID string `json:"photo_id"`
	Ext     string `json:"ext"`
}

type shareManifestItem struct {
	Path        string `json:"path"`
	MediaType   string `json:"media_type"` // image | video
	OriginalURL string `json:"original_url"`
	MediumURL   string `json:"medium_url"`
}

type shareManifest struct {
	Version   int                 `json:"version"`
	CreatedAt string              `json:"created_at"`
	ExpiresAt string              `json:"expires_at"`
	Items     []shareManifestItem `json:"items"`
}

func shareManifestPath(g, shareID string) string {
	return g + "/shares/" + shareID + shareManifestSuffix
}

// createShare signs the chosen items' original and medium objects, writes
// them into a manifest object and returns a single signed URL to that
// manifest. The manifest's expiry, not group membership, is what limits who
// can still see the photos: the same trade-off invitations already make
// (docs/BACKEND_DESIGN.md, "Invitations").
func (s *Server) createShare(w http.ResponseWriter, r *http.Request, id Identity, g string, _ Member) {
	var req struct {
		Items      []shareItemReq `json:"items"`
		TTLMinutes int            `json:"ttl_minutes"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.TTLMinutes == 0 {
		req.TTLMinutes = 10
	}
	if req.TTLMinutes != 5 && req.TTLMinutes != 10 && req.TTLMinutes != 15 {
		writeErr(w, http.StatusUnprocessableEntity, "ttl_minutes must be 5, 10 or 15")
		return
	}
	if len(req.Items) == 0 || len(req.Items) > maxShareItems {
		writeErr(w, http.StatusUnprocessableEntity, "invalid items")
		return
	}
	for _, it := range req.Items {
		if !validEventID(it.EventID) || !photoIDRe.MatchString(it.PhotoID) || !extRe.MatchString(it.Ext) {
			writeErr(w, http.StatusUnprocessableEntity, "invalid items")
			return
		}
	}

	ttl := time.Duration(req.TTLMinutes) * time.Minute
	items := make([]shareManifestItem, len(req.Items))
	errs := make([]error, len(req.Items))
	parallel(8, req.Items, func(i int, it shareItemReq) {
		pre := eventPrefix(g, it.EventID)
		original := pre + "original/" + it.PhotoID + "." + it.Ext
		medium := pre + "medium/" + it.PhotoID + ".jpg"
		originalURL, _, err := s.signer.SignURL(r.Context(), SignRequest{Object: original, Method: "GET", Expires: ttl})
		if err != nil {
			errs[i] = err
			return
		}
		mediumURL, _, err := s.signer.SignURL(r.Context(), SignRequest{Object: medium, Method: "GET", Expires: ttl})
		if err != nil {
			errs[i] = err
			return
		}
		mt := "image"
		if videoExts[it.Ext] {
			mt = "video"
		}
		items[i] = shareManifestItem{Path: original, MediaType: mt, OriginalURL: originalURL, MediumURL: mediumURL}
	})
	for _, err := range errs {
		if err != nil {
			writeErr(w, http.StatusBadGateway, "sign failed")
			return
		}
	}

	now := s.now()
	expiresAt := now.Add(ttl)
	manifest := shareManifest{
		Version:   1,
		CreatedAt: now.UTC().Format(time.RFC3339),
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		Items:     items,
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	shareID := newUUID()
	name := shareManifestPath(g, shareID)
	// create-if-absent: the id is a fresh UUID, so this never conflicts in
	// practice; a bucket lifecycle rule (terraform/backend/main.tf) cleans the object up
	// later regardless of whether anyone ever reads it.
	if _, err := s.store.Write(r.Context(), name, body, gen(createOnly)); err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	url, _, err := s.signer.SignURL(r.Context(), SignRequest{Object: name, Method: "GET", Expires: ttl})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "sign failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"share_id":   shareID,
		"url":        url,
		"expires_at": manifest.ExpiresAt,
	})
}
