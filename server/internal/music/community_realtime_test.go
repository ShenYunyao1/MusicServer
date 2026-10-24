package music

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type realtimeTestSection struct {
	Changed []json.RawMessage `json:"changed"`
	Removed []string          `json:"removed"`
	Order   []string          `json:"order"`
	Meta    map[string]any    `json:"meta"`
}

type realtimeTestReply struct {
	Cursor   string                         `json:"cursor"`
	Reset    bool                           `json:"reset"`
	Sections map[string]realtimeTestSection `json:"sections"`
}

func realtimeTestSync(t *testing.T, api *API, token, cursor string, view map[string]any) realtimeTestReply {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"cursor": cursor, "view": view})
	w := sessionRequest(api, token, "POST", "/api/v2/community/sync", string(body))
	var reply realtimeTestReply
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
		t.Fatalf("realtime sync: %d %s", w.Code, w.Body)
	}
	return reply
}
func TestRealtimeInvitationDeltaAndCursorIsolation(t *testing.T) {
	api, author, reader, playlist := sharingFixture(t)
	at := communityLogin(t, api.store, author.Username)
	bt := communityLogin(t, api.store, reader.Username)
	view := map[string]any{"invitations": true, "subscriptions": true}
	first := realtimeTestSync(t, api, bt, "", view)
	if !first.Reset || first.Cursor == "" {
		t.Fatal("missing initial baseline")
	}
	unchanged := realtimeTestSync(t, api, bt, first.Cursor, view)
	if unchanged.Reset || len(unchanged.Sections) != 0 {
		t.Fatal("unchanged view downloaded again")
	}
	i, err := api.store.CreateInvitation(playlist.ID, reader.Username, MutationOptions{UserID: author.ID, ExpectedVersion: playlist.Version})
	if err != nil {
		t.Fatal(err)
	}
	delta := realtimeTestSync(t, api, bt, first.Cursor, view)
	if delta.Reset || len(delta.Sections["invitations"].Changed) != 1 || delta.Sections["badge"].Meta["pending"] != float64(1) {
		t.Fatalf("missing new invitation: %+v", delta)
	}
	foreign := realtimeTestSync(t, api, at, delta.Cursor, view)
	if !foreign.Reset || len(foreign.Sections["invitations"].Changed) != 0 {
		t.Fatal("cursor exposed another user's invitations")
	}
	for _, section := range foreign.Sections {
		if len(section.Removed) != 0 {
			t.Fatal("foreign cursor exposed removed object IDs")
		}
	}
	i, err = api.store.InvitationAction(i.ID, "accept", MutationOptions{UserID: reader.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	accepted := realtimeTestSync(t, api, bt, delta.Cursor, view)
	if len(accepted.Sections["subscriptions"].Changed) != 1 || accepted.Sections["badge"].Meta["pending"] != float64(0) {
		t.Fatal("acceptance did not update subscriptions/badge")
	}
	_, err = api.store.InvitationAction(i.ID, "revoke", MutationOptions{UserID: author.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	revoked := realtimeTestSync(t, api, bt, accepted.Cursor, view)
	if len(revoked.Sections["subscriptions"].Changed) != 1 || !strings.Contains(string(revoked.Sections["subscriptions"].Changed[0]), `"subscription_status":"invalid"`) {
		t.Fatal("revoke left private content active")
	}
	if strings.Contains(string(revoked.Sections["subscriptions"].Changed[0]), "AUTHOR") {
		t.Fatal("private note leaked")
	}
}
func TestRealtimeCatalogPagingRemovalAndDetailPrivacy(t *testing.T) {
	api, author, reader, p := communityFixture(t)
	bt := communityLogin(t, api.store, reader.Username)
	public := "public"
	p, err := api.store.SetPlaylistVisibility(p.ID, public, MutationOptions{UserID: author.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	view := map[string]any{"catalog": true, "public_detail": p.ID}
	first := realtimeTestSync(t, api, bt, "", view)
	if len(first.Sections["catalog"].Changed) != 1 || len(first.Sections["detail_playlist"].Changed) != 1 {
		t.Fatal("missing public initial view")
	}
	name := "Realtime title"
	p, err = api.store.UpdatePlaylistMetadata(p.ID, &name, nil, MutationOptions{UserID: author.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	delta := realtimeTestSync(t, api, bt, first.Cursor, view)
	if delta.Reset || len(delta.Sections["catalog"].Changed) != 1 || len(delta.Sections["detail_tracks"].Changed) != 0 {
		t.Fatal("metadata update redownloaded unchanged tracks")
	}
	_, err = api.store.SetPlaylistVisibility(p.ID, "private", MutationOptions{UserID: author.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	removed := realtimeTestSync(t, api, bt, delta.Cursor, view)
	if len(removed.Sections["catalog"].Removed) != 1 || removed.Sections["detail_playlist"].Meta["available"] != false || len(removed.Sections["detail_tracks"].Changed) != 0 {
		t.Fatalf("private data retained: %+v", removed)
	}
	changedView := realtimeTestSync(t, api, bt, first.Cursor, map[string]any{"catalog": true, "offset": 30})
	if !changedView.Reset {
		t.Fatal("cursor reused across pagination")
	}
}

func TestRealtimeCursorExpiryAndReadySession(t *testing.T) {
	api, _ := testAPI(t)
	_, _, token := loginTestAccount(t, api.store, "admin")
	view := map[string]any{"invitations": true}
	first := realtimeTestSync(t, api, token, "", view)
	api.realtimeMu.Lock()
	old := api.realtimeCursors[first.Cursor]
	old.Expires = time.Now().Add(-time.Second)
	api.realtimeCursors[first.Cursor] = old
	api.realtimeMu.Unlock()
	if reply := realtimeTestSync(t, api, token, first.Cursor, view); !reply.Reset {
		t.Fatal("expired cursor did not reset")
	}
	if reply := realtimeTestSync(t, api, token, "forged", view); !reply.Reset {
		t.Fatal("unknown cursor did not reset")
	}
	_, _, second := loginTestAccount(t, api.store, "admin")
	if reply := realtimeTestSync(t, api, second, first.Cursor, view); !reply.Reset {
		t.Fatal("cursor not bound to browser session")
	}
	key := communityKey(t, api, token, "library")
	if w := callAPIWithKey(api, "POST", "/api/v2/community/sync", `{"view":{}}`, key); w.Code != 403 {
		t.Fatalf("key accessed realtime: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "http://example.com/api/v2/community/sync", strings.NewReader(`{"view":{}}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	for _, body := range []string{`{"view":{"offset":-1}}`, `{"view":{"owner_playlist":"../../admin"}}`, `{"view":{},"unknown":true}`} {
		if w := sessionRequest(api, token, "POST", "/api/v2/community/sync", body); w.Code != 400 {
			t.Fatalf("invalid view: %d", w.Code)
		}
	}
}
