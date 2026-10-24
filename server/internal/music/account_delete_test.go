package music

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccountDeletionPendingUploadCannotCommitWithoutActor(t *testing.T) {
	api, _ := testAPI(t)
	admin, _, _ := loginTestAccount(t, api.store, "admin")
	member, _ := readyMember(t, api, "deleted-stage-owner")
	preview, err := api.store.StageAudio("pending.wav", bytes.NewReader(validTestAudio(t)), nil, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.store.SetUserEnabled(member.ID, false, member.Version); err != nil {
		t.Fatal(err)
	}
	member, _ = readUser(api.store.db, member.ID)
	if err := api.store.DeleteUser(member.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: member.Version}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.store.CommitUploadStage(preview.ID); err == nil {
		t.Fatal("internal upload commit assigned resources to a deleted owner")
	}
}

func TestAccountDeletionLifecycleAndReferences(t *testing.T) {
	api, author, member, source := communityFixture(t)
	admin, _, token := loginTestAccount(t, api.store, "admin")
	path := "/api/v2/admin/users/" + member.ID
	if r := sessionRequest(api, token, "DELETE", path, ""); r.Code != 428 {
		t.Fatalf("missing version: %d", r.Code)
	}
	if r := memberVersionRequest(api, token, "DELETE", path, "", member.Version); r.Code != 409 {
		t.Fatalf("enabled account deleted: %d", r.Code)
	}
	if r := memberVersionRequest(api, token, "DELETE", "/api/v2/admin/users/"+admin.ID, "", admin.Version); r.Code != 409 {
		t.Fatalf("self deletion allowed: %d", r.Code)
	}
	memberToken := communityLogin(t, api.store, member.Username)
	if r := memberVersionRequest(api, memberToken, "DELETE", path, "", member.Version); r.Code != 403 {
		t.Fatalf("member deleted an account: %d", r.Code)
	}
	request := httptest.NewRequest("DELETE", "http://example.com"+path, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatalf("deletion bypassed CSRF: %d", response.Code)
	}
	track := api.store.Tracks()[0]
	track.OwnerID, track.UploadedBy = member.ID, member.ID
	if err := api.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	if err := api.store.PutPlaylist(Playlist{ID: "delete-owned", Name: "Keep this playlist", TrackIDs: []string{track.ID}}, MutationOptions{UserID: member.ID}); err != nil {
		t.Fatal(err)
	}
	owned, _ := api.store.Playlist("delete-owned")
	source, err := api.store.SetPlaylistVisibility(source.ID, "shared", MutationOptions{UserID: author.ID, ExpectedVersion: source.Version})
	if err != nil {
		t.Fatal(err)
	}
	invite, err := api.store.CreateInvitation(source.ID, member.Username, MutationOptions{UserID: author.ID, ExpectedVersion: source.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.InvitationAction(invite.ID, "accept", MutationOptions{UserID: member.ID, ExpectedVersion: invite.Version}); err != nil {
		t.Fatal(err)
	}
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "Remove with account", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	api.recordCall(key.ID, 200, 1, time.Millisecond)
	if response := callAPIWithKey(api, "DELETE", path, "", key.Secret); response.Code != 403 {
		t.Fatalf("key deleted an account: %d", response.Code)
	}
	if err := api.store.SetUserEnabled(member.ID, false, member.Version); err != nil {
		t.Fatal(err)
	}
	member, _ = readUser(api.store.db, member.ID)
	if r := memberVersionRequest(api, token, "DELETE", path, "", member.Version-1); r.Code != 409 {
		t.Fatalf("stale deletion: %d", r.Code)
	}
	if r := memberVersionRequest(api, token, "DELETE", path, "", member.Version); r.Code != 409 || !strings.Contains(r.Body.String(), "ACCOUNT_HAS_RESOURCES") {
		t.Fatalf("owned resources were not protected: %d %s", r.Code, r.Body)
	}
	if err := api.store.TransferResources(member.ID, TransferRequest{TargetID: admin.ID, TargetVersion: admin.Version, All: true, Tracks: []ResourceVersion{{track.ID, track.Version}}, Playlists: []ResourceVersion{{owned.ID, owned.Version}}}, MutationOptions{UserID: admin.ID, ExpectedVersion: member.Version}); err != nil {
		t.Fatal(err)
	}
	if r := memberVersionRequest(api, token, "DELETE", path, "", member.Version); r.Code != 204 {
		t.Fatalf("delete empty disabled account: %d %s", r.Code, r.Body)
	}
	assertKeyRowsDeleted(t, api.store, key.ID)
	for _, query := range []string{"SELECT count(*) FROM sessions WHERE user_id=?", "SELECT count(*) FROM subscriptions WHERE user_id=?", "SELECT count(*) FROM playlist_invitations WHERE recipient_id=?"} {
		var count int
		if err := api.store.db.QueryRow(query, member.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("account rows remain: %s: %d %v", query, count, err)
		}
	}
	users, err := api.store.Users()
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.ID == member.ID {
			t.Fatal("deleted account remains in normal list")
		}
	}
	retained, err := readUser(api.store.db, member.ID)
	if err != nil || retained.Enabled || retained.Username != member.Username {
		t.Fatalf("deleted account identity was not retained: %v", err)
	}
	if r := memberVersionRequest(api, token, "PATCH", path, `{"enabled":true}`, retained.Version); r.Code != 404 {
		t.Fatalf("deleted account could be re-enabled: %d", r.Code)
	}
	if r := memberVersionRequest(api, token, "POST", path+"/reset-password", `{}`, retained.Version); r.Code != 404 {
		t.Fatalf("deleted password could be reset: %d", r.Code)
	}
	if err := api.store.ResetPassword(member.Username, "replacement deleted password", false); err == nil {
		t.Fatal("offline reset restored a deleted account")
	}
	if r := sessionRequest(api, memberToken, "GET", "/api/v2/auth/me", ""); r.Code != 401 {
		t.Fatalf("deleted session authenticated: %d", r.Code)
	}
	if r := callAPIWithKey(api, "GET", "/api/v2/playlists", "", key.Secret); r.Code != 401 {
		t.Fatalf("deleted key authenticated: %d", r.Code)
	}
	if r := callAPI(api, "POST", "/api/v2/admin/users", `{"username":"PUBLIC-READER"}`); r.Code != 409 {
		t.Fatalf("deleted login name reused: %d", r.Code)
	}
	keptTrack, ok := api.store.Track(track.ID)
	keptPlaylist, exists := api.store.Playlist(owned.ID)
	if !ok || !exists || keptTrack.OwnerID != admin.ID || keptTrack.UploadedBy != member.ID || keptPlaylist.OwnerID != admin.ID {
		t.Fatal("account deletion changed transferred resources or uploader")
	}
	reopened, err := NewStore(api.store.DataPath(), api.store.mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var deleted string
	if err := reopened.db.QueryRow("SELECT value FROM meta WHERE key=?", "deleted_user/"+member.ID).Scan(&deleted); err != nil || deleted == "" {
		t.Fatalf("deletion marker did not survive reopening: %v", err)
	}
	var version int
	if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatal("deletion changed the database schema")
	}
	rows, err := reopened.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("account deletion broke foreign keys")
	}
}

func TestAccountDeletionReassignsFixedScanOwner(t *testing.T) {
	api, _ := testAPI(t)
	old, _, _ := loginTestAccount(t, api.store, "admin")
	actor, err := api.store.createUser("OtherAdmin", "another administrator password", "admin", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.store.SetUserEnabled(old.ID, false, old.Version); err != nil {
		t.Fatal(err)
	}
	old, _ = readUser(api.store.db, old.ID)
	track := api.store.Tracks()[0]
	if err := api.store.TransferResources(old.ID, TransferRequest{TargetID: actor.ID, TargetVersion: actor.Version, All: true, Tracks: []ResourceVersion{{track.ID, track.Version}}}, MutationOptions{UserID: actor.ID, ExpectedVersion: old.Version}); err != nil {
		t.Fatal(err)
	}
	if err := api.store.DeleteUser(old.ID, MutationOptions{UserID: actor.ID, ExpectedVersion: old.Version}); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := api.store.db.QueryRow("SELECT value FROM meta WHERE key='scan_owner_id'").Scan(&owner); err != nil || owner != actor.ID {
		t.Fatalf("fixed scanner retained a deleted account: %v", err)
	}
	if err := api.store.RequireAdministrator(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountDeletionCleanupFailureRollsBack(t *testing.T) {
	api, _ := testAPI(t)
	member, _ := readyMember(t, api, "delete-rollback")
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "Retain on failure", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	api.recordCall(key.ID, 200, 1, time.Millisecond)
	if err := api.store.SetUserEnabled(member.ID, false, member.Version); err != nil {
		t.Fatal(err)
	}
	member, _ = readUser(api.store.db, member.ID)
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_deleted_marker BEFORE INSERT ON meta WHEN NEW.key LIKE 'deleted_user/%' BEGIN SELECT RAISE(ABORT,'test deletion failure'); END"); err != nil {
		t.Fatal(err)
	}
	if r := adminVersionRequest(api, "DELETE", "/api/v2/admin/users/"+member.ID, "", member.Version); r.Code < 400 || !strings.Contains(r.Body.String(), "test deletion failure") {
		t.Fatalf("failure ignored: %d", r.Code)
	}
	var count int
	if err := api.store.db.QueryRow("SELECT count(*) FROM api_keys WHERE id=?", key.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed deletion lost key")
	}
	if err := api.store.db.QueryRow("SELECT count(*) FROM api_call_stats WHERE key_id=? AND total_requests=1", key.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed deletion lost statistics")
	}
	retained, err := readUser(api.store.db, member.ID)
	if err != nil || retained.Version != member.Version {
		t.Fatal("failed deletion changed account")
	}
}
