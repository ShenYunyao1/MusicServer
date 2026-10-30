package music

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPermanentTrackDeletion(t *testing.T) {
	a, _ := testAPI(t)
	track := a.store.Tracks()[0]
	path, _ := a.store.MediaPath(track)
	endpoint := "/api/v2/admin/removed/" + track.ID
	if r := adminVersionRequest(a, "DELETE", endpoint, "", track.Version); r.Code < 400 {
		t.Fatal("active track deleted")
	}
	if err := a.store.PutPlaylist(Playlist{ID: "purge-list", Name: "keep", TrackIDs: []string{track.ID}, TrackNotes: map[string]string{track.ID: "note"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeleteTrack(track.ID); err != nil {
		t.Fatal(err)
	}
	track = a.store.RemovedTracks()[0]
	if r := adminVersionRequest(a, "DELETE", endpoint, "", track.Version-1); r.Code != 409 {
		t.Fatal("stale", r.Code, r.Body)
	}
	if r := adminVersionRequest(a, "DELETE", endpoint, "", track.Version); r.Code != 204 {
		t.Fatal(r.Code, r.Body)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("audio retained", err)
	}
	if _, err := readTrackFrom(a.store.db, track.ID, true); err != sql.ErrNoRows {
		t.Fatal("track retained", err)
	}
	p, _ := a.store.Playlist("purge-list")
	if len(p.TrackIDs) != 0 || len(p.TrackNotes) != 0 {
		t.Fatal("playlist references retained", p)
	}
	if r := adminVersionRequest(a, "POST", endpoint, "", track.Version); r.Code < 400 {
		t.Fatal("purged track restored")
	}
	assertPurgeForeignKeys(t, a.store)
}

func TestPermanentDeletionRollbackAndRecovery(t *testing.T) {
	a, _ := testAPI(t)
	track := a.store.Tracks()[0]
	a.store.DeleteTrack(track.ID)
	track = a.store.RemovedTracks()[0]
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	opt := MutationOptions{UserID: admin.ID, ExpectedVersion: track.Version}
	path, _ := a.store.MediaPath(track)
	a.store.db.Exec("CREATE TRIGGER reject_purge BEFORE DELETE ON tracks BEGIN SELECT RAISE(ABORT,'purge failure'); END")
	if err := a.store.PurgeTrack(track.ID, opt); err == nil {
		t.Fatal("failure ignored")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("rollback lost audio", err)
	}
	a.store.db.Exec("DROP TRIGGER reject_purge")
	trash := filepath.Join(a.store.mediaRoot, ".staging", "purge-"+track.ID)
	os.MkdirAll(filepath.Dir(trash), 0700)
	if err := os.Rename(path, trash); err != nil {
		t.Fatal(err)
	}
	if err := a.store.recoverPurgedMedia(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("recovery lost audio", err)
	}
	if err := a.store.PurgeTrack(track.ID, opt); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trash, []byte("pending cleanup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.store.recoverPurgedMedia(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(trash); !os.IsNotExist(err) {
		t.Fatal("pending cleanup retained")
	}
}

func TestPermanentAccountDeletion(t *testing.T) {
	a, _ := testAPI(t)
	member, _ := readyMember(t, a, "PurgeEmpty")
	endpoint := "/api/v2/admin/users/" + member.ID + "/purge"
	if r := adminVersionRequest(a, "DELETE", endpoint, "", member.Version); r.Code != 409 {
		t.Fatal("enabled", r.Code)
	}
	a.store.SetUserEnabled(member.ID, false, member.Version)
	member, _ = readUser(a.store.db, member.ID)
	if r := adminVersionRequest(a, "DELETE", endpoint, "", member.Version-1); r.Code != 409 {
		t.Fatal("stale", r.Code)
	}
	if r := adminVersionRequest(a, "DELETE", endpoint, "", member.Version); r.Code != 204 {
		t.Fatal(r.Code, r.Body)
	}
	if _, err := readUser(a.store.db, member.ID); err != sql.ErrNoRows {
		t.Fatal("identity retained", err)
	}
	if r := callAPI(a, "POST", "/api/v2/admin/users", `{"username":"PurgeEmpty","quota_bytes":0}`); r.Code != 201 {
		t.Fatal("login reserved", r.Code, r.Body)
	}
	assertPurgeForeignKeys(t, a.store)
}

func TestPermanentAccountUploaderGuardAndTombstone(t *testing.T) {
	a, _ := testAPI(t)
	member, _ := readyMember(t, a, "PurgeUploader")
	track := a.store.Tracks()[0]
	a.store.transaction(func(tx *sql.Tx) error { track.UploadedBy = member.ID; return writeTrack(tx, track) })
	a.store.SetUserEnabled(member.ID, false, member.Version)
	member, _ = readUser(a.store.db, member.ID)
	endpoint := "/api/v2/admin/users/" + member.ID + "/purge"
	if r := adminVersionRequest(a, "DELETE", endpoint, "", member.Version); r.Code != 409 || !strings.Contains(r.Body.String(), "ACCOUNT_HAS_RESOURCES") {
		t.Fatal(r.Code, r.Body)
	}
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	if err := a.store.DeleteUser(member.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: member.Version}); err != nil {
		t.Fatal(err)
	}
	member, _ = readUser(a.store.db, member.ID)
	a.store.DeleteTrack(track.ID)
	track = a.store.RemovedTracks()[0]
	if err := a.store.PurgeTrack(track.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: track.Version}); err != nil {
		t.Fatal(err)
	}
	if r := adminVersionRequest(a, "DELETE", endpoint, "", member.Version); r.Code != 204 {
		t.Fatal(r.Code, r.Body)
	}
	assertPurgeForeignKeys(t, a.store)
}

func assertPurgeForeignKeys(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("broken foreign keys")
	}
}

func TestPermanentDeletionPermissionsAndAccountRollback(t *testing.T) {
	a, _ := testAPI(t)
	u, token := readyMember(t, a, "purge-security")
	track := a.store.Tracks()[0]
	for _, path := range []string{"/api/v2/admin/removed/" + track.ID, "/api/v2/admin/users/" + u.ID + "/purge"} {
		if r := memberVersionRequest(a, token, "DELETE", path, "", u.Version); r.Code != 403 {
			t.Fatal("member", r.Code, r.Body)
		}
	}
	admin, _, token := loginTestAccount(t, a.store, "admin")
	if r := memberVersionRequest(a, token, "DELETE", "/api/v2/admin/users/"+admin.ID+"/purge", "", admin.Version); r.Code != 409 {
		t.Fatal("self", r.Code)
	}
	a.store.SetUserEnabled(u.ID, false, u.Version)
	u, _ = readUser(a.store.db, u.ID)
	a.store.db.Exec("INSERT INTO meta(key,value) VALUES(?,?)", "email_binding/fixture", u.ID)
	a.store.db.Exec("CREATE TRIGGER reject_user_purge BEFORE DELETE ON users BEGIN SELECT RAISE(ABORT,'purge failure'); END")
	if err := a.store.PurgeUser(u.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: u.Version}); err == nil {
		t.Fatal("failure ignored")
	}
	var owner string
	if err := a.store.db.QueryRow("SELECT value FROM meta WHERE key='email_binding/fixture'").Scan(&owner); err != nil || owner != u.ID {
		t.Fatal("failed purge released email", err)
	}
	a.store.db.Exec("DROP TRIGGER reject_user_purge")
	if err := a.store.PurgeUser(u.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: u.Version}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.db.QueryRow("SELECT value FROM meta WHERE key='email_binding/fixture'").Scan(&owner); err != sql.ErrNoRows {
		t.Fatal("email retained", err)
	}
}

func TestPermanentAccountPreservesPublicTagContributions(t *testing.T) {
	a, _ := tagHTTPAPI(t)
	u, _ := readyMember(t, a, "purge-tagger")
	auth := tagMemberAuth(t, a.store, u.Username)
	track := a.store.Tracks()[0]
	tag := mustLinkTag(t, a.store, auth, "public", "Preserve contribution")
	// Use a real shared tag assignment so author foreign keys are exercised.
	if _, err := a.store.db.Exec("INSERT INTO track_tags(track_id,tag_id,scope_id,source,created_by,created_at) VALUES(?,?, 'public','manual',?,?)", track.ID, tag.ID, u.ID, "2026-10-30T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	a.store.SetUserEnabled(u.ID, false, u.Version)
	u, _ = readUser(a.store.db, u.ID)
	if err := a.store.PurgeUser(u.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: u.Version}); err != nil {
		t.Fatal(err)
	}
	var by string
	if err := a.store.db.QueryRow("SELECT created_by FROM track_tags WHERE track_id=? AND tag_id=?", track.ID, tag.ID).Scan(&by); err != nil || by != admin.ID {
		t.Fatal("contribution lost", by, err)
	}
	assertPurgeForeignKeys(t, a.store)
}

func TestPermanentAccountAssetGuards(t *testing.T) {
	a, _ := testAPI(t)
	u, _ := readyMember(t, a, "purge-assets")
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	if err := a.store.PutPlaylist(Playlist{ID: "guard-purge", Name: "asset"}, MutationOptions{UserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	a.store.SetUserEnabled(u.ID, false, u.Version)
	u, _ = readUser(a.store.db, u.ID)
	opt := MutationOptions{UserID: admin.ID, ExpectedVersion: u.Version}
	if err := a.store.PurgeUser(u.ID, opt); err != errAccountHasResources {
		t.Fatal("playlist not guarded", err)
	}
	a.store.db.Exec("DELETE FROM playlists WHERE id='guard-purge'")
	// An active chunk task blocks deletion; a canceled checkpoint is not an asset.
	a.store.db.Exec("INSERT INTO meta(key,value) VALUES('upload_resume/fixture',json_object('owner',?,'state','uploading'))", u.ID)
	if err := a.store.PurgeUser(u.ID, opt); err != errAccountHasResources {
		t.Fatal("resume not guarded", err)
	}
	a.store.db.Exec("UPDATE meta SET value=json_set(value,'$.state','canceled') WHERE key='upload_resume/fixture'")
	if err := a.store.PurgeUser(u.ID, opt); err != nil {
		t.Fatal("canceled checkpoint blocked deletion", err)
	}

	v, _ := readyMember(t, a, "purge-stage")
	if _, err := a.store.StageAudio("pending.wav", bytes.NewReader(validTestAudio(t)), nil, MutationOptions{UserID: v.ID}); err != nil {
		t.Fatal(err)
	}
	a.store.SetUserEnabled(v.ID, false, v.Version)
	v, _ = readUser(a.store.db, v.ID)
	if err := a.store.PurgeUser(v.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: v.Version}); err != errAccountHasResources {
		t.Fatal("staged upload not guarded", err)
	}
}

func TestPermanentTrackRejectsUnsafePath(t *testing.T) {
	a, _ := testAPI(t)
	track := a.store.Tracks()[0]
	path, _ := a.store.MediaPath(track)
	a.store.DeleteTrack(track.ID)
	track = a.store.RemovedTracks()[0]
	track.Path = "../outside.wav"
	if err := a.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	if err := a.store.PurgeTrack(track.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: track.Version}); err == nil {
		t.Fatal("unsafe path accepted")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("audio changed", err)
	}
	if _, err := readTrackFrom(a.store.db, track.ID, true); err != nil {
		t.Fatal("record lost", err)
	}
}
