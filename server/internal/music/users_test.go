package music

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type memberFixture struct {
	User     User   `json:"user"`
	Password string `json:"temporary_password"`
}

func createMemberFixture(t *testing.T, api *API, name string) memberFixture {
	t.Helper()
	w := callAPI(api, "POST", "/api/v2/admin/users", `{"username":"`+name+`","display_name":"成员"}`)
	if w.Code != 201 {
		t.Fatalf("member create: %d %s", w.Code, w.Body)
	}
	var result memberFixture
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func adminVersionRequest(api *API, method, path, body string, version int64) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	authorizeTestAdmin(api, r)
	if version > 0 {
		r.Header.Set("If-Match", fmt.Sprint(version))
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}
func TestUsersAdminCreatesListsAndResets(t *testing.T) {
	api, _ := testAPI(t)
	a := createMemberFixture(t, api, "Alice")
	b := createMemberFixture(t, api, "bob")
	if a.User.Username != "Alice" || a.User.Role != "member" || !a.User.MustChangePassword || len(a.Password) < 12 || a.Password == b.Password {
		t.Fatalf("bad temporary credentials: %+v", a.User)
	}
	list := callAPI(api, "GET", "/api/v2/admin/users", "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), "Alice") || strings.Contains(list.Body.String(), a.Password) || strings.Contains(list.Body.String(), "password_hash") {
		t.Fatalf("user list: %d %s", list.Code, list.Body)
	}
	if w := callAPI(api, "POST", "/api/v2/admin/users", `{"username":"ALICE"}`); w.Code != 409 {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}
	if w := callAPI(api, "POST", "/api/v2/admin/users", `{"username":"evil","role":"admin"}`); w.Code != 400 {
		t.Fatalf("role injection: %d", w.Code)
	}
	path := "/api/v2/admin/users/" + a.User.ID + "/reset-password"
	if w := adminVersionRequest(api, "POST", path, `{}`, 0); w.Code != 428 {
		t.Fatalf("missing version: %d", w.Code)
	}
	w := adminVersionRequest(api, "POST", path, `{"revoke_keys":false}`, a.User.Version)
	if w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	var reset memberFixture
	json.Unmarshal(w.Body.Bytes(), &reset)
	if reset.Password == a.Password || len(reset.Password) < 12 || !reset.User.MustChangePassword {
		t.Fatal("reset did not issue fresh temporary credentials")
	}
	if _, _, _, err := api.store.Login("alice", a.Password, "old"); err == nil {
		t.Fatal("old password still works")
	}
	if _, _, _, err := api.store.Login("alice", reset.Password, "new"); err != nil {
		t.Fatal(err)
	}
	if w := adminVersionRequest(api, "POST", path, `{}`, a.User.Version); w.Code != 409 {
		t.Fatalf("stale reset: %d", w.Code)
	}
	entries, err := api.store.Audit(100, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(entries)
	if strings.Contains(string(raw), a.Password) || strings.Contains(string(raw), reset.Password) {
		t.Fatal("temporary password leaked into audit")
	}
}
func TestUsersRejectMemberAndKeyManagement(t *testing.T) {
	api, _ := testAPI(t)
	member := createMemberFixture(t, api, "alice")
	_, _, token, err := api.store.Login("alice", member.Password, "member")
	if err != nil {
		t.Fatal(err)
	}
	if w := sessionRequest(api, token, "POST", "/api/v2/admin/users", `{"username":"outsider"}`); w.Code != 403 {
		t.Fatalf("member created user: %d", w.Code)
	}
	_, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "player"})
	if w := callAPIWithKey(api, "GET", "/api/v2/admin/users", "", secret); w.Code != 403 {
		t.Fatalf("key managed users: %d", w.Code)
	}
}
func TestUsersConcurrentDuplicateCreation(t *testing.T) {
	api, _ := testAPI(t)
	callAPI(api, "GET", "/api/v2/admin/users", "")
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, username := range []string{"Same-User", "sAME-uSER"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- callAPI(api, "POST", "/api/v2/admin/users", `{"username":"`+username+`"}`).Code
		}()
	}
	wg.Wait()
	close(codes)
	seen := map[int]int{}
	for code := range codes {
		seen[code]++
	}
	if seen[201] != 1 || seen[409] != 1 {
		t.Fatalf("duplicate race: %+v", seen)
	}
}
