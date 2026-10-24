package music

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProfileSelfUpdateAndPersistence(t *testing.T) {
	api, _ := testAPI(t)
	alice, token := readyMember(t, api, "profile-alice")
	bob, _ := readyMember(t, api, "profile-bob")
	key := communityKey(t, api, token, "library")
	patch := func(body string, version int64) *httptest.ResponseRecorder {
		return memberVersionRequest(api, token, "PATCH", "/api/v2/auth/me", body, version)
	}
	response := patch(`{"display_name":"  音乐朋友 🎵  "}`, alice.Version)
	if response.Code != 200 {
		t.Fatalf("self profile: %d %s", response.Code, response.Body)
	}
	var result struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	user := result.User
	if user.DisplayName != "音乐朋友 🎵" || user.ID != alice.ID || user.Username != alice.Username || user.Role != "member" || !user.Enabled || user.Version != alice.Version+1 {
		t.Fatalf("identity changed: %+v", user)
	}
	if w := patch(`{"display_name":"stale"}`, alice.Version); w.Code != 409 {
		t.Fatalf("stale profile accepted: %d %s", w.Code, w.Body)
	}
	if w := patch(`{"display_name":"音乐朋友 🎵"}`, user.Version); w.Code != 200 {
		t.Fatalf("no-op profile: %d", w.Code)
	}
	current, _ := readUser(api.store.db, alice.ID)
	if current.Version != user.Version || current.AuthVersion != alice.AuthVersion {
		t.Fatal("nickname update rotated authentication or no-op version")
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/auth/me", ""); w.Code != 200 {
		t.Fatal("nickname update revoked browser login")
	}
	if w := communityPlayer(api, key, "playlists"); w.Code != 200 {
		t.Fatal("nickname update invalidated player key")
	}
	// Duplicate nicknames are allowed; account UUIDs and credentials stay distinct.
	if w := patch(`{"display_name":"`+bob.DisplayName+`"}`, user.Version); w.Code != 200 {
		t.Fatalf("duplicate nickname rejected: %d %s", w.Code, w.Body)
	}
	other, _ := readUser(api.store.db, bob.ID)
	if other.Version != bob.Version || other.Username != bob.Username {
		t.Fatal("another account changed")
	}
	reopened, err := NewStore(api.store.DataPath(), api.store.mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := readUser(reopened.db, alice.ID)
	if err != nil || persisted.DisplayName != bob.DisplayName || persisted.ID != alice.ID {
		t.Fatalf("nickname not persisted: %+v %v", persisted, err)
	}
}

func TestProfileRejectsUnsafeRequests(t *testing.T) {
	api, _ := testAPI(t)
	user, token := readyMember(t, api, "profile-member")
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"display_name":null}`, `{"display_name":" "}`,
		`{"display_name":"` + strings.Repeat("名", 129) + `"}`,
		`{"display_name":1}`, `{"display_name":"name"} {}`,
		`{"display_name":"name","enabled":false}`, `{"display_name":"name","role":"admin"}`,
		`{"display_name":"name","user_id":"another"}`, `{"username":"new-login"}`, `{"id":"another"}`,
	} {
		w := memberVersionRequest(api, token, "PATCH", "/api/v2/auth/me", body, user.Version)
		if w.Code != 400 {
			t.Fatalf("invalid profile accepted (%s): %d %s", body, w.Code, w.Body)
		}
	}
	if w := sessionRequest(api, token, "PATCH", "/api/v2/auth/me", `{"display_name":"name"}`); w.Code != 428 {
		t.Fatalf("missing profile version: %d", w.Code)
	}
	r := httptest.NewRequest("PATCH", "http://example.com/api/v2/auth/me", strings.NewReader(`{"display_name":"name"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "CSRF_DENIED") {
		t.Fatalf("profile without CSRF: %d", w.Code)
	}
	key := communityKey(t, api, token, "library")
	if w := callAPIWithKey(api, "PATCH", "/api/v2/auth/me", `{"display_name":"name"}`, key); w.Code != 403 {
		t.Fatalf("player key changed profile: %d", w.Code)
	}
	temporary := createMemberFixture(t, api, "profile-temporary")
	_, _, pending, _ := api.store.Login(temporary.User.Username, temporary.Password, "profile-test")
	if w := memberVersionRequest(api, pending, "PATCH", "/api/v2/auth/me", `{"display_name":"name"}`, temporary.User.Version); w.Code != 403 || !strings.Contains(w.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
		t.Fatalf("first-password bypass: %d %s", w.Code, w.Body)
	}
}

func TestProfilePublicAuthorInvalidatesCatalog(t *testing.T) {
	api, author, reader, playlist := communityFixture(t)
	at := communityLogin(t, api.store, author.Username)
	bt := communityLogin(t, api.store, reader.Username)
	if w := memberVersionRequest(api, at, "PUT", "/api/v2/playlists/"+playlist.ID+"/visibility", `{"visibility":"public"}`, playlist.Version); w.Code != 200 {
		t.Fatalf("publish: %d %s", w.Code, w.Body)
	}
	before := communityConditional(api, bt, "public/playlists", "")
	if w := memberVersionRequest(api, at, "PATCH", "/api/v2/auth/me", `{"display_name":"新的作者昵称"}`, author.Version); w.Code != 200 {
		t.Fatalf("author rename: %d %s", w.Code, w.Body)
	}
	after := communityConditional(api, bt, "public/playlists", before.Header().Get("ETag"))
	if after.Code != 200 || !strings.Contains(after.Body.String(), "新的作者昵称") {
		t.Fatalf("catalog nickname stale: %d %s", after.Code, after.Body)
	}
	current, _ := api.store.Playlist(playlist.ID)
	if current.OwnerID != author.ID || len(current.TrackIDs) != len(playlist.TrackIDs) {
		t.Fatal("nickname changed playlist ownership or members")
	}
}

func TestProfileSharingNamesKeepUniqueLogins(t *testing.T) {
	api, author, reader, playlist := sharingFixture(t)
	at := communityLogin(t, api.store, author.Username)
	bt := communityLogin(t, api.store, reader.Username)
	for _, item := range []struct {
		token string
		user  User
	}{{at, author}, {bt, reader}} {
		if w := memberVersionRequest(api, item.token, "PATCH", "/api/v2/auth/me", `{"display_name":"同名朋友"}`, item.user.Version); w.Code != 200 {
			t.Fatalf("duplicate display name: %d %s", w.Code, w.Body)
		}
	}
	invitation, err := api.store.CreateInvitation(playlist.ID, reader.Username, MutationOptions{UserID: author.ID, ExpectedVersion: playlist.Version})
	if err != nil {
		t.Fatal(err)
	}
	response := sessionRequest(api, bt, "GET", "/api/v2/invitations/"+invitation.ID, "")
	var view map[string]any
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &view) != nil {
		t.Fatalf("invitation: %d %s", response.Code, response.Body)
	}
	if view["inviter_name"] != "同名朋友" || view["recipient_display_name"] != "同名朋友" || view["inviter_username"] != author.Username || view["recipient_username"] != reader.Username {
		t.Fatalf("ambiguous invitation identity: %s", response.Body)
	}
}
