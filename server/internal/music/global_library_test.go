package music

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestMultiuserGlobalLibraryAndOwnerMetadata(t *testing.T) {
	api, _ := testAPI(t)
	alice, aliceToken := readyMember(t, api, "alice")
	bob, bobToken := readyMember(t, api, "bob")
	track := api.store.Tracks()[0]
	track.OwnerID = alice.ID
	if err := api.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v2/tracks", "/api/v2/tracks/" + track.ID, "/api/v2/media/" + track.ID} {
		w := sessionRequest(api, bobToken, "GET", path, "")
		if w.Code != 200 {
			t.Fatalf("global read %s: %d %s", path, w.Code, w.Body)
		}
		if !strings.Contains(path, "/media/") && strings.Contains(w.Body.String(), track.Path) {
			t.Fatalf("filesystem path leaked: %s", w.Body)
		}
	}
	w := memberVersionRequest(api, aliceToken, "PATCH", "/api/v2/tracks/"+track.ID, `{"title":"Alice title"}`, track.Version)
	if w.Code != 200 || strings.Contains(w.Body.String(), track.Path) {
		t.Fatalf("owner metadata: %d %s", w.Code, w.Body)
	}
	var updated Track
	json.Unmarshal(w.Body.Bytes(), &updated)
	if w := memberVersionRequest(api, bobToken, "PATCH", "/api/v2/tracks/"+track.ID, `{"title":"Bob hijack"}`, updated.Version); w.Code != 404 {
		t.Fatalf("foreign metadata: %d", w.Code)
	}
	if _, err := api.store.Scan(); err != nil {
		t.Fatal(err)
	}
	after, _ := api.store.Track(track.ID)
	if after.OwnerID != alice.ID || after.UploadedBy != track.UploadedBy || after.Title != "Alice title" {
		t.Fatalf("scan changed ownership/override: %+v", after)
	}
	alice, _ = readUser(api.store.db, alice.ID)
	if err := api.store.SetUserEnabled(alice.ID, false, alice.Version); err != nil {
		t.Fatal(err)
	}
	if w := sessionRequest(api, bobToken, "GET", "/api/v2/media/"+track.ID, ""); w.Code != 200 {
		t.Fatalf("disabled uploader blocked another account: %d", w.Code)
	}
	if w := sessionRequest(api, bobToken, "GET", "/api/v2/tracks?q=Alice", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Alice title") {
		t.Fatal("global search failed")
	}
	_ = bob
}
