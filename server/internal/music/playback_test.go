package music

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestPlaybackDirectoryPagingAndAccess(t *testing.T) {
	api, _ := testAPI(t)
	alice, _ := readyMember(t, api, "playback-alice")
	bob, _ := readyMember(t, api, "playback-bob")
	ids := browseSeedTracks(t, api, alice.ID, alice.ID, 105)
	if err := api.store.PutPlaylist(Playlist{ID: "playback", Name: "Queue", TrackIDs: ids, TrackNotes: map[string]string{ids[0]: "private note"}}, MutationOptions{UserID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	request := func(user User, source, extra string) map[string]any {
		return browseTestPage(t, browseTestRequest(api, user, "GET", "/api/v2/browse/playback/playback?source="+source+extra, "", 0))
	}
	first := request(alice, "own", "&limit=100")
	if first["total"] != float64(105) || len(first["items"].([]any)) != 100 {
		t.Fatal(first)
	}
	item := first["items"].([]any)[0].(map[string]any)
	if item["lyrics"] != nil || item["playlist_note"] != nil || item["detail_loaded"] != false {
		t.Fatal(item)
	}
	last := request(alice, "own", fmt.Sprintf("&offset=100&limit=100&revision=%.0f", first["revision"]))
	if len(last["items"].([]any)) != 5 || last["items"].([]any)[0].(map[string]any)["id"] != ids[100] {
		t.Fatal(last)
	}
	if r := browseTestRequest(api, bob, "GET", "/api/v2/browse/playback/playback?source=own", "", 0); r.Code != 404 {
		t.Fatal(r.Code, r.Body)
	}
	p, _ := api.store.Playlist("playback")
	p, err := api.store.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: alice.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	request(bob, "public", "&limit=1")
	searched := request(bob, "public", "&limit=1&q=private%20note")
	if searched["total"] != float64(105) {
		t.Fatal("playback search must not expose private note matches", searched)
	}
	if r := browseTestRequest(api, alice, "GET", fmt.Sprintf("/api/v2/browse/playback/playback?source=own&revision=%.0f", first["revision"]), "", 0); r.Code != 409 {
		t.Fatal(r.Code, r.Body)
	}
	if _, err = api.store.Subscribe(p.ID, MutationOptions{UserID: bob.ID}); err != nil {
		t.Fatal(err)
	}
	request(bob, "subscription", "&limit=1")
	if err = api.store.Unsubscribe(p.ID, MutationOptions{UserID: bob.ID}); err != nil {
		t.Fatal(err)
	}
	if r := browseTestRequest(api, bob, "GET", "/api/v2/browse/playback/playback?source=subscription", "", 0); r.Code != 404 {
		t.Fatal(r.Code, r.Body)
	}
	if _, err = api.store.SetPlaylistVisibility(p.ID, "private", MutationOptions{UserID: alice.ID, ExpectedVersion: p.Version}); err != nil {
		t.Fatal(err)
	}
	if r := browseTestRequest(api, bob, "GET", "/api/v2/browse/playback/playback?source=public", "", 0); r.Code != 404 {
		t.Fatal(r.Code, r.Body)
	}
	if r := browseTestRequest(api, alice, "GET", "/api/v2/browse/playback/library?source=own", "", 0); r.Code != 404 {
		t.Fatal(r.Code, r.Body)
	}
	// Invalid source must never fall back to administrator access.
	r := browseTestRequest(api, alice, "GET", "/api/v2/browse/playback/playback?source=invalid", "", 0)
	if r.Code != 404 {
		b, _ := json.Marshal(r.Body.String())
		t.Fatal(r.Code, string(b))
	}
}

func TestPlaybackSharedInvitationRevocation(t *testing.T) {
	api, a, b, p := sharingFixture(t)
	bt := communityLogin(t, api.store, b.Username)
	check := func(want int) {
		t.Helper()
		r := sessionRequest(api, bt, "GET", "/api/v2/browse/playback/"+p.ID+"?source=subscription&limit=1", "")
		if r.Code != want {
			t.Fatalf("got %d want %d: %s", r.Code, want, r.Body)
		}
	}
	check(404)
	invite, err := api.store.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	check(404)
	invite, err = api.store.InvitationAction(invite.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: invite.Version})
	if err != nil {
		t.Fatal(err)
	}
	check(200)
	_, err = api.store.InvitationAction(invite.ID, "revoke", MutationOptions{UserID: a.ID, ExpectedVersion: invite.Version})
	if err != nil {
		t.Fatal(err)
	}
	check(404)
}
