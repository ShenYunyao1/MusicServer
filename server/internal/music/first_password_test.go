package music

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUsersFirstPasswordGate(t *testing.T) {
	api, _ := testAPI(t)
	member := createMemberFixture(t, api, "alice")
	_, _, token, err := api.store.Login("alice", member.Password, "first")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ method, path, body string }{{"GET", "/api/v2/tracks", ""}, {"GET", "/api/v2/playlists", ""}, {"GET", "/api/v2/sync", ""}, {"POST", "/api/v2/uploads/stage", ""}, {"GET", "/api/v2/auth/keys", ""}, {"POST", "/api/v2/auth/keys", `{"name":"forbidden"}`}, {"GET", "/api/v2/auth/sessions", ""}} {
		w := sessionRequest(api, token, item.method, item.path, item.body)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
			t.Fatalf("first password bypass %s: %d %s", item.path, w.Code, w.Body)
		}
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/auth/me", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"must_change_password":true`) {
		t.Fatalf("identity: %d %s", w.Code, w.Body)
	}
	body, _ := json.Marshal(map[string]string{"old_password": member.Password, "new_password": member.Password})
	if w := sessionRequest(api, token, "PUT", "/api/v2/auth/password", string(body)); w.Code != 400 {
		t.Fatalf("temporary password reused: %d", w.Code)
	}
	body, _ = json.Marshal(map[string]string{"old_password": member.Password, "new_password": "new personally chosen password"})
	if w := sessionRequest(api, token, "PUT", "/api/v2/auth/password", string(body)); w.Code != 204 {
		t.Fatalf("first password change: %d %s", w.Code, w.Body)
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/auth/me", ""); w.Code != 401 {
		t.Fatalf("old session survived: %d", w.Code)
	}
	user, _, fresh, err := api.store.Login("alice", "new personally chosen password", "again")
	if err != nil || user.MustChangePassword {
		t.Fatalf("flag not cleared: %+v %v", user, err)
	}
	if w := sessionRequest(api, fresh, "GET", "/api/v2/playlists", ""); w.Code != 200 {
		t.Fatalf("normal session still gated: %d", w.Code)
	}
}
func TestUsersPasswordResetBlocksExistingKeyUntilChanged(t *testing.T) {
	api, _ := testAPI(t)
	member := createMemberFixture(t, api, "alice")
	if err := api.store.ChangePassword(member.User, member.Password, "personally chosen password"); err != nil {
		t.Fatal(err)
	}
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "player"}, MutationOptions{UserID: member.User.ID})
	if err != nil {
		t.Fatal(err)
	}
	user, err := readUser(api.store.db, member.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := api.store.ResetMemberPassword(user.ID, false, MutationOptions{ExpectedVersion: user.Version})
	if err != nil {
		t.Fatal(err)
	}
	if w := callAPIWithKey(api, "GET", "/api/v2/playlists", "", key.Secret); w.Code != 401 {
		t.Fatalf("key bypassed forced change: %d", w.Code)
	}
	if err := api.store.ChangePassword(reset.User, reset.TemporaryPassword, "second personally chosen password"); err != nil {
		t.Fatal(err)
	}
	if w := callAPIWithKey(api, "GET", "/api/v2/playlists", "", key.Secret); w.Code != 200 {
		t.Fatalf("retained key failed after change: %d %s", w.Code, w.Body)
	}
}
