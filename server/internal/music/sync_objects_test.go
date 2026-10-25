package music

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestObjectSyncReorderAndPaginationRevision(t *testing.T) {
	api, _ := testAPI(t)
	s := api.store
	a := s.Tracks()[0].ID
	b, err := s.ImportAudio("Object second.wav", bytes.NewReader(validTestAudio(t)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutPlaylist(Playlist{ID: "order", Name: "Order", TrackIDs: []string{a, b.ID}}); err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "order", AllowedPlaylistIDs: []string{"order"}})
	if err != nil {
		t.Fatal(err)
	}
	auth := testPlayerIdentity(t, api, key.Secret)
	first, err := s.SyncObjectPage(auth, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReorderPlaylistTracks("order", []string{a, b.ID}, []string{b.ID, a}); err != nil {
		t.Fatal(err)
	}
	ordered, err := s.SyncObjectPage(auth, first.Cursor, 100)
	if err != nil || ordered.Reset || len(ordered.ChangedTrackIDs) != 0 || len(ordered.Playlists) != 1 || !reflect.DeepEqual(ordered.Playlists[0].TrackIDs, []string{b.ID, a}) {
		t.Fatalf("%+v %v", ordered, err)
	}
	if _, err := s.UpdatePlaylistTrackNote("order", a, "Private member note"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTrack(a, map[string]string{"title": "Before page"}); err != nil {
		t.Fatal(err)
	}
	partial, err := s.SyncObjectPage(auth, ordered.Cursor, 1)
	if err != nil || !partial.HasMore {
		t.Fatalf("%+v %v", partial, err)
	}
	if _, err := s.ResolveSyncTracks(auth, partial.Cursor, []string{a}); !errors.Is(err, errSyncRestart) {
		t.Fatal("partial cursor batch", err)
	}
	if err := s.UpdateTrack(a, map[string]string{"title": "During page"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncObjectPage(auth, partial.Cursor, 1); !errors.Is(err, errSyncRestart) {
		t.Fatal("mixed delta revisions", err)
	}
	snapshot, err := s.SyncObjectPage(auth, "", 1)
	if err != nil || !snapshot.HasMore {
		t.Fatalf("%+v %v", snapshot, err)
	}
	if err := s.UpdateTrack(a, map[string]string{"title": "During snapshot"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncObjectPage(auth, snapshot.Cursor, 1); !errors.Is(err, errSyncRestart) {
		t.Fatal("mixed snapshot revisions", err)
	}
}

func TestObjectSyncBindingRetainsAuthorizationBoundaries(t *testing.T) {
	auth := authContext{userID: "u", keyID: "k", authVersion: 1, role: "player", allowedPlaylists: map[string]bool{"p": true}, scopes: []string{ScopeLibraryRead}}
	p := Playlist{ID: "p", OwnerID: "u", TrackIDs: []string{"a"}}
	first := syncProtocolBinding(auth, []Playlist{p}, true)
	p.TrackIDs = []string{"b", "a"}
	if syncProtocolBinding(auth, []Playlist{p}, true) != first {
		t.Fatal("ordinary membership changes object binding")
	}
	for _, change := range []func(*Playlist){func(p *Playlist) { p.OwnerID = "other" }, func(p *Playlist) {
		p.ReadOnly = true
		p.SubscriptionStatus = "invalid"
		p.SubscriptionReason = "share_revoked"
	}} {
		changed := clonePlaylist(p)
		change(&changed)
		if syncProtocolBinding(auth, []Playlist{changed}, true) == first {
			t.Fatal("availability or owner boundary lost")
		}
	}
	auth.scopes = []string{ScopeMediaRead}
	if syncProtocolBinding(auth, []Playlist{p}, true) == first {
		t.Fatal("scope boundary lost")
	}
}

func TestObjectSyncMembershipOrderAndMetadata(t *testing.T) {
	api, _ := testAPI(t)
	store := api.store
	id := store.Tracks()[0].ID
	if err := store.PutPlaylist(Playlist{ID: "objects", Name: "Objects", TrackIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(APIKeyCreateRequest{Name: "objects", AllowedPlaylistIDs: []string{"objects"}})
	if err != nil {
		t.Fatal(err)
	}
	auth := testPlayerIdentity(t, api, key.Secret)
	first, err := store.SyncObjectPage(auth, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ObjectDelta || !first.Reset || len(first.Changed) != 0 {
		t.Fatal(first)
	}
	if _, err := store.AddPlaylistTracks("objects", []string{id}); err != nil {
		t.Fatal(err)
	}
	added, err := store.SyncObjectPage(auth, first.Cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if added.Reset || added.FullSnapshot || len(added.Playlists) != 1 || !reflect.DeepEqual(added.Playlists[0].TrackIDs, []string{id}) {
		t.Fatal(added)
	}
	resolved, err := store.ResolveSyncTracks(auth, added.Cursor, []string{id})
	if err != nil || len(resolved.Tracks) != 1 || resolved.Tracks[0].ID != id {
		t.Fatalf("%+v %v", resolved, err)
	}
	if err := store.UpdateTrack(id, map[string]string{"title": "Object metadata"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveSyncTracks(auth, added.Cursor, []string{id}); !errors.Is(err, errSyncRestart) {
		t.Fatalf("stale batch: %v", err)
	}
	updated, err := store.SyncObjectPage(auth, added.Cursor, 100)
	if err != nil || updated.Reset || len(updated.Changed) != 0 || !reflect.DeepEqual(updated.ChangedTrackIDs, []string{id}) {
		t.Fatalf("%+v %v", updated, err)
	}
	resolved, err = store.ResolveSyncTracks(auth, updated.Cursor, []string{id})
	if err != nil || resolved.Tracks[0].Title != "Object metadata" {
		t.Fatalf("%+v %v", resolved, err)
	}
	if err := store.RemovePlaylistTrack("objects", id); err != nil {
		t.Fatal(err)
	}
	removed, err := store.SyncObjectPage(auth, updated.Cursor, 100)
	if err != nil || removed.Reset || len(removed.Playlists) != 1 || len(removed.Playlists[0].TrackIDs) != 0 {
		t.Fatalf("%+v %v", removed, err)
	}
	if _, err := store.ResolveSyncTracks(auth, removed.Cursor, []string{id}); !errors.Is(err, errResourceHidden) {
		t.Fatalf("removed member accessible: %v", err)
	}
}

func TestObjectSyncResolveAuthorizationAndLimits(t *testing.T) {
	api, _ := testAPI(t)
	s := api.store
	id := s.Tracks()[0].ID
	key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "objects", AllowedPlaylistIDs: []string{"library"}})
	if err != nil {
		t.Fatal(err)
	}
	auth := testPlayerIdentity(t, api, key.Secret)
	first, err := s.SyncObjectPage(auth, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveSyncTracks(auth, first.Cursor, []string{id, "hidden"}); !errors.Is(err, errResourceHidden) {
		t.Fatal("mixed unauthorized batch", err)
	}
	if _, err := s.ResolveSyncTracks(auth, first.Cursor, make([]string, 101)); err == nil {
		t.Fatal("unbounded batch")
	}
	empty := []string{}
	if _, err := s.UpdateAPIKey(key.ID, APIKeyUpdateRequest{AllowedPlaylistIDs: &empty}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveSyncTracks(auth, first.Cursor, []string{id}); !errors.Is(err, errSyncRestart) {
		t.Fatal("stale grants batch", err)
	}
	reset, err := s.SyncObjectPage(auth, first.Cursor, 100)
	if err != nil || !reset.Reset || len(reset.ChangedTrackIDs) != 0 || len(reset.Playlists) != 0 {
		t.Fatalf("%+v %v", reset, err)
	}
}

func TestObjectSyncSharingRevocationAndPrivateNotes(t *testing.T) {
	api, a, b, p := sharingFixture(t)
	s := api.store
	i, err := s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	i, err = s.InvitationAction(i.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	key := communityKey(t, api, communityLogin(t, s, b.Username), p.ID)
	auth := testPlayerIdentity(t, api, key)
	first, err := s.SyncObjectPage(auth, "", 100)
	if err != nil || len(first.Playlists) != 1 || len(first.Playlists[0].TrackNotes) != 0 {
		t.Fatalf("private author note exposed: %+v %v", first, err)
	}
	if _, err := s.ResolveSyncTracks(auth, first.Cursor, p.TrackIDs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InvitationAction(i.ID, "revoke", MutationOptions{UserID: a.ID, ExpectedVersion: i.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveSyncTracks(auth, first.Cursor, p.TrackIDs); !errors.Is(err, errSyncRestart) {
		t.Fatal("revoked share old cursor", err)
	}
	revoked, err := s.SyncObjectPage(auth, first.Cursor, 100)
	if err != nil || !revoked.Reset || len(revoked.Playlists) != 1 || revoked.Playlists[0].SubscriptionReason != "share_revoked" || len(revoked.Playlists[0].TrackIDs) != 0 || len(revoked.Playlists[0].TrackNotes) != 0 || len(revoked.ChangedTrackIDs) != 0 {
		t.Fatalf("unsafe revoked projection %+v %v", revoked, err)
	}
	resolved, err := s.ResolveSyncTracks(auth, revoked.Cursor, p.TrackIDs)
	if !errors.Is(err, errResourceHidden) || len(resolved.Tracks) != 0 {
		t.Fatalf("revoked share resolved %+v %v", resolved, err)
	}
}

func TestObjectSyncInFlightKeyRevocationRestartsAuthentication(t *testing.T) {
	api, _ := testAPI(t)
	s := api.store
	key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "in-flight", AllowedPlaylistIDs: []string{"library"}})
	if err != nil {
		t.Fatal(err)
	}
	auth := testPlayerIdentity(t, api, key.Secret)
	first, err := s.SyncObjectPage(auth, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAPIKey(key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveSyncTracks(auth, first.Cursor, []string{s.Tracks()[0].ID}); !errors.Is(err, errSyncAuthChanged) {
		t.Fatal("in-flight key was not sent through fresh authentication", err)
	}
	if _, err := s.SyncObjectPage(auth, first.Cursor, 100); !errors.Is(err, errSyncAuthChanged) {
		t.Fatal("in-flight sync was not sent through fresh authentication", err)
	}
}

func TestObjectSyncRechecksSessionAndScope(t *testing.T) {
	api, _ := testAPI(t)
	s := api.store
	_, _, token, err := s.Login("admin", "test administrator password", "object-session")
	if err != nil {
		t.Fatal(err)
	}
	u, session, ok := s.sessionIdentity(token)
	if !ok {
		t.Fatal("missing session")
	}
	auth := authContext{session: true, userID: u.ID, keyID: session.ID, authVersion: u.AuthVersion, role: u.Role, allowedPlaylists: map[string]bool{"library": true}}
	first, err := s.SyncObjectPage(auth, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveSyncTracks(auth, first.Cursor, []string{s.Tracks()[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(u.ID, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveSyncTracks(auth, first.Cursor, []string{s.Tracks()[0].ID}); !errors.Is(err, errSyncAuthChanged) {
		t.Fatal("revoked in-flight session", err)
	}
	if _, err := s.SyncObjectPage(auth, first.Cursor, 100); !errors.Is(err, errSyncAuthChanged) {
		t.Fatal("revoked session sync", err)
	}
	key, err := s.CreateAPIKey(APIKeyCreateRequest{Name: "scope", AllowedPlaylistIDs: []string{"library"}})
	if err != nil {
		t.Fatal(err)
	}
	auth = testPlayerIdentity(t, api, key.Secret)
	first, err = s.SyncObjectPage(auth, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	scopes := []string{ScopeMediaRead}
	if _, err := s.UpdateAPIKey(key.ID, APIKeyUpdateRequest{Scopes: &scopes}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveSyncTracks(auth, first.Cursor, []string{s.Tracks()[0].ID}); !errors.Is(err, errSyncScopeChanged) {
		t.Fatal("stale in-flight scope", err)
	}
	if _, err := s.SyncObjectPage(auth, first.Cursor, 100); !errors.Is(err, errSyncScopeChanged) {
		t.Fatal("stale sync scope", err)
	}
}
