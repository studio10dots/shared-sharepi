package main

import (
	"encoding/json"
	"testing"
)

// Who may delete what. A member's photos and videos are theirs to remove (a
// wrong one sent by mistake); only the backend's owner may remove someone
// else's, delete an event that holds photos, or look into the trash. Before this
// anyone could do all of it, so one member could empty a group.

func TestAMemberDeletesOnlyWhatTheyUploaded(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	e.seedItem("bbq00001", pid2, "jpg", "u-carol", testNow)
	// carol is not in the roster here: she uploaded earlier and has since left.

	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid2, "alice", ""), 403)
	if !e.st.has(obj(group, "bbq00001", "original", pid2, "jpg")) {
		t.Fatal("a member's refused delete removed someone else's photo")
	}
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "alice", ""), 204)
	if e.st.has(obj(group, "bbq00001", "original", pid, "jpg")) {
		t.Error("a member could not delete their own photo")
	}
}

func TestTheOwnerDeletesAnyonesPhotoWithoutBeingAMember(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	e.seedItem("bbq00001", pid2, "mp4", "u-gone", testNow)

	// The administrator is not in this group's roster, and need not be.
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "admin", ""), 204)
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid2, "admin", ""), 204)
}

func TestAnItemWithNoUploaderIsTheOwnersToDelete(t *testing.T) {
	e := newEnv(t)
	// From before the uploader was recorded: nobody can say it is theirs.
	e.st.put(obj(group, "bbq00001", "thumbnail", pid, "jpg"), 30, nil, testNow)
	e.st.put(obj(group, "bbq00001", "original", pid, "jpg"), 3000, nil, testNow)

	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "alice", ""), 403)
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "admin", ""), 204)
}

func TestAStrangerAndARemovedMemberDeleteNothing(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-bob", testNow) // bob is removed

	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "bob", ""), 404)
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "carol", ""), 404)
	if !e.st.has(obj(group, "bbq00001", "original", pid, "jpg")) {
		t.Error("someone with no access deleted a photo")
	}
}

func TestAMemberDeletesAnEventOnlyWhileItHoldsNothing(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("empty001", "Mistake", "2026/09/23")
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)

	// Even an event with only their own photos is the owner's to delete: the
	// photos can be deleted one by one first.
	want(t, e.do("DELETE", evPath("bbq00001"), "alice", ""), 403)
	want(t, e.do("DELETE", evPath("empty001"), "alice", ""), 200)
	if e.st.has(group + "/empty001/event.json") {
		t.Error("the empty event was not deleted")
	}

	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "alice", ""), 204)
	want(t, e.do("DELETE", evPath("bbq00001"), "alice", ""), 200)
}

func TestTheOwnerDeletesAnEventWithPhotos(t *testing.T) {
	e := newEnv(t)
	e.seedEvent("bbq00001", "BBQ", "2026/09/23")
	e.seedItem("bbq00001", pid, "jpg", "u-carol", testNow)

	want(t, e.do("DELETE", evPath("bbq00001"), "admin", ""), 200)
}

func TestOnlyTheOwnerSeesAndRestoresTheTrash(t *testing.T) {
	e := newEnv(t)
	e.seedItem("bbq00001", pid, "jpg", "u-alice", testNow)
	want(t, e.do("DELETE", evPath("bbq00001")+"/items/"+pid, "alice", ""), 204)
	trash := e.trash(t, "admin")
	if len(trash) != 3 {
		t.Fatalf("the owner sees %d objects in the trash, want 3", len(trash))
	}
	body, _ := json.Marshal(map[string]any{"objects": trash})

	// A member (even the one who deleted it) cannot look into it or restore from it.
	want(t, e.do("GET", "/v1/groups/"+group+"/trash", "alice", ""), 403)
	want(t, e.do("POST", "/v1/groups/"+group+"/trash/restore", "alice", string(body)), 403)
	want(t, e.do("GET", "/v1/groups/"+group+"/trash/thumbnail?path="+trash[0].Path+"&generation="+trash[0].Generation, "alice", ""), 403)
	// A stranger learns nothing, not even that there is a trash.
	want(t, e.do("GET", "/v1/groups/"+group+"/trash", "carol", ""), 404)

	want(t, e.do("POST", "/v1/groups/"+group+"/trash/restore", "admin", string(body)), 200)
}
