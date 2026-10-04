package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// version and repo are baked in at build time by release-backend.yml
// (-ldflags "-X main.version=... -X main.repo=..."). A local/dev build (no
// ldflags) has version "dev" and repo "", which disables the check below:
// there is nothing meaningful to compare and no repo to ask.
var (
	version = "dev"
	repo    = ""
)

// UpdateNotice tells an administrator a newer backend image has been
// published; they update it themselves by re-running Terraform
// (Agents.md section 8, "backend/app API version mismatch").
type UpdateNotice struct {
	Version string `json:"version"`
}

var backendTagRe = regexp.MustCompile(`^backend-v(\d+)\.(\d+)\.(\d+)$`)

// checkForUpdateAsync runs once, in the background, right after the process
// starts. A burst of the very first requests to a cold instance must not
// wait on a round trip to GitHub, so this never blocks a request: the result
// (if any) becomes available only once the check completes, for
// consumeUpdateNotice to hand out - once - to whichever request asks first.
func (s *Server) checkForUpdateAsync(hc *http.Client) {
	if repo == "" || version == "dev" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		latest, ok := latestBackendTag(ctx, hc, repo)
		if !ok || latest == version {
			return
		}
		s.updateNotice.Store(&UpdateNotice{Version: latest})
	}()
}

// consumeUpdateNotice hands out a pending notice at most once: the first
// caller to arrive after the background check completes gets it, and every
// other caller (on this instance, for the rest of its life) gets nil. A
// second Cloud Run instance runs its own check and may hand out its own
// notice; the app dedupes by version in its local database, the same way it
// already dedupes report notifications (Agents.md section 9).
func (s *Server) consumeUpdateNotice() *UpdateNotice {
	return s.updateNotice.Swap(nil)
}

// latestBackendTag returns the highest backend-vX.Y.Z tag's version (without
// the "backend-v" prefix), or ok=false if the request failed or no such tag
// was found.
func latestBackendTag(ctx context.Context, hc *http.Client, repo string) (string, bool) {
	return latestBackendTagAt(ctx, hc, "https://api.github.com/repos/"+repo+"/tags?per_page=100")
}

// latestBackendTagAt is latestBackendTag against an explicit URL, so tests
// can point it at an httptest server instead of GitHub.
func latestBackendTagAt(ctx context.Context, hc *http.Client, url string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", false
	}
	var best string
	var bestParts [3]int
	for _, t := range tags {
		m := backendTagRe.FindStringSubmatch(t.Name)
		if m == nil {
			continue
		}
		parts := [3]int{atoi(m[1]), atoi(m[2]), atoi(m[3])}
		if best == "" || higher(parts, bestParts) {
			best, bestParts = strings.TrimPrefix(t.Name, "backend-v"), parts
		}
	}
	return best, best != ""
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func higher(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
