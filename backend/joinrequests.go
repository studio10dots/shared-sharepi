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
)

// Invitation requests. Someone who was sent a link into a group they are not in
// (see "Opening a link" in docs/BACKEND_DESIGN.md) can ask the group's owner to
// invite them. The request is an object under the group prefix, like a report,
// so the owner's app finds it by a listing and the backend keeps no database:
//
//	{group}/joins/{key}.json
//
// `key` is 32 hex digits of the requester's keyed member id (id.Sub), so one
// person has one request per group: asking again replaces it (a new note, a new
// time) instead of piling up. It is not the person's Google id, and only the
// owner can read it.
//
// A request grants nothing and opens nothing: the owner decides whether to
// invite, with the invitation they already know (QR or code). When that person
// joins, their request is removed by the redemption itself.

const (
	maxJoinNote = 200
	// maxJoinRequests bounds what a group's owner can be handed. Past it a new
	// person's request is dropped without a word (the caller still hears "sent":
	// a refusal would also tell a stranger that the group exists).
	maxJoinRequests = 50
)

var joinKeyRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// JoinRequest is the object stored at {group}/joins/{id}.json.
type JoinRequest struct {
	ID          string `json:"id"`
	Nickname    string `json:"nickname"`
	Note        string `json:"note,omitempty"`
	RequestedAt string `json:"requested_at"`
}

func joinRequestName(g, id string) string { return g + "/joins/" + id + ".json" }

// joinKey is the part of a member id that names a request. "" when it cannot be
// made (never for an identity that came through auth).
func joinKey(memberID string) string {
	k := strings.TrimPrefix(memberID, "h1:")
	if len(k) < 32 {
		return ""
	}
	k = k[:32]
	if !joinKeyRe.MatchString(k) {
		return ""
	}
	return k
}

// requestJoin is POST /v1/groups/{g}/join-requests: any signed-in person may ask
// to be invited to a group they know the id of.
//
// What the answer tells a stranger is the same whether the group exists or not
// ("sent"), because the rule everywhere else is that a stranger learns nothing
// about a group, not even that it exists. The only different answer is for
// someone who is a member already, which tells them nothing new.
func (s *Server) requestJoin(w http.ResponseWriter, r *http.Request, id Identity) {
	g := r.PathValue("g")
	if !groupIDRe.MatchString(g) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid group")
		return
	}
	var req struct {
		Nickname string `json:"nickname"`
		Note     string `json:"note"`
	}
	if !decode(w, r, &req) {
		return
	}
	nick, ok := validText(req.Nickname, 30)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "invalid nickname")
		return
	}
	note := strings.TrimSpace(req.Note)
	if note != "" {
		if note, ok = validText(note, maxJoinNote); !ok {
			writeErr(w, http.StatusUnprocessableEntity, "invalid note")
			return
		}
	}
	key := joinKey(id.Sub)
	if key == "" {
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}

	ro, err := s.loadRoster(r.Context(), g)
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "sent"})
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	if _, _, member := ro.activeID(id); member {
		writeJSON(w, http.StatusOK, map[string]string{"status": "member"})
		return
	}

	// A person already asking may ask again (it replaces the earlier request);
	// a new person is let in only while there is room.
	existing, err := s.store.List(r.Context(), g+"/joins/", "", false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	mine := joinRequestName(g, key)
	has := false
	for _, o := range existing.Objects {
		if o.Name == mine {
			has = true
		}
	}
	if !has && len(existing.Objects) >= maxJoinRequests {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "sent"})
		return
	}

	body, err := json.Marshal(JoinRequest{ID: key, Nickname: nick, Note: note, RequestedAt: s.nowRFC3339()})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := s.store.Write(r.Context(), mine, body, nil); err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "sent"})
}

// listJoinRequests is GET /v1/groups/{g}/join-requests (administrator): the
// group's open requests, oldest first.
func (s *Server) listJoinRequests(w http.ResponseWriter, r *http.Request, _ Identity, g string, _ Roster) {
	reqs, err := s.readJoinRequests(r.Context(), g)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": reqs})
}

func (s *Server) readJoinRequests(ctx context.Context, g string) ([]JoinRequest, error) {
	l, err := s.store.List(ctx, g+"/joins/", "", false)
	if err != nil {
		return nil, err
	}
	names := l.Objects
	if len(names) > maxJoinRequests {
		names = names[:maxJoinRequests]
	}
	out := make([]JoinRequest, 0, len(names))
	var mu sync.Mutex
	var firstErr error
	parallel(8, names, func(_ int, o ObjectInfo) {
		b, _, err := s.store.Read(ctx, o.Name)
		mu.Lock()
		defer mu.Unlock()
		if errors.Is(err, ErrNotFound) {
			return // resolved while we were reading
		}
		if err != nil {
			firstErr = err
			return
		}
		var jr JoinRequest
		if json.Unmarshal(b, &jr) != nil || !joinKeyRe.MatchString(jr.ID) {
			return // not one of ours
		}
		out = append(out, jr)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RequestedAt != out[j].RequestedAt {
			return out[i].RequestedAt < out[j].RequestedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// deleteJoinRequest is DELETE /v1/groups/{g}/join-requests/{id} (administrator):
// the owner has dealt with it, or does not want it.
func (s *Server) deleteJoinRequest(w http.ResponseWriter, r *http.Request, _ Identity, g string, _ Roster) {
	key := r.PathValue("id")
	if !joinKeyRe.MatchString(key) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid request")
		return
	}
	if err := s.store.Delete(r.Context(), joinRequestName(g, key)); err != nil && !errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// openJoinRequests is GET /v1/join-requests/open (administrator): how many open
// requests each group of the backend has, {"groups": {"<group>": n}}, only the
// groups that have any. The owner's app asks with its report check, about once
// a minute while it is in the foreground, so it only lists objects.
func (s *Server) openJoinRequests(w http.ResponseWriter, r *http.Request, id Identity) {
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
	out := map[string]int{}
	var mu sync.Mutex
	var firstErr error
	parallel(8, groups, func(_ int, g string) {
		l, err := s.store.List(r.Context(), g+"/joins/", "", false)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			firstErr = err
			return
		}
		if n := len(l.Objects); n > 0 {
			out[g] = n
		}
	})
	if firstErr != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

// forgetJoinRequest removes the request of a person who has just joined. Best
// effort: a request left behind is only a line the owner can dismiss.
func (s *Server) forgetJoinRequest(ctx context.Context, g string, id Identity) {
	if key := joinKey(id.Sub); key != "" {
		_ = s.store.Delete(ctx, joinRequestName(g, key))
	}
}
