package music

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRound2AdminOwnPlaylistReadAvoidsWriter(t *testing.T) {
	a, _ := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	if err := a.store.PutPlaylist(Playlist{ID: "own-reader", Name: "Own"}, MutationOptions{UserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	a.store.writeMu.Lock()
	done := make(chan int, 1)
	go func() {
		done <- browseTestRequest(a, u, "GET", "/api/v2/browse/playlists/own-reader/tracks", "", 0).Code
	}()
	select {
	case code := <-done:
		a.store.writeMu.Unlock()
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		a.store.writeMu.Unlock()
		<-done
		t.Fatal("administrator own read acquired writer")
	}
}
func TestRound2TagEditsDoNotInvalidateOrdinaryBrowse(t *testing.T) {
	s, auth := tagTestStore(t)
	before, err := browseAccountRevision(s.db, auth)
	if err != nil {
		t.Fatal(err)
	}
	mustLinkTag(t, s, auth, "personal", "independent-tag")
	after, err := browseAccountRevision(s.db, auth)
	if err != nil || before != after {
		t.Fatal("tag edit restarted ordinary browse", before, after, err)
	}
}

func TestRound2CredentialRevocationSignalsWithoutInvalidatingLibrary(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	auth := tagTestAuth(t, s, "admin")
	other, _ := readyMember(t, a, "credential-signal")
	before, err := browseAccountRevision(s.db, auth)
	if err != nil {
		t.Fatal(err)
	}
	unwatch := s.watchCommunity()
	defer unwatch()
	signal := s.communitySignal()
	if err := s.transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE users SET auth_version=auth_version+1 WHERE id=?", other.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after, err := browseAccountRevision(s.db, auth)
	if err != nil || before != after {
		t.Fatal("unrelated credential change invalidated library", err)
	}
	select {
	case <-signal:
	default:
		t.Fatal("credential revocation was not signaled")
	}
}
func TestRound2AuditOnlyWriteDoesNotWakeCommunity(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	unwatch := s.watchCommunity()
	defer unwatch()
	signal := s.communitySignal()
	if err := s.transaction(func(tx *sql.Tx) error {
		return auditMutation(tx, "playlist.admin.read", "test", nil, map[string]any{}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signal:
		t.Fatal("audit-only write woke all readers")
	default:
	}
}
func TestRound2CountedGuardsDoNotBlockForADay(t *testing.T) {
	a, u, auth, id := listeningFixture(t)
	now := time.Now().UTC()
	if err := a.store.transaction(func(tx *sql.Tx) error {
		for i := 0; i < 3000; i++ {
			if _, err := tx.Exec("INSERT INTO listening_sessions VALUES(?,?,?,?,30000,30000,1,?)", u.ID, fmt.Sprintf("90000000-0000-4000-8000-%012d", i), id, now.Add(-2*time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.reportListening(auth, "90000000-0000-4000-8001-000000000000", id, 0, now); err != nil {
		t.Fatal("counted guards blocked new playback", err)
	}
	receipt, err := a.store.reportListening(auth, "90000000-0000-4000-8000-000000000000", id, 30001, now)
	if err != nil || !receipt.Counted {
		t.Fatal("replay guard lost", receipt, err)
	}
}
func TestRound2ReadQueueHonorsCallerCancellation(t *testing.T) {
	a, _ := testAPI(t)
	conns := []*sql.Conn{}
	for i := 0; i < 4; i++ {
		c, err := a.store.readDatabase().Conn(t.Context())
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
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error { return nil }); err == nil || time.Since(start) > time.Second {
		t.Fatal("read queue cancellation failed", err)
	}
}

func TestRound2ExpensiveReadHonorsCallerCancellation(t *testing.T) {
	a, _ := testAPI(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := a.store.readQuery(ctx, func(reader sqlReader) error {
		var total int64
		return reader.QueryRow(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT sum(x) FROM n`).Scan(&total)
	})
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("query cancellation failed", err, time.Since(start))
	}
}

func TestRound2AuthenticationDoesNotQueueOnWriterConnection(t *testing.T) {
	a, _ := testAPI(t)
	_, _, token := loginTestAccount(t, a.store, "admin")
	c, err := a.store.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v2/auth/me", nil)
	req.Header.Set("Cookie", sessionCookie+"="+token)
	done := make(chan bool, 1)
	go func() { _, ok := a.authenticate(req); done <- ok }()
	select {
	case ok := <-done:
		c.Close()
		if !ok {
			t.Fatal("authentication failed")
		}
	case <-time.After(time.Second):
		c.Close()
		<-done
		t.Fatal("authentication queued behind writer connection")
	}
}

func TestRound2RealtimeWaiterSharesComputationAndCanCancel(t *testing.T) {
	s, auth := tagTestStore(t)
	conns := []*sql.Conn{}
	for i := 0; i < 4; i++ {
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
	first := make(chan error, 1)
	go func() { _, err := s.realtimeSnapshot(auth, realtimeView{}); first <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		s.realtimeFlightsMu.Lock()
		active := len(s.realtimeFlights)
		s.realtimeFlightsMu.Unlock()
		if active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("view computation never started")
		}
		time.Sleep(time.Millisecond)
	}
	before := s.readDatabase().Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	auth.requestContext = ctx
	if _, err := s.realtimeSnapshot(auth, realtimeView{}); err == nil {
		t.Fatal("waiter ignored cancellation")
	}
	if delta := s.readDatabase().Stats().WaitCount - before; delta > 1 {
		t.Fatal("duplicate realtime view consumed another reader", delta)
	}
	for _, c := range conns {
		c.Close()
	}
	conns = nil
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestRound2CanceledPublicAndTagReadsDoNotWaitForPool(t *testing.T) {
	s, auth := tagTestStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	auth.requestContext = ctx
	if _, err := s.ListTags(auth, TagQuery{Scope: "public", Limit: 20}); err == nil {
		t.Fatal("tag read ignored context")
	}
	if _, _, _, err := s.publicCatalog("", 0, 30, "", ctx); err == nil {
		t.Fatal("catalog ignored context")
	}
}
