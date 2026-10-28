package music

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// These are the unchanged contracts used by the complete-object and object-delta
// player generations. Tag presentation remains a session-only concern.
func TestTrackTagsCompatibilityPlayerProtocolsAndAudioCache(t *testing.T) {
	api, admin, _, member, _, _ := tagLinkFixture(t)
	s := api.store
	original := s.Tracks()[0]
	hidden := mergeTestTrack(t, s, admin)
	const playlistID = "tag-player-compat"
	if err := s.PutPlaylist(Playlist{ID: playlistID, Name: "Player contract", TrackIDs: []string{original.ID}}); err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "compat", AllowedPlaylistIDs: []string{playlistID}})
	if err != nil {
		t.Fatal(err)
	}
	source := mustLinkTag(t, s, admin, "public", "compat-public-source")
	target := mustLinkTag(t, s, admin, "public", "compat-public-target")
	personal := mustLinkTag(t, s, member, "personal", "compat-private-secret")
	privatePreset, err := s.CreateSavedTrackFilter(member, SavedTrackFilterInput{Name: "compat-private-preset", Filter: TrackFilter{Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{source.ID, source.Name, target.ID, target.Name, personal.ID, personal.Name, privatePreset.ID, privatePreset.Name}
	request := func(path string) []byte {
		t.Helper()
		r := callAPIWithKey(api, "GET", path, "", key.Secret)
		if r.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, r.Code, r.Body.String())
		}
		for _, secret := range secrets {
			if strings.Contains(r.Body.String(), secret) {
				t.Fatalf("session-only tag/filter data leaked through %s", path)
			}
		}
		return r.Body.Bytes()
	}
	getSync := func(protocol, cursor string) SyncResponse {
		t.Helper()
		var result SyncResponse
		if err := json.Unmarshal(request("/api/v2/sync?limit=100&protocol="+protocol+"&cursor="+cursor), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	playlistBefore := string(request("/api/v2/playlists"))
	var projection map[string]any
	if err := json.Unmarshal(request("/api/v2/tracks/"+original.ID), &projection); err != nil {
		t.Fatal(err)
	}
	old := getSync("1", "")
	objects := getSync("2", "")
	if len(old.Changed) != 1 || old.Changed[0].ID != original.ID || !slices.Equal(objects.ChangedTrackIDs, []string{original.ID}) {
		t.Fatal("initial snapshots escaped the explicit playlist grant", old, objects)
	}
	media := func(etag string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v2/media/"+original.ID, nil)
		r.Header.Set("X-API-Key", key.Secret)
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	audio := media("")
	if audio.Code != 200 || audio.Header().Get("ETag") != fmt.Sprintf("\"%s\"", original.ContentHash) {
		t.Fatal("initial audio/cache contract", audio.Code, audio.Header())
	}
	steps := []struct {
		name         string
		sharedChange bool
		mutate       func() error
	}{
		{"personal binding", false, func() error {
			_, err := s.ChangeTrackTags(member, original.ID, "personal", 1, []string{personal.ID}, nil)
			return err
		}},
		{"public binding", true, func() error {
			_, err := s.ChangeTrackTags(admin, original.ID, "public", 1, []string{source.ID}, nil)
			return err
		}},
		{"dictionary rename", false, func() error {
			name := "compat-public-renamed"
			secrets = append(secrets, name)
			var err error
			source, err = s.UpdateTag(admin, source.ID, TagPatch{Name: &name}, source.Version)
			return err
		}},
		{"public merge", true, func() error {
			_, err := s.MergeTags(admin, source.ID, target.ID, source.Version, target.Version)
			return err
		}},
		{"ungranted public binding", false, func() error {
			_, err := s.ChangeTrackTags(admin, hidden.ID, "public", 1, []string{target.ID}, nil)
			return err
		}},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			if err := step.mutate(); err != nil {
				t.Fatal(err)
			}
			legacyDelta := getSync("1", old.Cursor)
			objectDelta := getSync("2", objects.Cursor)
			if legacyDelta.Reset || objectDelta.Reset || len(legacyDelta.Playlists) != 0 || len(objectDelta.Playlists) != 0 || len(legacyDelta.DeletedTracks) != 0 || len(objectDelta.DeletedTracks) != 0 {
				t.Fatal("tag mutation changed playlist membership or authorization", legacyDelta, objectDelta)
			}
			if step.sharedChange {
				if len(legacyDelta.Changed) != 1 || legacyDelta.Changed[0].ID != original.ID || !slices.Equal(objectDelta.ChangedTrackIDs, []string{original.ID}) {
					t.Fatal("shared tag mutation did not target the granted UUID", legacyDelta, objectDelta)
				}
			} else if len(legacyDelta.Changed) != 0 || len(objectDelta.ChangedTrackIDs) != 0 {
				t.Fatal("private/dictionary/ungranted mutation polluted player delta", legacyDelta, objectDelta)
			}
			old, objects = legacyDelta, objectDelta
			resolved, err := s.ResolveSyncTracks(testPlayerIdentity(t, api, key.Secret), objects.Cursor, []string{original.ID})
			if err != nil || len(resolved.Tracks) != 1 {
				t.Fatal("object resolution no longer works", resolved, err)
			}
			current := resolved.Tracks[0]
			if current.ID != original.ID || current.ContentHash != original.ContentHash || current.Format != original.Format || current.Size != original.Size || current.Genre != original.Genre || current.UploadedBy != original.UploadedBy {
				t.Fatal("tag edit invalidated audio identity or legacy genre", original, current)
			}
			var updated map[string]any
			if err := json.Unmarshal(request("/api/v2/tracks/"+original.ID), &updated); err != nil {
				t.Fatal(err)
			}
			for field := range projection {
				if _, exists := updated[field]; !exists {
					t.Fatalf("old track field disappeared: %s", field)
				}
			}
			if len(updated) != len(projection) || updated["path"] != "" {
				t.Fatal("generic player projection gained fields or exposed path", updated)
			}
			if got := string(request("/api/v2/playlists")); got != playlistBefore {
				t.Fatal("tag edit changed playlist fingerprint/object")
			}
			if got := media(audio.Header().Get("ETag")); got.Code != http.StatusNotModified {
				t.Fatal("tag edit invalidated conditional audio cache", got.Code, got.Body.String())
			}
			if got := media(""); got.Code != 200 || !reflect.DeepEqual(got.Body.Bytes(), audio.Body.Bytes()) {
				t.Fatal("tag edit changed playable audio")
			}
			for _, path := range []string{"/api/v2/tracks/" + hidden.ID, "/api/v2/media/" + hidden.ID} {
				if got := callAPIWithKey(api, "GET", path, "", key.Secret); got.Code != 404 {
					t.Fatal("tag operation widened key grant", path, got.Code)
				}
			}
		})
	}
}
