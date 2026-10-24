package music

import (
	"encoding/json"
	"testing"
	"time"
)

func TestKeyDeletionStartupRemovesOldRevokedRows(t *testing.T) {
	api, _ := testAPI(t)
	key, _ := createPersistentKey(t, api, APIKeyCreateRequest{Name: "Old revoked", AllowedPlaylistIDs: []string{"library"}})
	keep, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "Keep"})
	key.RevokedAt = time.Now().UTC()
	raw, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("UPDATE api_keys SET payload=? WHERE id=?", string(raw), key.ID); err != nil {
		t.Fatal(err)
	}
	if err := api.store.BeginServer(); err != nil {
		t.Fatal(err)
	}
	assertKeyRowsDeleted(t, api.store, key.ID)
	if keys := api.store.APIKeys(); len(keys) != 1 || keys[0].ID != keep.ID {
		t.Fatal("startup did not retain exactly the current key")
	}
	if _, ok := api.store.AuthenticateSecret(secret); !ok {
		t.Fatal("startup removed a current key")
	}
}

func assertKeyRowsDeleted(t *testing.T, s *Store, id string) {
	t.Helper()
	for _, query := range []string{
		"SELECT count(*) FROM api_keys WHERE id=?",
		"SELECT count(*) FROM key_playlist_grants WHERE key_id=?",
		"SELECT count(*) FROM scopes WHERE key_id=?",
		"SELECT count(*) FROM api_call_stats WHERE key_id=?",
	} {
		var count int
		if err := s.db.QueryRow(query, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("key deletion left rows (%s): %d %v", query, count, err)
		}
	}
}

func TestKeyDeletionRemovesRowsAndCannotRotate(t *testing.T) {
	api, _ := testAPI(t)
	key, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "Delete", AllowedPlaylistIDs: []string{"library"}})
	api.recordCall(key.ID, 200, 10, time.Millisecond)
	if response := callAPI(api, "DELETE", "/api/v2/auth/keys/"+key.ID, ""); response.Code != 204 {
		t.Fatalf("delete key: %d", response.Code)
	}
	assertKeyRowsDeleted(t, api.store, key.ID)
	api.recordCall(key.ID, 200, 10, time.Millisecond)
	assertKeyRowsDeleted(t, api.store, key.ID)
	if _, err := api.store.RotateAPIKey(key.ID); err == nil {
		t.Fatal("deleted key was revived by rotation")
	}
	if response := callAPIWithKey(api, "GET", "/api/v2/playlists", "", secret); response.Code != 401 {
		t.Fatalf("deleted key authenticated: %d", response.Code)
	}
}

func TestKeyDeletionBulkResetAndAccountIsolation(t *testing.T) {
	api, _ := testAPI(t)
	a, token := readyMember(t, api, "delete-key-owner")
	b, _ := readyMember(t, api, "keep-key-owner")
	create := func(user User) APIKeyView {
		key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "Player", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: user.ID})
		if err != nil {
			t.Fatal(err)
		}
		api.recordCall(key.ID, 200, 1, time.Millisecond)
		return key
	}
	first, second, other := create(a), create(a), create(b)
	if response := sessionRequest(api, token, "DELETE", "/api/v2/auth/keys/"+other.ID, ""); response.Code != 404 {
		t.Fatalf("deleted another account's key: %d", response.Code)
	}
	if response := sessionRequest(api, token, "DELETE", "/api/v2/auth/keys", ""); response.Code != 204 {
		t.Fatalf("bulk deletion: %d", response.Code)
	}
	assertKeyRowsDeleted(t, api.store, first.ID)
	assertKeyRowsDeleted(t, api.store, second.ID)
	resetKey := create(a)
	if response := adminVersionRequest(api, "POST", "/api/v2/admin/users/"+a.ID+"/reset-password", `{"revoke_keys":true}`, a.Version); response.Code != 200 {
		t.Fatalf("reset and delete keys: %d", response.Code)
	}
	assertKeyRowsDeleted(t, api.store, resetKey.ID)
	if _, ok := api.store.AuthenticateSecret(other.Secret); !ok {
		t.Fatal("another account lost its key")
	}
}
