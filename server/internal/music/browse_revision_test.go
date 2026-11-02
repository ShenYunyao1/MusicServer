package music

import (
	"fmt"
	"testing"
)

func TestBrowseRevisionIgnoresOtherAccountsPrivateChanges(t *testing.T) {
	a, _ := testAPI(t)
	alice, _ := readyMember(t, a, "revision-alice")
	bob, _ := readyMember(t, a, "revision-bob")
	first := browseTestPage(t, browseTestRequest(a, alice, "GET", "/api/v2/browse/playlists", "", 0))
	if err := a.store.PutPlaylist(Playlist{ID: "bobs-private", Name: "Private", TrackIDs: []string{}}, MutationOptions{UserID: bob.ID}); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v2/browse/playlists?revision=%.0f", first["revision"])
	second := browseTestRequest(a, alice, "GET", path, "", 0)
	if second.Code != 200 {
		t.Fatal("another account invalidated private pagination", second.Code, second.Body)
	}
	if err := a.store.PutPlaylist(Playlist{ID: "alices-private", Name: "Relevant", TrackIDs: []string{}}, MutationOptions{UserID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	if changed := browseTestRequest(a, alice, "GET", path, "", 0); changed.Code != 409 {
		t.Fatal("own changes failed to invalidate baseline", changed.Code, changed.Body)
	}
}
