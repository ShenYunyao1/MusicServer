package music

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// Start with the same normal schema-7 API used by existing users, then migrate
// the database in place. Existing passwords, sessions, grants and media work
// without rescanning or issuing new credentials.
func TestSchemaMigrationExistingAccountsAndPlayerRemainUsable(t *testing.T) {
	api, author, reader, p := sharingFixtureForSchema(t, 7)
	s := api.store
	_, _, authorToken, err := s.Login(author.Username, "personal password for "+author.Username, "migration-author")
	if err != nil {
		t.Fatal(err)
	}
	_, _, readerToken, err := s.Login(reader.Username, "personal password for "+reader.Username, "migration-reader")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := s.CreateInvitation(p.ID, reader.Username, MutationOptions{UserID: author.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.InvitationAction(invite.ID, "accept", MutationOptions{UserID: reader.ID, ExpectedVersion: invite.Version}); err != nil {
		t.Fatal(err)
	}
	sub, err := s.UpdateSubscriptionNote(p.ID, p.TrackIDs[0], "reader private migration note", MutationOptions{UserID: reader.ID, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "preserved player", AllowedPlaylistIDs: []string{p.ID}}, MutationOptions{UserID: reader.ID})
	if err != nil {
		t.Fatal(err)
	}
	audio := callAPIWithKey(api, "GET", "/api/v2/media/"+p.TrackIDs[0], "", key.Secret)
	if audio.Code != 200 {
		t.Fatal(audio.Code, audio.Body)
	}
	oldSync := callAPIWithKey(api, "GET", "/api/v2/sync?protocol=2", "", key.Secret)
	if oldSync.Code != 200 {
		t.Fatal(oldSync.Code, oldSync.Body)
	}
	var sync SyncResponse
	if err = json.Unmarshal(oldSync.Body.Bytes(), &sync); err != nil {
		t.Fatal(err)
	}
	before := migrationSnapshot(t, s.db)
	if err = s.prepareSQLiteMigrationBackup(8); err != nil {
		t.Fatal(err)
	}
	if err = initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, migrationSnapshot(t, s.db)) {
		t.Fatal("migration rewrote old application rows")
	}
	if err = initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{authorToken, readerToken} {
		if r := memberVersionRequest(api, token, "GET", "/api/v2/auth/me", "", 0); r.Code != 200 {
			t.Fatal("old session failed", r.Code, r.Body)
		}
	}
	if _, _, _, err = s.Login(reader.Username, "personal password for "+reader.Username, "migration-relogin"); err != nil {
		t.Fatal("old password failed", err)
	}
	subscriptions, err := s.Subscriptions(reader.ID)
	if err != nil || len(subscriptions) != 1 || subscriptions[0].SubscriptionVersion != sub.SubscriptionVersion || subscriptions[0].TrackNotes[p.TrackIDs[0]] != "reader private migration note" {
		t.Fatal("subscription/note changed", subscriptions, err)
	}
	received, err := s.ReceivedInvitation(invite.ID, reader.ID)
	if err != nil || received.State != "accepted" {
		t.Fatal("invitation changed", received, err)
	}
	afterAudio := callAPIWithKey(api, "GET", "/api/v2/media/"+p.TrackIDs[0], "", key.Secret)
	if afterAudio.Code != 200 || !bytes.Equal(audio.Body.Bytes(), afterAudio.Body.Bytes()) || afterAudio.Header().Get("ETag") != audio.Header().Get("ETag") {
		t.Fatal("old media/key/cache identity failed")
	}
	afterSync := callAPIWithKey(api, "GET", "/api/v2/sync?protocol=2&cursor="+sync.Cursor, "", key.Secret)
	if afterSync.Code != 200 {
		t.Fatal(afterSync.Code, afterSync.Body)
	}
	var delta SyncResponse
	if err = json.Unmarshal(afterSync.Body.Bytes(), &delta); err != nil || delta.Reset || len(delta.ChangedTrackIDs) != 0 || len(delta.Playlists) != 0 {
		t.Fatal("migration reset or changed old sync", delta, err)
	}
	auth := tagMemberAuth(t, s, reader.Username)
	label := mustLinkTag(t, s, auth, "personal", "After migration")
	if _, err = s.ChangeTrackTags(auth, p.TrackIDs[0], "personal", 1, []string{label.ID}, nil); err != nil {
		t.Fatal("new tag API failed", err)
	}
}
