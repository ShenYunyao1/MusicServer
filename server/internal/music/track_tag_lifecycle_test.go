package music

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestTrackTagLifecycleDisableReactivateTransferAndDelete(t *testing.T) {
	api, admin, alice, aliceAuth, _, bobAuth := tagLinkFixture(t)
	s := api.store
	track := s.Tracks()[0]
	track.OwnerID, track.UploadedBy = alice.ID, alice.ID
	if err := s.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	a := mustLinkTag(t, s, aliceAuth, "personal", "alice-only-lifecycle")
	b := mustLinkTag(t, s, bobAuth, "personal", "bob-only-lifecycle")
	public := mustLinkTag(t, s, admin, "public", "shared-lifecycle")
	for _, input := range []struct {
		auth  authContext
		scope string
		tag   Tag
	}{{aliceAuth, "personal", a}, {bobAuth, "personal", b}, {aliceAuth, "public", public}} {
		if _, err := s.ChangeTrackTags(input.auth, track.ID, input.scope, 1, []string{input.tag.ID}, nil); err != nil {
			t.Fatal(err)
		}
	}
	replacement := mustLinkTag(t, s, aliceAuth, "personal", "alice-merged-lifecycle")
	merged, err := s.MergeTags(aliceAuth, a.ID, replacement.ID, a.Version, replacement.Version)
	if err != nil {
		t.Fatal(err)
	}
	a = merged.Target
	for _, userID := range []string{alice.ID, bobAuth.userID} {
		if _, err := s.db.Exec("INSERT INTO saved_track_filters(id,user_id,name,query_json,version,created_at,updated_at) VALUES(?,?,?,'{}',1,'created','updated')", uuid.NewString(), userID, "private filter"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetUserEnabled(alice.ID, false, alice.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTrackTags(aliceAuth, track.ID); !errors.Is(err, errTagSession) {
		t.Fatal("disabled account retained access", err)
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM tags WHERE scope_id=?", alice.ID).Scan(&count)
	if count != 2 {
		t.Fatal("disable deleted personal dictionary")
	}
	alice, _ = readUser(s.db, alice.ID)
	if err := s.SetUserEnabled(alice.ID, true, alice.Version); err != nil {
		t.Fatal(err)
	}
	aliceAuth = tagMemberAuth(t, s, alice.Username)
	view, err := s.GetTrackTags(aliceAuth, track.ID)
	if err != nil || len(view.Personal) != 1 || view.Personal[0].ID != a.ID {
		t.Fatal("reactivation did not retain personal tags", view, err)
	}
	alice, _ = readUser(s.db, alice.ID)
	if err = s.SetUserEnabled(alice.ID, false, alice.Version); err != nil {
		t.Fatal(err)
	}
	alice, _ = readUser(s.db, alice.ID)
	adminUser, _ := readUser(s.db, admin.userID)
	current, _ := s.Track(track.ID)
	if err = s.TransferResources(alice.ID, TransferRequest{TargetID: adminUser.ID, TargetVersion: adminUser.Version, All: true, Tracks: []ResourceVersion{{ID: track.ID, Version: current.Version}}}, MutationOptions{UserID: adminUser.ID, ExpectedVersion: alice.Version}); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Track(track.ID)
	if current.OwnerID != adminUser.ID || current.UploadedBy != alice.ID {
		t.Fatal("takeover changed provenance", current)
	}
	adminView, err := s.GetTrackTags(admin, track.ID)
	if err != nil || len(adminView.Personal) != 0 || len(adminView.Public) != 1 {
		t.Fatal("takeover moved private tags", adminView, err)
	}
	if err = s.DeleteUser(alice.ID, MutationOptions{UserID: adminUser.ID, ExpectedVersion: alice.Version}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"SELECT count(*) FROM tag_scopes WHERE user_id=?", "SELECT count(*) FROM tags WHERE scope_id=?", "SELECT count(*) FROM track_tags WHERE scope_id=?", "SELECT count(*) FROM track_tag_states WHERE scope_id=?", "SELECT count(*) FROM saved_track_filters WHERE user_id=?"} {
		if err = s.db.QueryRow(query, alice.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("deleted account private data retained", query, count, err)
		}
	}
	bobView, err := s.GetTrackTags(bobAuth, track.ID)
	if err != nil || len(bobView.Personal) != 1 || bobView.Personal[0].ID != b.ID || len(bobView.Public) != 1 {
		t.Fatal("delete damaged another scope", bobView, err)
	}
	if err = s.db.QueryRow("SELECT count(*) FROM saved_track_filters WHERE user_id=?", bobAuth.userID).Scan(&count); err != nil || count != 1 {
		t.Fatal("another user's filter was deleted", count, err)
	}
	var missingFK bool
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	missingFK = rows.Next()
	rows.Close()
	if missingFK {
		t.Fatal("delete left foreign-key violations")
	}
}
