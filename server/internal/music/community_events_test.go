package music

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRealtimeSSEFlushScopeRevocationAndNoActivity(t *testing.T) {
	api, author, reader, p := sharingFixture(t)
	bt := communityLogin(t, api.store, reader.Username)
	_, session, ok := api.store.sessionIdentity(bt)
	if !ok {
		t.Fatal("missing session")
	}
	server := httptest.NewServer(api)
	ctx, cancel := context.WithCancel(context.Background())
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+`/api/v2/community/events?view=%7B%22invitations%22%3Atrue%7D`, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: bt})
	response, err := server.Client().Do(r)
	if err != nil {
		cancel()
		server.Close()
		t.Fatal(err)
	}
	defer func() { cancel(); response.Body.Close(); server.Close() }()
	if response.StatusCode != 200 {
		t.Fatalf("SSE unavailable: %d", response.StatusCode)
	}
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("not an SSE response")
	}
	events := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				select {
				case events <- strings.TrimPrefix(line, "event: "):
				default:
				}
			}
		}
	}()
	wait := func(wanted string) {
		t.Helper()
		select {
		case event := <-events:
			if event != wanted {
				t.Fatalf("event %q, wanted %q", event, wanted)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("event %q not flushed", wanted)
		}
	}
	wait("ready")
	if err := api.store.PutPlaylist(Playlist{ID: "unrelated-private", Name: "Hidden"}, MutationOptions{UserID: author.ID, CreateOnly: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("unrelated event sent: %s", event)
	case <-time.After(300 * time.Millisecond):
	}
	_, err = api.store.CreateInvitation(p.ID, reader.Username, MutationOptions{UserID: author.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	wait("change")
	_, after, ok := api.store.sessionIdentity(bt)
	if !ok || !after.LastActiveAt.Equal(session.LastActiveAt) {
		t.Fatal("SSE extended login activity")
	}
	if err := api.store.RevokeSession(reader.ID, session.ID); err != nil {
		t.Fatal(err)
	}
	wait("expired")
}

func TestRealtimeCommitSignalIgnoresReadsAndRollbacks(t *testing.T) {
	api, _ := testAPI(t)
	unwatch := api.store.watchCommunity()
	defer unwatch()
	signal := api.store.communitySignal()
	if err := api.store.transaction(func(tx *sql.Tx) error { var n int; return tx.QueryRow("SELECT count(*) FROM users").Scan(&n) }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signal:
		t.Fatal("read transaction published a change")
	default:
	}
	fixture := errors.New("rollback fixture")
	err := api.store.transaction(func(tx *sql.Tx) error {
		if err := auditMutation(tx, "test.rollback", "id", nil, nil, nil); err != nil {
			return err
		}
		return fixture
	})
	if !errors.Is(err, fixture) {
		t.Fatal(err)
	}
	select {
	case <-signal:
		t.Fatal("failed transaction published a change")
	default:
	}
	if err := api.store.PutPlaylist(Playlist{ID: "commit-signal", Name: "Committed"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signal:
	default:
		t.Fatal("committed mutation did not notify")
	}
}

func TestRealtimeSSELimitsAndKeyRejection(t *testing.T) {
	api, _ := testAPI(t)
	user, _, token := loginTestAccount(t, api.store, "admin")
	api.realtimeStreams = map[string]int{user.ID: 8}
	if w := sessionRequest(api, token, "GET", "/api/v2/community/events", ""); w.Code != 429 {
		t.Fatalf("unbounded SSE: %d", w.Code)
	}
	key := communityKey(t, api, token, "library")
	if w := communityPlayer(api, key, "community/events"); w.Code != 403 {
		t.Fatalf("key opened SSE: %d", w.Code)
	}
	if w := sessionRequest(api, token, "GET", "/api/v2/community/events?view=invalid", ""); w.Code != 400 {
		t.Fatal("invalid SSE view accepted")
	}
}
