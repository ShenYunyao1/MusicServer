package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRound3RealtimeLeaderCancellationDoesNotCancelFollower(t *testing.T) {
	s, auth := tagTestStore(t)
	var conns []*sql.Conn
	for range 4 {
		c, err := s.readDatabase().Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	leader := auth
	leader.requestContext = ctx
	first := make(chan error, 1)
	next := make(chan error, 1)
	go func() { _, err := s.realtimeSnapshot(leader, realtimeView{}); first <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		s.realtimeFlightsMu.Lock()
		n := len(s.realtimeFlights)
		s.realtimeFlightsMu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no flight")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { _, err := s.realtimeSnapshot(auth, realtimeView{}); next <- err }()
	deadline = time.Now().Add(time.Second)
	for {
		s.realtimeFlightsMu.Lock()
		waiters := 0
		for _, flight := range s.realtimeFlights {
			waiters += flight.waiters
		}
		s.realtimeFlightsMu.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("follower never joined shared flight")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-first; err == nil {
		t.Fatal("leader ignored cancellation")
	}
	for _, c := range conns {
		c.Close()
	}
	conns = nil
	if err := <-next; err != nil {
		t.Fatal("live follower lost shared computation", err)
	}
}

func TestRound3RealtimeCapacityIsRetryable(t *testing.T) {
	a, _ := testAPI(t)
	_, _, token := loginTestAccount(t, a.store, "admin")
	a.store.realtimeFlights = map[string]*realtimeFlight{}
	for n := range 64 {
		a.store.realtimeFlights[fmt.Sprint(n)] = &realtimeFlight{done: make(chan struct{})}
	}
	for _, route := range []string{"/api/v2/community/sync", "/api/v2/community/events"} {
		method, body := "POST", `{"view":{}}`
		if route == "/api/v2/community/events" {
			method, body = "GET", ""
		}
		w := mailRequest(a, token, method, route, body, 0)
		if w.Code != 503 || w.Header().Get("Retry-After") == "" || w.Header().Get("X-Error-Code") != "REALTIME_BUSY" {
			t.Errorf("%s: %d %s", route, w.Code, w.Body)
		}
	}
	a.store.realtimeFlights = nil
}

func TestRound3StorageOverviewReadsProjectionUntilScan(t *testing.T) {
	a, root := testAPI(t)
	s := a.store
	track := s.Tracks()[0]
	before, err := s.StorageOverview()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, track.Path)); err != nil {
		t.Fatal(err)
	}
	after, err := s.StorageOverview()
	if err != nil {
		t.Fatal(err)
	}
	if after.MediaBytes != before.MediaBytes {
		t.Fatal("GET reconciled external files instead of using projection", before.MediaBytes, after.MediaBytes)
	}
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	after, err = s.StorageOverview()
	if err != nil {
		t.Fatal(err)
	}
	if after.MediaBytes >= before.MediaBytes {
		t.Fatal("scan did not reconcile usage")
	}
	raw, _ := json.Marshal(after)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	if stamp, _ := fields["usage_refreshed_at"].(string); stamp == "" {
		t.Fatal("successful scan timestamp absent")
	}
	conn, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.StorageOverview(); done <- err }()
	select {
	case err := <-done:
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		conn.Close()
		<-done
		t.Fatal("capacity read queued on sole writer")
	}
}
