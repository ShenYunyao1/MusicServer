package music

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func testPlayerIdentity(t *testing.T, api *API, secret string) authContext {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/v2/sync", nil)
	r.Header.Set("X-API-Key", secret)
	auth, ok := api.authenticate(r)
	if !ok {
		t.Fatal("key authentication failed")
	}
	return auth
}

func testSyncIdentity(store *Store) authContext {
	var id string
	_ = store.db.QueryRow("SELECT id FROM users WHERE role='admin' ORDER BY id LIMIT 1").Scan(&id)
	return authContext{role: "admin", session: true, userID: id, keyID: "test-admin"}
}

func syncSession(t *testing.T, api *API, token, cursor string) SyncResponse {
	t.Helper()
	r := memberVersionRequest(api, token, "GET", "/api/v2/sync?limit=500&cursor="+cursor, "", 0)
	if r.Code != 200 {
		t.Fatal("sync", r.Code, r.Body.String())
	}
	var result SyncResponse
	json.Unmarshal(r.Body.Bytes(), &result)
	return result
}

func TestMultiuserSyncPrivateOwnershipAndUnavailableMembers(t *testing.T) {
	api, _ := testAPI(t)
	a, at := readyMember(t, api, "sync-a")
	b, bt := readyMember(t, api, "sync-b")
	track := api.store.Tracks()[0]
	api.store.PutPlaylist(Playlist{ID: "sync-private-a", Name: "secret A", TrackIDs: []string{track.ID}, TrackNotes: map[string]string{track.ID: "private note"}}, MutationOptions{UserID: a.ID})
	api.store.PutPlaylist(Playlist{ID: "empty-transfer", Name: "empty private"}, MutationOptions{UserID: a.ID})
	first := syncSession(t, api, bt, "")
	if len(first.Changed) == 0 {
		t.Fatal("global tracks absent")
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "secret A") || strings.Contains(string(encoded), "private note") {
		t.Fatal("private snapshot leak")
	}
	initial := syncSession(t, api, at, "")
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "empty grant", AllowedPlaylistIDs: []string{"empty-transfer"}}, MutationOptions{UserID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	keySnapshot := callAPIWithKey(api, "GET", "/api/v2/sync", "", key.Secret)
	var oldKeySync SyncResponse
	json.Unmarshal(keySnapshot.Body.Bytes(), &oldKeySync)
	if err := api.store.DeleteTrack(track.ID); err != nil {
		t.Fatal(err)
	}
	down := syncSession(t, api, at, initial.Cursor)
	found := false
	for _, p := range down.Playlists {
		if p.ID == "sync-private-a" {
			found = true
			if len(p.TrackIDs) != 1 || p.TrackNotes[track.ID] != "private note" || len(p.UnavailableTrackIDs) != 1 {
				t.Fatal("sync erased unavailable member", p)
			}
		}
	}
	if !found {
		t.Fatal("downlisting did not update private placeholder")
	}
	if err := api.store.RestoreTrack(track.ID); err != nil {
		t.Fatal(err)
	}
	up := syncSession(t, api, at, down.Cursor)
	if len(up.Changed) == 0 {
		t.Fatal("restore missing")
	}
	enabled := false
	a, _ = api.store.UpdateUser(a.ID, AccountPatch{Enabled: &enabled}, MutationOptions{ExpectedVersion: a.Version})
	admin, _ := readUser(api.store.db, track.OwnerID)
	p, _ := api.store.Playlist("empty-transfer")
	if err := api.store.TransferResources(a.ID, TransferRequest{TargetID: b.ID, TargetVersion: b.Version, Playlists: []ResourceVersion{{p.ID, p.Version}}}, MutationOptions{UserID: admin.ID, ExpectedVersion: a.Version}); err != nil {
		t.Fatal(err)
	}
	changed := syncSession(t, api, bt, up.Cursor) // Different user cursor always resets.
	if !changed.Reset {
		t.Fatal("cross-user cursor accepted")
	}
	changed = syncSession(t, api, bt, first.Cursor)
	if !changed.Reset {
		t.Fatal("empty transferred playlist did not change binding")
	}
	enabled = true
	api.store.UpdateUser(a.ID, AccountPatch{Enabled: &enabled}, MutationOptions{ExpectedVersion: a.Version})
	keyResult := callAPIWithKey(api, "GET", "/api/v2/sync?cursor="+oldKeySync.Cursor, "", key.Secret)
	var fresh SyncResponse
	json.Unmarshal(keyResult.Body.Bytes(), &fresh)
	if keyResult.Code != 200 || !fresh.Reset || len(fresh.Playlists) != 0 || len(fresh.Changed) != 0 {
		t.Fatal("reenabled key recovered transferred playlist", keyResult.Code, keyResult.Body.String())
	}
	// Administrator's regular sync includes their own playlists, not everybody's private notes.
	_, _, adminToken, err := api.store.Login("admin", "test administrator password", "sync-admin")
	if err != nil {
		t.Fatal(err)
	}
	s := syncSession(t, api, adminToken, "")
	encoded, _ = json.Marshal(s)
	if strings.Contains(string(encoded), "private note") {
		t.Fatal("management resources leaked into regular admin sync")
	}
}
