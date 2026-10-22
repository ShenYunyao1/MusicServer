package music

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, _ := form.CreateFormFile("lyrics_file", "test.lrc")
	file.Write([]byte("[00:01]private upload"))
	form.Close()
	r := httptest.NewRequest("PUT", "http://example.com/api/v2/tracks/"+track.ID+"/lyrics", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(aliceToken))
	r.Header.Set("If-Match", fmt.Sprint(updated.Version))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: aliceToken})
	coverResponse := httptest.NewRecorder()
	api.ServeHTTP(coverResponse, r)
	if coverResponse.Code != 200 || strings.Contains(coverResponse.Body.String(), track.Path) {
		t.Fatal("attachment response path", coverResponse.Code, coverResponse.Body.String())
	}
	updated, _ = api.store.Track(track.ID)
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
