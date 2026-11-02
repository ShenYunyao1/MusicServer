package music

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListeningPlayerScopeAndGrantRecheck(t *testing.T) {
	a, u, _, id := listeningFixture(t)
	token := communityLogin(t, a.store, u.Username)
	p := Playlist{ID: "listen-key", Name: "Player listens", TrackIDs: []string{id}}
	if err := a.store.PutPlaylist(p, MutationOptions{UserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	secret := communityKey(t, a, token, p.ID)
	auth := testPlayerIdentity(t, a, secret)
	const sid = "80000000-0000-4000-8000-000000000003"
	w := listeningRequest(a, auth, "POST", "sessions", listeningBody(sid, id, 0))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	// The handler refreshes current scope rather than trusting the in-flight context.
	if _, err := a.store.db.Exec("UPDATE api_keys SET payload=json_set(payload,'$.scopes',json('[\"library:read\"]')) WHERE id=?", auth.keyID); err != nil {
		t.Fatal(err)
	}
	if w := listeningRequest(a, auth, "POST", "sessions", listeningBody(sid, id, 1)); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := a.store.db.Exec("UPDATE api_keys SET payload=json_set(payload,'$.scopes',json('[\"library:read\",\"media:read\"]'),'$.allowed_playlist_ids',json('[]')) WHERE id=?", auth.keyID); err != nil {
		t.Fatal(err)
	}
	if w := listeningRequest(a, auth, "POST", "sessions", listeningBody(sid, id, 1)); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestListeningSchemaBridgeGate(t *testing.T) {
	for _, schema := range []int{8, 9} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			a, _ := testAPIForSchema(t, schema)
			_, _, token := loginTestAccount(t, a.store, "admin")
			for _, item := range []struct{ method, path, body string }{
				{"GET", "/api/v2/listening/tracks", ""},
				{"POST", "/api/v2/listening/sessions", listeningBody("80000000-0000-4000-8000-000000000099", a.store.Tracks()[0].ID, 0)},
			} {
				w := mailRequest(a, token, item.method, item.path, item.body, 0)
				if schema == 8 && (w.Code != 501 || !strings.Contains(w.Body.String(), "LISTENING_DISABLED")) {
					t.Fatalf("bridge endpoint: %d %s", w.Code, w.Body)
				}
				if schema == 9 && w.Code != 200 {
					t.Fatalf("current endpoint: %d %s", w.Code, w.Body)
				}
			}
			w := httptest.NewRecorder()
			a.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			want := fmt.Sprintf(`data-listening-enabled="%t"`, schema >= 9)
			if !strings.Contains(w.Body.String(), want) {
				t.Fatalf("HTML is missing feature gate %s", want)
			}
		})
	}
}

func TestListeningRangesStableSortAndLifecycle(t *testing.T) {
	a, u, auth, _ := listeningFixture(t)
	ids := browseSeedTracks(t, a, u.ID, u.ID, 3)
	now := time.Now().UTC()
	stamp := now.UnixMilli()
	for i, id := range ids {
		if _, err := a.store.db.Exec("INSERT INTO listening_totals VALUES(?,?,?,?)", u.ID, id, 5-i, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.db.Exec("INSERT INTO listening_daily VALUES(?,?,?,?,?)", u.ID, id, now.AddDate(0, 0, -i*15).Format("2006-01-02"), 5-i, stamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		period string
		total  int
	}{{"all", 3}, {"7d", 1}, {"30d", 2}} {
		w := listeningRequest(a, auth, "GET", "tracks?range="+tc.period, "")
		var page struct {
			Total int `json:"total"`
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || page.Total != tc.total || page.Items[0].ID != ids[0] {
			t.Fatal(w.Code, w.Body, err)
		}
	}
	if _, err := a.store.db.Exec("UPDATE listening_totals SET play_count=5 WHERE user_id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	page, err := a.store.listListening(auth, listeningQuery{period: "all", offset: 1, limit: 20}, now)
	if err != nil {
		t.Fatal(err)
	}
	if items := page["items"].([]map[string]any); len(items) != 2 || items[0]["id"] != ids[1] {
		t.Fatal(page)
	}
	if _, err := a.store.db.Exec("UPDATE tracks SET removed=1 WHERE id=?", ids[0]); err != nil {
		t.Fatal(err)
	}
	w := listeningRequest(a, auth, "GET", "tracks?range=all&q=Page%20000", "")
	if !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatal(w.Body)
	}
	if _, err := a.store.db.Exec("DELETE FROM tracks WHERE id=?", ids[0]); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := a.store.db.QueryRow("SELECT count(*) FROM listening_totals WHERE track_id=?", ids[0]).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := a.store.transaction(func(tx *sql.Tx) error { return deleteAccountListeningRows(tx, u.ID) }); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"listening_sessions", "listening_totals", "listening_daily"} {
		if err := a.store.db.QueryRow("SELECT count(*) FROM "+table+" WHERE user_id=?", u.ID).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
}

func TestListeningSessionAdmissionAndRetention(t *testing.T) {
	a, u, auth, id := listeningFixture(t)
	now := time.Now().UTC()
	if _, err := a.store.db.Exec("INSERT INTO listening_sessions VALUES(?,?,?,?,?,?,?,?)", u.ID, "80000000-0000-4000-8000-000000009999", id, now.AddDate(0, 0, -31).UnixMilli(), 0, 30000, 0, now.AddDate(0, 0, -31).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		if _, err := a.store.reportListening(auth, fmt.Sprintf("80000000-0000-4000-8000-%012d", i), id, 0, now); err != nil {
			t.Fatal(i, err)
		}
	}
	var n int
	if err := a.store.db.QueryRow("SELECT count(*) FROM listening_sessions WHERE user_id=?", u.ID).Scan(&n); err != nil || n != 120 {
		t.Fatal(n, err)
	}
	if _, err := a.store.reportListening(auth, "80000000-0000-4000-8000-000000000120", id, 0, now); err == nil {
		t.Fatal("unbounded new sessions accepted")
	}
}

func listeningFixture(t *testing.T) (*API, User, authContext, string) {
	t.Helper()
	a, _ := testAPI(t)
	u, _ := readyMember(t, a, "listening-alice")
	var exists int
	if err := a.store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='listening_sessions'").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists == 0 {
		if _, err := a.store.db.Exec(listeningSchema); err != nil {
			t.Fatal(err)
		}
	}
	var sid string
	if err := a.store.db.QueryRow("SELECT id FROM sessions WHERE user_id=? ORDER BY last_active_at DESC LIMIT 1", u.ID).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	auth := authContext{userID: u.ID, keyID: sid, authVersion: u.AuthVersion, role: u.Role, session: true}
	id := a.store.Tracks()[0].ID
	return a, u, auth, id
}
func listeningRequest(a *API, auth authContext, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/v2/listening/"+path, strings.NewReader(body))
	r = r.WithContext(withAuth(r.Context(), auth))
	w := httptest.NewRecorder()
	a.listening(w, r)
	return w
}
func listeningBody(sid, id string, played int64) string {
	return fmt.Sprintf(`{"session_id":%q,"track_id":%q,"played_ms":%d}`, sid, id, played)
}
func TestListeningSessionThresholdIdempotencyAndPrivacy(t *testing.T) {
	a, u, auth, id := listeningFixture(t)
	const sid = "80000000-0000-4000-8000-000000000001"
	post := func(played int64) *httptest.ResponseRecorder {
		return listeningRequest(a, auth, "POST", "sessions", listeningBody(sid, id, played))
	}
	if w := post(30000); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	w := post(0)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var receipt struct {
		Minimum int64 `json:"minimum_ms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.db.Exec("UPDATE listening_sessions SET started_at=? WHERE user_id=?", time.Now().Add(-time.Minute).UnixMilli(), u.ID); err != nil {
		t.Fatal(err)
	}
	if w := post(receipt.Minimum - 1); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	for i := 0; i < 3; i++ {
		w = post(receipt.Minimum)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"counted":true`) {
			t.Fatal(w.Code, w.Body)
		}
	}
	var count int
	if err := a.store.db.QueryRow("SELECT play_count FROM listening_totals WHERE user_id=? AND track_id=?", u.ID, id).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	w = listeningRequest(a, auth, "GET", "tracks?range=all&limit=20", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"play_count":1`) {
		t.Fatal(w.Code, w.Body)
	}
	bob, _ := readyMember(t, a, "listening-bob")
	var bs string
	_ = a.store.db.QueryRow("SELECT id FROM sessions WHERE user_id=?", bob.ID).Scan(&bs)
	other := authContext{userID: bob.ID, keyID: bs, authVersion: bob.AuthVersion, role: bob.Role, session: true}
	w = listeningRequest(a, other, "GET", "tracks", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := listeningRequest(a, auth, "GET", "tracks?limit=21", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if w := listeningRequest(a, auth, "POST", "sessions", fmt.Sprintf(`{"session_id":%q,"track_id":%q,"played_ms":0,"user_id":%q}`, sid, id, bob.ID)); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestListeningRejectsForgedElapsedAndRevokedSession(t *testing.T) {
	a, u, auth, id := listeningFixture(t)
	// Unknown duration uses a thirty second threshold.
	if _, err := a.store.db.Exec("UPDATE tracks SET payload=json_set(payload,'$.duration_ms',0) WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	const sid = "80000000-0000-4000-8000-000000000002"
	w := listeningRequest(a, auth, "POST", "sessions", listeningBody(sid, id, 0))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := listeningRequest(a, auth, "POST", "sessions", listeningBody(sid, id, 30000)); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := a.store.db.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=?", persistedTime(time.Now()), u.ID); err != nil {
		t.Fatal(err)
	}
	if w := listeningRequest(a, auth, "GET", "tracks", ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
}
