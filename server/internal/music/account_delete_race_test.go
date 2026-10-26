package music

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type pausedAccountBody struct {
	reader  io.Reader
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (b *pausedAccountBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started); <-b.resume })
	return b.reader.Read(p)
}
func (*pausedAccountBody) Close() error { return nil }

func TestAccountDeletionRejectsPreviouslyAuthenticatedAdminWrites(t *testing.T) {
	for _, action := range []string{"create", "update", "reset"} {
		t.Run(action, func(t *testing.T) {
			api, _ := testAPI(t)
			admin, _, _ := loginTestAccount(t, api.store, "admin")
			actor, err := api.store.createUser("RetiredAdmin", "retiring administrator password", "admin", false)
			if err != nil {
				t.Fatal(err)
			}
			_, _, token, err := api.store.Login(actor.Username, "retiring administrator password", "retire-test")
			if err != nil {
				t.Fatal(err)
			}
			target, _ := readyMember(t, api, "retained-member")
			method, path, body := "POST", "/api/v2/admin/users", `{"quota_bytes":0,"username":"late-created-member"}`
			if action == "update" {
				method, path, body = "PATCH", "/api/v2/admin/users/"+target.ID, `{"display_name":"Unauthorized late change"}`
			} else if action == "reset" {
				path, body = "/api/v2/admin/users/"+target.ID+"/reset-password", `{}`
			}
			paused := &pausedAccountBody{reader: strings.NewReader(body), started: make(chan struct{}), resume: make(chan struct{})}
			request := httptest.NewRequest(method, "http://example.com"+path, nil)
			request.Body = paused
			request.Header.Set("Origin", "http://example.com")
			request.Header.Set("X-CSRF-Token", csrfForToken(token))
			request.Header.Set("If-Match", "2")
			request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			response, done := httptest.NewRecorder(), make(chan struct{})
			go func() { defer close(done); api.ServeHTTP(response, request) }()
			<-paused.started // Authentication has succeeded, before decoding the mutation.
			if err := api.store.SetUserEnabled(actor.ID, false, actor.Version); err != nil {
				close(paused.resume)
				<-done
				t.Fatal(err)
			}
			actor, _ = readUser(api.store.db, actor.ID)
			err = api.store.DeleteUser(actor.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: actor.Version})
			close(paused.resume)
			<-done
			if err != nil {
				t.Fatal(err)
			}
			if response.Code != 404 {
				t.Fatalf("deleted administrator completed %s: %d", action, response.Code)
			}
			retained, err := readUser(api.store.db, target.ID)
			if err != nil || retained.Version != target.Version || retained.DisplayName != target.DisplayName {
				t.Fatal("stale administrator changed the target account")
			}
		})
	}
}
