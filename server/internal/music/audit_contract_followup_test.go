package music

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"musicserver/internal/activity"
)

func TestAuditKnownEndpointMethods(t *testing.T) {
	a, _ := testAPI(t)
	for _, test := range []struct{ path, method, allow string }{
		{"/api/v2/auth/keys", "OPTIONS", "GET, POST, DELETE"},
		{"/api/v2/admin/settings", "OPTIONS", "GET, PUT, PATCH"},
		{"/api/v2/auth/login", "GET", "POST"},
		{"/api/v2/auth/me", "OPTIONS", "GET, PATCH"},
		{"/api/v2/auth/password", "POST", "PUT"},
		{"/api/v2/auth/sessions", "PUT", "GET"},
		{"/api/v2/admin/backups", "OPTIONS", "GET, POST"},
		{"/api/v2/admin/backups/policy", "GET", "PUT"},
		{"/api/v2/admin/backups/jobs/active-job", "GET", "DELETE"},
		{"/api/v2/admin/storage/policy", "POST", "PUT"},
		{"/api/v2/admin/logs", "OPTIONS", "GET"},
		{"/api/v2/admin/registration", "OPTIONS", "GET, PUT"},
		{"/api/v2/registration/config", "POST", "GET"},
	} {
		w := callAPI(a, test.method, test.path, "")
		if w.Code != 405 || w.Header().Get("Allow") != test.allow || !strings.Contains(w.Body.String(), "METHOD_NOT_ALLOWED") {
			t.Errorf("%s %s: %d %s %s", test.method, test.path, w.Code, w.Header().Get("Allow"), w.Body)
		}
	}
}

func TestAuditRevokedDuringMultipartReturnsAuthenticationError(t *testing.T) {
	for _, route := range []string{"/api/v2/uploads", "/api/v2/uploads/stage", "track-lyrics"} {
		t.Run(route, func(t *testing.T) {
			a, _ := testAPI(t)
			journal, err := activity.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			a.store.SetActivity(journal)
			_, session, token := loginTestAccount(t, a.store, "admin")
			track := a.store.Tracks()[0]
			field, name, content := "file", "upload.wav", validTestAudio(t)
			method := "POST"
			if route == "track-lyrics" {
				route = "/api/v2/tracks/" + track.ID + "/lyrics"
				field, name, content = "lyrics_file", "song.lrc", []byte("[00:01]Lyrics")
				method = "PUT"
			}
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			file, err := form.CreateFormFile(field, name)
			if err != nil {
				t.Fatal(err)
			}
			file.Write(content)
			form.Close()
			reader := &resumeReadHook{Reader: bytes.NewReader(body.Bytes()), after: func() {
				if _, err := a.store.db.Exec("UPDATE sessions SET revoked_at='revoked' WHERE id=?", session.ID); err != nil {
					t.Fatal(err)
				}
			}}
			r := httptest.NewRequest(method, "http://example.com"+route, reader)
			r.Header.Set("Content-Type", form.FormDataContentType())
			r.Header.Set("Origin", "http://example.com")
			r.Header.Set("X-CSRF-Token", csrfForToken(token))
			r.Header.Set("If-Match", `"1"`)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != 401 || !strings.Contains(w.Body.String(), "AUTH_INVALID") {
				t.Fatal("late revocation mislabeled as invalid file", w.Code, w.Body)
			}
			for _, entry := range journal.Query(activity.Query{Limit: 100}).Entries {
				if entry.Event == "upload.failed" {
					t.Fatal("authentication denial logged as audio failure")
				}
			}
		})
	}
}

func TestAuditMultipartBodyLimitReturns413(t *testing.T) {
	w := httptest.NewRecorder()
	writeUploadBodyError(w, fmt.Errorf("multipart read: %w", &http.MaxBytesError{Limit: 1024}), "INVALID_UPLOAD", "too large")
	if w.Code != 413 || !strings.Contains(w.Body.String(), "UPLOAD_TOO_LARGE") {
		t.Fatal(w.Code, w.Body)
	}
}

func TestAuditJSONPlaylistConflicts(t *testing.T) {
	a, _ := testAPI(t)
	if err := a.store.PutPlaylist(Playlist{ID: "json-conflict", Name: "before"}); err != nil {
		t.Fatal(err)
	}
	p, _ := a.store.Playlist("json-conflict")
	if err := a.store.PutPlaylist(Playlist{ID: p.ID, Name: "after"}, MutationOptions{ExpectedVersion: p.Version}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		r := httptest.NewRequest(method, "/api/v2/playlists/"+p.ID, strings.NewReader(`{"name":"stale"}`))
		authorizeTestAdmin(a, r)
		r.Header.Set("If-Match", `"1"`)
		w := httptest.NewRecorder()
		auth, ok := a.authenticate(r)
		if !ok {
			t.Fatal("fixture auth")
		}
		r = r.WithContext(withAuth(r.Context(), auth))
		a.playlist(w, r)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "VERSION_CONFLICT") {
			t.Fatal("JSON conflict mapped incorrectly", method, w.Code, w.Body)
		}
	}
}

func TestAuditRevokedSessionCannotCancelBackupOrChangePassword(t *testing.T) {
	a, _ := testAPI(t)
	user, session, _ := loginTestAccount(t, a.store, "admin")
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.store.backupJob = BackupJob{ID: "active-job", State: "running"}
	canceled := false
	a.store.backupCancel = func() { canceled = true }
	if _, err := a.store.db.Exec("UPDATE sessions SET revoked_at='revoked' WHERE id=?", session.ID); err != nil {
		t.Fatal(err)
	}
	options := MutationOptions{UserID: user.ID, ActorType: "session", CredentialID: session.ID}
	if err := a.store.ChangePassword(user, "test administrator password", "another new administrator password", options); err == nil {
		t.Error("revoked session changed password")
	}
	r := httptest.NewRequest("DELETE", "/api/v2/admin/backups/jobs/active-job", nil)
	r = r.WithContext(withAuth(r.Context(), authContext{session: true, userID: user.ID, keyID: session.ID, role: "admin", authVersion: user.AuthVersion}))
	w := httptest.NewRecorder()
	a.backups(w, r)
	if w.Code != 403 || canceled {
		t.Fatal("revoked session canceled backup", w.Code, canceled, w.Body)
	}
}

func TestAuditBackupCancellationRecordsRequest(t *testing.T) {
	a, _ := testAPI(t)
	user, session, _ := loginTestAccount(t, a.store, "admin")
	canceled := false
	a.store.backupJob = BackupJob{ID: "active-job", State: "running"}
	a.store.backupCancel = func() { canceled = true }
	if err := a.store.CancelBackupJob("active-job", MutationOptions{UserID: user.ID, ActorType: "session", CredentialID: session.ID}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.store.db.QueryRow("SELECT count(*) FROM audit WHERE action='backup.cancel_requested' AND entity_id='active-job' AND user_id=?", user.ID).Scan(&count); err != nil || count != 1 || !canceled {
		t.Fatal("cancellation not audited", count, canceled, err)
	}
}

func TestAuditAdminPlaybackReadsAreAudited(t *testing.T) {
	a, _ := testAPI(t)
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	member, _ := readyMember(t, a, "playback-audit")
	if err := a.store.PutPlaylist(Playlist{ID: "private-for-audit", Name: "Private", TrackIDs: []string{a.store.Tracks()[0].ID}}, MutationOptions{UserID: member.ID}); err != nil {
		t.Fatal(err)
	}
	response := browseTestRequest(a, admin, "GET", "/api/v2/browse/playback/private-for-audit?source=own", "", 0)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	var count int
	if err := a.store.db.QueryRow("SELECT count(*) FROM audit WHERE action='playlist.admin.read' AND entity_id='private-for-audit' AND user_id=?", admin.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("playback management read not audited", count, err)
	}
	if response := browseTestRequest(a, member, "GET", "/api/v2/browse/playback/private-for-audit?source=own", "", 0); response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	if err := a.store.db.QueryRow("SELECT count(*) FROM audit WHERE action='playlist.admin.read' AND entity_id='private-for-audit'").Scan(&count); err != nil || count != 1 {
		t.Fatal("own playback incorrectly audited", count, err)
	}
}
