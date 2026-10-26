package music

import (
	"bytes"
	"database/sql"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func resourcePolicyRequest(t *testing.T, api *API, token, id, kind string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	field, name, data := "cover", "cover.png", tinyPNG()
	if kind == "lyrics" {
		field, name, data = "lyrics_file", "song.lrc", []byte("[00:01]new uploaded lyrics")
	}
	file, err := form.CreateFormFile(field, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPut, "http://example.com/api/v2/tracks/"+id+"/"+kind, &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", csrfForToken(token))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	addTestVersion(api, r)
	return r
}

// Confirm the complete HTTP body has reached the serialized mutation before
// changing policy. A body-read hook alone races the final read authorization.
func waitResourceTransaction(t *testing.T, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stack := make([]byte, 2<<20)
		n := runtime.Stack(stack, true)
		for _, goroutine := range strings.Split(string(stack[:n]), "\n\n") {
			if strings.Contains(goroutine, "(*API).trackResource(") && strings.Contains(goroutine, "(*Store).transaction(") {
				return
			}
		}
		select {
		case <-done:
			t.Fatal("resource request returned before transaction lock")
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("resource request did not reach the queued transaction")
}

func resourcePolicyCounts(t *testing.T, s *Store) [3]int {
	t.Helper()
	var result [3]int
	for i, table := range []string{"cover_assets", "metadata_overrides", "audit"} {
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&result[i]); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestUploadEmailPolicyTrackResourceQueuedMutation(t *testing.T) {
	for _, kind := range []string{"cover", "lyrics"} {
		for _, change := range []string{"require_verification", "revoke_session"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				api, _ := testAPI(t)
				user, session, token := loginTestAccount(t, api.store, "admin")
				before := api.store.Tracks()[0]
				counts, revision := resourcePolicyCounts(t, api.store), api.store.Revision()
				r := resourcePolicyRequest(t, api, token, before.ID, kind)
				w := httptest.NewRecorder()
				done := make(chan struct{})
				api.store.writeMu.Lock()
				locked := true
				defer func() {
					if locked {
						api.store.writeMu.Unlock()
						<-done
					}
				}()
				go func() { defer close(done); api.ServeHTTP(w, r) }()
				waitResourceTransaction(t, done)
				if change == "require_verification" {
					setEmailTestRequirement(t, api.store, user.ID, true)
				} else if _, err := api.store.db.Exec("UPDATE sessions SET revoked_at=? WHERE id=?", persistedTime(time.Now()), session.ID); err != nil {
					t.Fatal(err)
				}
				api.store.writeMu.Unlock()
				locked = false
				<-done
				if w.Code < 400 {
					t.Fatalf("queued upload ignored changed authorization: %d %s", w.Code, w.Body)
				}
				if change == "require_verification" && (w.Code != 403 || !strings.Contains(w.Body.String(), "EMAIL_VERIFICATION_REQUIRED")) {
					t.Fatalf("wrong policy error: %d %s", w.Code, w.Body)
				}
				after, _ := api.store.Track(before.ID)
				if !reflect.DeepEqual(before, after) || api.store.Revision() != revision || resourcePolicyCounts(t, api.store) != counts {
					t.Fatalf("denied resource changed track, revision, assets, overrides or audit: before=%+v after=%+v", before, after)
				}
			})
		}
	}
}

func TestUploadEmailPolicyTrackResourceChecksAdministratorActor(t *testing.T) {
	for _, kind := range []string{"cover", "lyrics"} {
		t.Run(kind, func(t *testing.T) {
			api, _ := testAPI(t)
			admin, _, token := loginTestAccount(t, api.store, "admin")
			member, _ := readyMember(t, api, "ResourceOwner")
			setEmailTestRequirement(t, api.store, member.ID, true)
			track := api.store.Tracks()[0]
			track.OwnerID = member.ID
			if err := api.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			api.ServeHTTP(w, resourcePolicyRequest(t, api, token, track.ID, kind))
			if w.Code != 200 {
				t.Fatalf("exempt admin blocked by target owner policy: %d %s", w.Code, w.Body)
			}
			setEmailTestRequirement(t, api.store, admin.ID, true)
			w = httptest.NewRecorder()
			api.ServeHTTP(w, resourcePolicyRequest(t, api, token, track.ID, kind))
			if w.Code != 403 || !strings.Contains(w.Body.String(), "EMAIL_VERIFICATION_REQUIRED") {
				t.Fatalf("unverified admin upload allowed: %d %s", w.Code, w.Body)
			}
			current, _ := api.store.Track(track.ID)
			w = memberVersionRequest(api, token, "PATCH", "/api/v2/tracks/"+track.ID, fmt.Sprintf(`{"title":%q}`, "Allowed metadata edit"), current.Version)
			if w.Code != 200 {
				t.Fatalf("ordinary metadata edit incorrectly gated: %d %s", w.Code, w.Body)
			}
		})
	}
}
