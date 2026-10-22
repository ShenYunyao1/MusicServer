package music

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
)

func TestTrackLifecyclePrivateReferencesAndPlaceholders(t *testing.T) {
	api, _ := testAPI(t)
	a, at := readyMember(t, api, "lifecycle-a")
	b, _ := readyMember(t, api, "lifecycle-b")
	tr, err := api.store.ImportAudioWithMetadata("owned.wav", bytes.NewReader(validTestAudio(t)), nil, MutationOptions{UserID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.store.PutPlaylist(Playlist{ID: "b-private", Name: "Secret", TrackIDs: []string{tr.ID}, TrackNotes: map[string]string{tr.ID: "keep me"}}, MutationOptions{UserID: b.ID}); err != nil {
		t.Fatal(err)
	}
	rev := api.store.Revision()
	r := memberVersionRequest(api, at, "DELETE", "/api/v2/tracks/"+tr.ID, "", tr.Version)
	if r.Code != 409 || !bytes.Contains(r.Body.Bytes(), []byte("TRACK_IN_USE")) || api.store.Revision() != rev {
		t.Fatal("reference guard", r.Code, r.Body.String())
	}
	enabled := false
	_, err = api.store.UpdateUser(b.ID, AccountPatch{Enabled: &enabled}, MutationOptions{ExpectedVersion: b.Version})
	if err != nil {
		t.Fatal(err)
	}
	r = memberVersionRequest(api, at, "DELETE", "/api/v2/tracks/"+tr.ID, "", tr.Version)
	if r.Code != 409 {
		t.Fatal("disabled owner reference not protected", r.Code)
	}
	r = callAPI(api, "DELETE", "/api/v2/tracks/"+tr.ID, "")
	if r.Code != 204 {
		t.Fatal("admin removal", r.Code, r.Body.String())
	}
	if r := memberVersionRequest(api, at, "GET", "/api/v2/media/"+tr.ID, "", 0); r.Code != 404 {
		t.Fatal("removed media", r.Code)
	}
	current, _ := readUser(api.store.db, b.ID)
	enabled = true
	api.store.UpdateUser(b.ID, AccountPatch{Enabled: &enabled}, MutationOptions{ExpectedVersion: current.Version})
	// Use the administrator read to verify retained private member notes and minimal DTO.
	r = callAPI(api, "GET", "/api/v2/playlists/b-private", "")
	var view struct {
		Playlist Playlist `json:"playlist"`
		Tracks   []Track  `json:"tracks"`
	}
	json.Unmarshal(r.Body.Bytes(), &view)
	if len(view.Tracks) != 1 || view.Tracks[0].ID != tr.ID || !view.Tracks[0].Removed || view.Tracks[0].Title != "Unavailable" || view.Playlist.TrackNotes[tr.ID] != "keep me" {
		t.Fatal("placeholder", r.Body.String())
	}
	if err := api.store.RestoreTrack(tr.ID); err != nil {
		t.Fatal(err)
	}
	p, _ := api.store.Playlist("b-private")
	api.store.RemovePlaylistTrack(p.ID, tr.ID, MutationOptions{ExpectedVersion: p.Version, UserID: b.ID})
	tr, _ = api.store.Track(tr.ID)
	r = memberVersionRequest(api, at, "DELETE", "/api/v2/tracks/"+tr.ID, "", tr.Version)
	if r.Code != 204 {
		t.Fatal("unreferenced owner delete", r.Code, r.Body.String())
	}
	if _, err := api.store.AddPlaylistTracks(p.ID, []string{tr.ID}); err == nil {
		t.Fatal("added downlisted track")
	}
}

func TestTrackLifecycleAddDeleteRaceAndRollback(t *testing.T) {
	api, _ := testAPI(t)
	a, _ := readyMember(t, api, "race-owner")
	b, _ := readyMember(t, api, "race-collector")
	tr, err := api.store.ImportAudioWithMetadata("race.wav", bytes.NewReader(validTestAudio(t)), nil, MutationOptions{UserID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.store.PutPlaylist(Playlist{ID: "race-list", Name: "Race"}, MutationOptions{UserID: b.ID}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		var addErr, deleteErr error
		go func() {
			defer wg.Done()
			_, addErr = api.store.AddPlaylistTracks("race-list", []string{tr.ID}, MutationOptions{UserID: b.ID})
		}()
		go func() { defer wg.Done(); deleteErr = api.store.DeleteTrack(tr.ID, MutationOptions{UserID: a.ID}) }()
		wg.Wait()
		if addErr == nil && deleteErr == nil {
			t.Fatal("both reference and owner removal succeeded")
		}
		api.store.RestoreTrack(tr.ID)
		p, _ := api.store.Playlist("race-list")
		if len(p.TrackIDs) > 0 {
			if err := api.store.RemovePlaylistTrack(p.ID, tr.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := api.store.Revision()
	tr, _ = api.store.Track(tr.ID)
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_removal BEFORE UPDATE ON tracks WHEN NEW.removed=1 BEGIN SELECT RAISE(ABORT,'test failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := api.store.DeleteTrack(tr.ID, MutationOptions{UserID: a.ID}); err == nil {
		t.Fatal("trigger not reached")
	}
	after, ok := api.store.Track(tr.ID)
	if !ok || after.Version != tr.Version || api.store.Revision() != before {
		t.Fatal("failure leaked mutation")
	}
}
