package music

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlaylistMemberOperations(t *testing.T) {
	api, root := testAPI(t)
	if err := os.WriteFile(filepath.Join(root, "Another - Song.mp3"), []byte("another song"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	tracks := api.store.Tracks()
	first, second := tracks[0].ID, tracks[1].ID
	if err := api.store.PutPlaylist(Playlist{ID: "mix", Name: "Mix", TrackIDs: []string{first}}); err != nil {
		t.Fatal(err)
	}
	base := "/api/v2/playlists/mix"
	assertStatus := func(method, path, body string, want int) {
		t.Helper()
		response := callAPI(api, method, path, body)
		if response.Code != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, response.Code, want, response.Body.String())
		}
	}
	assertStatus(http.MethodPost, base+"/tracks", `{"track_ids":["`+second+`","`+first+`"]}`, http.StatusOK)
	assertStatus(http.MethodPatch, base+"/tracks/"+second, `{"note":"Play next"}`, http.StatusOK)
	assertStatus(http.MethodPatch, base, `{"name":"New mix","description":"Updated"}`, http.StatusOK)
	playlist, _ := api.store.Playlist("mix")
	if playlist.Name != "New mix" || !reflect.DeepEqual(playlist.TrackIDs, []string{first, second}) || playlist.TrackNotes[second] != "Play next" {
		t.Fatalf("member or metadata lost: %+v", playlist)
	}
	assertStatus(http.MethodPut, base+"/tracks/order", `{"track_ids":["`+second+`","`+first+`"],"expected_track_ids":["`+first+`","`+second+`"]}`, http.StatusOK)
	assertStatus(http.MethodPut, base+"/tracks/order", `{"track_ids":["`+first+`","`+second+`"],"expected_track_ids":["`+first+`","`+second+`"]}`, http.StatusConflict)
	assertStatus(http.MethodDelete, base+"/tracks/"+first, "", http.StatusNoContent)
	assertStatus(http.MethodDelete, base+"/tracks/"+first, "", http.StatusNotFound)
	assertStatus(http.MethodPost, "/api/v2/playlists/library/tracks", `{"track_ids":["`+first+`"]}`, http.StatusConflict)
	assertStatus(http.MethodPost, base+"/tracks", `{"track_ids":["missing"]}`, http.StatusBadRequest)
	assertStatus(http.MethodPut, base+"/tracks/order", `{"track_ids":["`+second+`","`+second+`"],"expected_track_ids":["`+second+`"]}`, http.StatusBadRequest)
	if _, exists := api.store.Track(first); !exists {
		t.Fatal("removing a playlist member deleted the library track")
	}
	path := api.store.DataPath()
	if err := api.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	playlist, _ = reopened.Playlist("mix")
	if !reflect.DeepEqual(playlist.TrackIDs, []string{second}) || playlist.TrackNotes[second] != "Play next" {
		t.Fatalf("member state lost after restart: %+v", playlist)
	}
	response := callAPI(NewAPI(reopened), http.MethodGet, base, "")
	var detail struct {
		Playlist Playlist `json:"playlist"`
		Tracks   []Track  `json:"tracks"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || len(detail.Tracks) != 1 || detail.Tracks[0].ID != second {
		t.Fatalf("playlist read returned wrong tracks: %s", response.Body.String())
	}
}
