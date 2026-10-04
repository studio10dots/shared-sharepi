package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// A member's favorites are personal: folders they name and the photos and videos
// they filed in them. They live in the bucket, at {group}/members/{user_id}/
// favorites.json, so they survive a reinstall and come back with a recovered
// group. The backend is the only writer (Agents.md section 7): the app sends
// operations, the backend applies them to the stored document with a generation
// precondition and retries on conflict, so two devices never overwrite each other.
//
// "members" is a reserved folder name in the group's namespace, like "invites"
// and "reports" (see validEventID).

const (
	// defaultFavFolder is the implicit folder every member has. It is never
	// stored in Folders, cannot be renamed or deleted, and always comes first.
	defaultFavFolder = "default"

	maxFavFolders = 100
	maxFavItems   = 5000
	maxFavOps     = 200
	maxFavName    = 30
)

var favFolderIDRe = regexp.MustCompile(`^[a-z0-9]{8,32}$`)

type favFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type favItem struct {
	PhotoID string `json:"photo_id"`
	Event   string `json:"event"`
	Path    string `json:"path"` // the original object's path
	// Folders holds the ids of the folders the item is in (defaultFavFolder for
	// the implicit one). An item in no folder is not stored.
	Folders []string `json:"folders"`
	AddedAt string   `json:"added_at"`
}

// favDoc is the stored document. Folders are in display order.
type favDoc struct {
	Version int         `json:"version"`
	Folders []favFolder `json:"folders"`
	Items   []favItem   `json:"items"`
}

func newFavDoc() favDoc { return favDoc{Version: 1, Folders: []favFolder{}, Items: []favItem{}} }

// favOp is one change. Which fields apply depends on Op:
//
//	folder_create  id, name      (idempotent: an existing id is left as it is)
//	folder_rename  id, name      (an unknown id is ignored: it was deleted elsewhere)
//	folder_delete  id            (items left in no folder are dropped)
//	folder_order   ids           (listed folders first, in this order)
//	item_set       photo_id, event, path, folders, added_at
//	               (folders replaces the item's folders; empty removes the item;
//	               folders that no longer exist are ignored)
type favOp struct {
	Op      string   `json:"op"`
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	IDs     []string `json:"ids"`
	PhotoID string   `json:"photo_id"`
	Event   string   `json:"event"`
	Path    string   `json:"path"`
	Folders []string `json:"folders"`
	AddedAt string   `json:"added_at"`
}

var errFavInvalid = errors.New("invalid favorites operation")

func favName(g, userID string) string { return g + "/members/" + userID + "/favorites.json" }

// readFavorites returns the member's document and its version (createOnly
// when there is none yet).
func (s *Server) readFavorites(r *http.Request, g, userID string) (favDoc, Version, error) {
	b, generation, err := s.store.Read(r.Context(), favName(g, userID))
	if errors.Is(err, ErrNotFound) {
		return newFavDoc(), createOnly, nil
	}
	if err != nil {
		return favDoc{}, "", err
	}
	doc := newFavDoc()
	if err := json.Unmarshal(b, &doc); err != nil {
		return favDoc{}, "", err
	}
	if doc.Folders == nil {
		doc.Folders = []favFolder{}
	}
	if doc.Items == nil {
		doc.Items = []favItem{}
	}
	return doc, generation, nil
}

func (s *Server) getFavorites(w http.ResponseWriter, r *http.Request, id Identity, g string, me Member) {
	doc, _, err := s.readFavorites(r, g, me.UserID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// postFavoriteOps applies a batch of operations atomically: if any is invalid,
// nothing is written.
func (s *Server) postFavoriteOps(w http.ResponseWriter, r *http.Request, id Identity, g string, me Member) {
	var req struct {
		Ops []favOp `json:"ops"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Ops) == 0 || len(req.Ops) > maxFavOps {
		writeErr(w, http.StatusUnprocessableEntity, "invalid favorites operation")
		return
	}
	for range 6 {
		doc, generation, err := s.readFavorites(r, g, me.UserID)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		if err := s.applyFavOps(g, &doc, req.Ops); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		b, _ := json.Marshal(doc)
		if _, err := s.store.Write(r.Context(), favName(g, me.UserID), b, gen(generation)); err != nil {
			if errors.Is(err, ErrPrecondition) {
				continue
			}
			writeErr(w, http.StatusBadGateway, "storage error")
			return
		}
		writeJSON(w, http.StatusOK, doc)
		return
	}
	writeErr(w, http.StatusConflict, "favorites update kept conflicting")
}

func (s *Server) applyFavOps(g string, doc *favDoc, ops []favOp) error {
	for _, op := range ops {
		var err error
		switch op.Op {
		case "folder_create":
			err = favCreateFolder(doc, op)
		case "folder_rename":
			err = favRenameFolder(doc, op)
		case "folder_delete":
			err = favDeleteFolder(doc, op)
		case "folder_order":
			err = favOrderFolders(doc, op)
		case "item_set":
			err = s.favSetItem(g, doc, op)
		default:
			err = errFavInvalid
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func favFolderExists(doc *favDoc, id string) bool {
	if id == defaultFavFolder {
		return true
	}
	for _, f := range doc.Folders {
		if f.ID == id {
			return true
		}
	}
	return false
}

func favCreateFolder(doc *favDoc, op favOp) error {
	name, ok := validText(op.Name, maxFavName)
	if !ok || !favFolderIDRe.MatchString(op.ID) {
		return errFavInvalid
	}
	if favFolderExists(doc, op.ID) {
		return nil
	}
	if len(doc.Folders) >= maxFavFolders {
		return errFavInvalid
	}
	doc.Folders = append(doc.Folders, favFolder{ID: op.ID, Name: name})
	return nil
}

func favRenameFolder(doc *favDoc, op favOp) error {
	name, ok := validText(op.Name, maxFavName)
	if !ok || !favFolderIDRe.MatchString(op.ID) {
		return errFavInvalid
	}
	for i := range doc.Folders {
		if doc.Folders[i].ID == op.ID {
			doc.Folders[i].Name = name
		}
	}
	return nil
}

func favDeleteFolder(doc *favDoc, op favOp) error {
	if !favFolderIDRe.MatchString(op.ID) {
		return errFavInvalid
	}
	folders := doc.Folders[:0]
	for _, f := range doc.Folders {
		if f.ID != op.ID {
			folders = append(folders, f)
		}
	}
	doc.Folders = folders
	items := doc.Items[:0]
	for _, it := range doc.Items {
		kept := it.Folders[:0]
		for _, f := range it.Folders {
			if f != op.ID {
				kept = append(kept, f)
			}
		}
		it.Folders = kept
		if len(kept) > 0 {
			items = append(items, it)
		}
	}
	doc.Items = items
	return nil
}

func favOrderFolders(doc *favDoc, op favOp) error {
	if len(op.IDs) > maxFavFolders {
		return errFavInvalid
	}
	byID := map[string]favFolder{}
	for _, f := range doc.Folders {
		byID[f.ID] = f
	}
	ordered := make([]favFolder, 0, len(doc.Folders))
	seen := map[string]bool{}
	for _, id := range op.IDs {
		if !favFolderIDRe.MatchString(id) {
			return errFavInvalid
		}
		if f, ok := byID[id]; ok && !seen[id] {
			ordered = append(ordered, f)
			seen[id] = true
		}
	}
	for _, f := range doc.Folders {
		if !seen[f.ID] {
			ordered = append(ordered, f)
		}
	}
	doc.Folders = ordered
	return nil
}

func (s *Server) favSetItem(g string, doc *favDoc, op favOp) error {
	if !photoIDRe.MatchString(op.PhotoID) || !validEventID(op.Event) || !favPathOK(g, op) {
		return errFavInvalid
	}
	var folders []string
	seen := map[string]bool{}
	for _, f := range op.Folders {
		if f != defaultFavFolder && !favFolderIDRe.MatchString(f) {
			return errFavInvalid
		}
		if favFolderExists(doc, f) && !seen[f] {
			folders = append(folders, f)
			seen[f] = true
		}
	}
	for i, it := range doc.Items {
		if it.PhotoID != op.PhotoID {
			continue
		}
		if len(folders) == 0 {
			doc.Items = append(doc.Items[:i], doc.Items[i+1:]...)
		} else {
			doc.Items[i].Folders = folders
		}
		return nil
	}
	if len(folders) == 0 {
		return nil // removing what is not there
	}
	if len(doc.Items) >= maxFavItems {
		return errFavInvalid
	}
	added := op.AddedAt
	if _, err := time.Parse(time.RFC3339, added); err != nil {
		added = s.now().UTC().Format(time.RFC3339)
	}
	doc.Items = append(doc.Items, favItem{PhotoID: op.PhotoID, Event: op.Event, Path: op.Path, Folders: folders, AddedAt: added})
	return nil
}

// favPathOK: the path must be this group's original object of that photo, so a
// favorite can never point outside the caller's group.
func favPathOK(g string, op favOp) bool {
	pre := g + "/" + op.Event + "/original/" + op.PhotoID + "."
	ext, ok := strings.CutPrefix(op.Path, pre)
	return ok && extRe.MatchString(ext)
}
