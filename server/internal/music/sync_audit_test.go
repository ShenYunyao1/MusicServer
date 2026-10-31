package music

import (
	"reflect"
	"strings"
	"testing"
)

func TestAuditSyncOwnedPlaylistDeletionDelta(t *testing.T) {
	for _, objects := range []bool{false, true} {
		t.Run(map[bool]string{false: "classic", true: "objects"}[objects], func(t *testing.T) {
			a, _ := testAPI(t)
			s := a.store
			for _, id := range []string{"keep-sync", "remove-sync"} {
				if err := s.PutPlaylist(Playlist{ID: id, Name: id, TrackIDs: []string{s.Tracks()[0].ID}}); err != nil {
					t.Fatal(err)
				}
			}
			key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "deletion", AllowedPlaylistIDs: []string{"library", "keep-sync", "remove-sync"}})
			if err != nil {
				t.Fatal(err)
			}
			auth := testPlayerIdentity(t, a, key.Secret)
			first, err := s.syncPage(auth, "", 500, objects)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.DeletePlaylist("remove-sync"); err != nil {
				t.Fatal(err)
			}
			delta, err := s.syncPage(testPlayerIdentity(t, a, key.Secret), first.Cursor, 500, objects)
			if err != nil || delta.Reset || delta.FullSnapshot || !reflect.DeepEqual(delta.DeletedPlaylists, []string{"remove-sync"}) {
				t.Fatalf("expected actual deletion delta: %+v %v", delta, err)
			}
			if len(delta.Changed) != 0 || len(delta.ChangedTrackIDs) != 0 {
				t.Fatal("unchanged songs retransferred", delta)
			}
			if objects {
				if _, err := s.ResolveSyncTracks(testPlayerIdentity(t, a, key.Secret), delta.Cursor, []string{s.Tracks()[0].ID}); err != nil {
					t.Fatal("updated cursor cannot resolve", err)
				}
			}
		})
	}
}

func TestAuditSyncPlaylistDeletionPrivacyAndGrantReset(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	for _, id := range []string{"authorized-sync", "ungranted-private-sync"} {
		if err := s.PutPlaylist(Playlist{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "private", AllowedPlaylistIDs: []string{"authorized-sync"}})
	if err != nil {
		t.Fatal(err)
	}
	auth := testPlayerIdentity(t, a, key.Secret)
	first, err := s.SyncObjectPage(auth, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlaylist("ungranted-private-sync"); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.SyncObjectPage(auth, first.Cursor, 500)
	if err != nil || len(unchanged.DeletedPlaylists) != 0 {
		t.Fatal("private deleted UUID leaked", unchanged, err)
	}
	empty := []string{}
	if _, err := s.UpdateAPIKey(key.ID, APIKeyUpdateRequest{AllowedPlaylistIDs: &empty}); err != nil {
		t.Fatal(err)
	}
	reset, err := s.SyncObjectPage(testPlayerIdentity(t, a, key.Secret), unchanged.Cursor, 500)
	if err != nil || !reset.Reset || len(reset.DeletedPlaylists) != 0 {
		t.Fatal("ordinary grant revoke must reset", reset, err)
	}
}

func TestAuditSyncLegacyCursorAndProofBound(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	for index := 0; index < 35; index++ {
		id := strings.Repeat("x", index+1)
		if err := s.PutPlaylist(Playlist{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	auth := tagTestAuth(t, s, "admin")
	first, err := s.SyncPage(auth, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Cursor) > 16384 {
		t.Fatal("unbounded cursor", len(first.Cursor))
	}
	var secret string
	s.db.QueryRow("SELECT value FROM meta WHERE key='sync_secret'").Scan(&secret)
	legacy, err := parseCursor(first.Cursor, secret)
	if err != nil {
		t.Fatal(err)
	}
	legacy.OwnedPlaylists = nil
	legacy.StableBinding = ""
	legacy.TrackBinding = ""
	if err := s.DeletePlaylist("x"); err != nil {
		t.Fatal(err)
	}
	reset, err := s.SyncPage(auth, signCursor(legacy, secret), 500)
	if err != nil || !reset.Reset {
		t.Fatal("legacy cursor must remain reset-compatible", reset, err)
	}
}

func TestAuditSyncSessionDeletionAndClassicVisibilityReset(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	track := s.Tracks()[0]
	if err := s.PutPlaylist(Playlist{ID: "sole-sync-list", Name: "Sole", TrackIDs: []string{track.ID}}); err != nil {
		t.Fatal(err)
	}
	session := tagTestAuth(t, s, "admin")
	sessionPage, err := s.SyncPage(session, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "sole", AllowedPlaylistIDs: []string{"sole-sync-list"}})
	if err != nil {
		t.Fatal(err)
	}
	classic, err := s.SyncPage(testPlayerIdentity(t, a, key.Secret), "", 500)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := s.SyncObjectPage(testPlayerIdentity(t, a, key.Secret), "", 500)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlaylist("sole-sync-list"); err != nil {
		t.Fatal(err)
	}
	delta, err := s.SyncPage(session, sessionPage.Cursor, 500)
	if err != nil || delta.Reset || !reflect.DeepEqual(delta.DeletedPlaylists, []string{"sole-sync-list"}) {
		t.Fatal("session deletion not incremental", delta, err)
	}
	reset, err := s.SyncPage(testPlayerIdentity(t, a, key.Secret), classic.Cursor, 500)
	if err != nil || !reset.Reset || len(reset.Changed) != 0 {
		t.Fatal("classic track visibility reduction must reset", reset, err)
	}
	delta, err = s.SyncObjectPage(testPlayerIdentity(t, a, key.Secret), objects.Cursor, 500)
	if err != nil || delta.Reset || !reflect.DeepEqual(delta.DeletedPlaylists, []string{"sole-sync-list"}) {
		t.Fatal("object deletion should be incremental", delta, err)
	}
	if _, err := s.ResolveSyncTracks(testPlayerIdentity(t, a, key.Secret), delta.Cursor, []string{track.ID}); err == nil {
		t.Fatal("deleted list retained song authorization")
	}
}

func TestAuditSyncProofBoundUsesConservativeReset(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	auth := tagTestAuth(t, s, "admin")
	for index := 0; index < syncKnownPlaylistLimit+1; index++ {
		id := strings.Repeat("p", index+1)
		if err := s.PutPlaylist(Playlist{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.SyncObjectPage(auth, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	var secret string
	s.db.QueryRow("SELECT value FROM meta WHERE key='sync_secret'").Scan(&secret)
	cursor, err := parseCursor(first.Cursor, secret)
	if err != nil || cursor.StableBinding != "" || len(cursor.OwnedPlaylists) > 0 {
		t.Fatal("large owned list should omit proof", cursor, err)
	}
	if err := s.DeletePlaylist("p"); err != nil {
		t.Fatal(err)
	}
	reset, err := s.SyncObjectPage(auth, first.Cursor, 500)
	if err != nil || !reset.Reset {
		t.Fatal("proof limit fallback must reset", reset, err)
	}
	if _, err := parseCursor(strings.Repeat("x", 16385), secret); err == nil {
		t.Fatal("oversized cursor accepted")
	}
}
