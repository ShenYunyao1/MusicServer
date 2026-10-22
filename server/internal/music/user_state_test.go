package music

import (
	"encoding/json"
	"testing"
	"time"
)

func TestUsersDisableEnablePreservesKeysAndStats(t *testing.T) {
	api, _ := testAPI(t)
	member := createMemberFixture(t, api, "alice")
	if err := api.store.ChangePassword(member.User, member.Password, "personal password for alice"); err != nil {
		t.Fatal(err)
	}
	user, _, token, err := api.store.Login("alice", "personal password for alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "Keep"}, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if w := callAPIWithKey(api, "GET", "/api/v2/playlists", "", key.Secret); w.Code != 200 {
		t.Fatal(w.Body)
	}
	path := "/api/v2/admin/users/" + user.ID
	w := adminVersionRequest(api, "PATCH", path, `{"enabled":false}`, user.Version)
	if w.Code != 200 {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	var disabled User
	json.Unmarshal(w.Body.Bytes(), &disabled)
	if w := sessionRequest(api, token, "GET", "/api/v2/auth/me", ""); w.Code != 401 {
		t.Fatalf("session survived: %d", w.Code)
	}
	if w := callAPIWithKey(api, "GET", "/api/v2/playlists", "", key.Secret); w.Code != 401 {
		t.Fatalf("disabled key accepted: %d", w.Code)
	}
	if _, ok := api.store.AuthenticateSecret(key.Secret); !ok {
		t.Fatal("account disable revoked key credential")
	}
	if stats := api.readCallStats(); len(stats.Keys) != 1 || stats.Total.TotalRequests != 1 {
		t.Fatalf("disable deleted stats: %+v", stats)
	}
	if w := adminVersionRequest(api, "PATCH", path, `{"enabled":true}`, user.Version); w.Code != 409 {
		t.Fatalf("stale enable: %d", w.Code)
	}
	if w := adminVersionRequest(api, "PATCH", path, `{"enabled":true}`, disabled.Version); w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	if w := callAPIWithKey(api, "GET", "/api/v2/playlists", "", key.Secret); w.Code != 200 {
		t.Fatalf("same key not restored: %d", w.Code)
	}
	if stats := api.readCallStats(); stats.Total.TotalRequests != 2 {
		t.Fatalf("stats not continuous: %+v", stats)
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/auth/me", ""); w.Code != 401 {
		t.Fatal("old session restored")
	}
	if _, _, _, err := api.store.Login("alice", "personal password for alice", "again"); err != nil {
		t.Fatal(err)
	}
}
func TestUsersToggleDoesNotRestoreRevokedOrExpiredKey(t *testing.T) {
	api, _ := testAPI(t)
	member := createMemberFixture(t, api, "alice")
	api.store.ChangePassword(member.User, member.Password, "alice personal password")
	revoked, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "revoked"}, MutationOptions{UserID: member.User.ID})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "expired", ExpiresAt: time.Now().Add(-time.Hour)}, MutationOptions{UserID: member.User.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.store.RevokeAPIKey(revoked.ID); err != nil {
		t.Fatal(err)
	}
	u, _ := readUser(api.store.db, member.User.ID)
	if err := api.store.SetUserEnabled(u.ID, false, u.Version); err != nil {
		t.Fatal(err)
	}
	u, _ = readUser(api.store.db, u.ID)
	if err := api.store.SetUserEnabled(u.ID, true, u.Version); err != nil {
		t.Fatal(err)
	}
	for _, key := range []APIKeyView{revoked, expired} {
		if w := callAPIWithKey(api, "GET", "/api/v2/playlists", "", key.Secret); w.Code != 401 {
			t.Fatalf("old key revived: %d", w.Code)
		}
	}
}
func TestUsersPatchVersionAndLastAdmin(t *testing.T) {
	api, _ := testAPI(t)
	admin, _, _, err := api.store.Login("admin", "test administrator password", "admin")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/admin/users/" + admin.ID
	if w := adminVersionRequest(api, "PATCH", path, `{"enabled":false}`, 0); w.Code != 428 {
		t.Fatalf("missing version: %d", w.Code)
	}
	if w := adminVersionRequest(api, "PATCH", path, `{"enabled":false}`, admin.Version); w.Code == 200 {
		t.Fatal("last admin disabled")
	}
	if w := adminVersionRequest(api, "PATCH", path, `{"display_name":"管理员"}`, admin.Version); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
}
