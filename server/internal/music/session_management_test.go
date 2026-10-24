package music

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRevokeOtherSessionsPreservesCurrentUserAndKeys(t *testing.T) {
	api, _ := testAPI(t)
	user, current, token := loginTestAccount(t, api.store, "admin")
	_, other, otherToken := loginTestAccount(t, api.store, "admin")
	_, expired, _, err := api.store.Login("admin", "test administrator password", "expired-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.store.db.Exec("UPDATE sessions SET expires_at=? WHERE id=?", persistedTime(time.Now().Add(-time.Hour)), expired.ID); err != nil {
		t.Fatal(err)
	}
	_, bobToken := readyMember(t, api, "sessions-bob")
	key := communityKey(t, api, token, "library")
	listed := sessionRequest(api, token, "GET", "/api/v2/auth/sessions", "")
	var sessions []Session
	if json.Unmarshal(listed.Body.Bytes(), &sessions) != nil || len(sessions) < 2 || sessions[0].ID != current.ID || !sessions[0].Current {
		t.Fatalf("current login not first: %d %s", listed.Code, listed.Body)
	}
	for _, expected := range []int{len(sessions) - 1, 0} {
		w := sessionRequest(api, token, "POST", "/api/v2/auth/sessions/revoke-others", "")
		var result struct {
			Revoked int `json:"revoked"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Revoked != expected {
			t.Fatalf("revoke others: %d %s", w.Code, w.Body)
		}
	}
	if w := sessionRequest(api, otherToken, "GET", "/api/v2/auth/me", ""); w.Code != 401 {
		t.Fatal("other browser remained logged in")
	}
	for _, live := range []string{token, bobToken} {
		if w := sessionRequest(api, live, "GET", "/api/v2/auth/me", ""); w.Code != 200 {
			t.Fatal("current or unrelated login revoked")
		}
	}
	if w := communityPlayer(api, key, "playlists"); w.Code != 200 {
		t.Fatal("player key changed after logging out browsers")
	}
	var changes, actorID string
	if err := api.store.db.QueryRow("SELECT changes,user_id FROM audit WHERE action='session.revoke_others' ORDER BY id LIMIT 1").Scan(&changes, &actorID); err != nil || actorID != user.ID {
		t.Fatalf("missing audit: %s %v", actorID, err)
	}
	if strings.Contains(changes, token) || strings.Contains(changes, otherToken) || strings.Contains(changes, key) || strings.Contains(changes, other.ID) {
		t.Fatal("revoke audit exposed credential information")
	}
}

func TestRevokeOtherSessionsRejectsKeyAndFirstPassword(t *testing.T) {
	api, _ := testAPI(t)
	_, _, token := loginTestAccount(t, api.store, "admin")
	key := communityKey(t, api, token, "library")
	if w := callAPIWithKey(api, "POST", "/api/v2/auth/sessions/revoke-others", "", key); w.Code != 403 {
		t.Fatalf("key revoked sessions: %d", w.Code)
	}
	member := createMemberFixture(t, api, "session-pending")
	_, _, pending, _ := api.store.Login(member.User.Username, member.Password, "session-fixture")
	if w := sessionRequest(api, pending, "POST", "/api/v2/auth/sessions/revoke-others", ""); w.Code != 403 || !strings.Contains(w.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
		t.Fatalf("first password gate: %d %s", w.Code, w.Body)
	}
}
