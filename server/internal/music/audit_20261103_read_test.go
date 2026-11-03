package music

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAuditSubscriptionRealtimeProjectsOnlyCurrentPage(t *testing.T) {
	a, _ := testAPI(t)
	owner, _ := readyMember(t, a, "paged-source")
	listener, _ := readyMember(t, a, "paged-reader")
	ids := browseSeedTracks(t, a, owner.ID, owner.ID, 105)
	if err := a.store.PutPlaylist(Playlist{ID: "paged-subscription", Name: "Paged", TrackIDs: ids}, MutationOptions{UserID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	p, _ := a.store.Playlist("paged-subscription")
	p, err := a.store.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: owner.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Subscribe(p.ID, MutationOptions{UserID: listener.ID}); err != nil {
		t.Fatal(err)
	}
	var sid string
	a.store.db.QueryRow("SELECT id FROM sessions WHERE user_id=?", listener.ID).Scan(&sid)
	auth := authContext{session: true, userID: listener.ID, keyID: sid, role: listener.Role, authVersion: listener.AuthVersion}
	snapshot, err := a.store.realtimeSnapshot(auth, realtimeView{SubscriptionDetail: p.ID, SubscriptionOffset: 100, SubscriptionLimit: 50})
	if err != nil {
		t.Fatal(err)
	}
	section := snapshot["subscription_tracks"]
	if len(section.Rows) != 5 || section.Meta["total"] != 105 {
		t.Fatalf("unbounded page: %+v", section)
	}
	for _, raw := range section.Rows {
		if strings.Contains(string(raw), "lyrics") || strings.Contains(string(raw), "private note") {
			t.Fatal(string(raw))
		}
	}
	// The counter changes atomically even for maintenance SQL, and survives reopen.
	var before, after int64
	err = a.store.readSnapshot(t.Context(), func(tx *sql.Tx) error { var err error; before, err = browseAccountRevision(tx, auth); return err })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("UPDATE playlists SET version=version+1 WHERE id=?", p.ID); err != nil {
		t.Fatal(err)
	}
	err = a.store.readSnapshot(t.Context(), func(tx *sql.Tx) error { var err error; after, err = browseAccountRevision(tx, auth); return err })
	if err != nil || before == after {
		t.Fatal("counter missed direct SQL", err)
	}
}

func TestAuditPublicPlaybackRevisionIncludesUnsubscribedSource(t *testing.T) {
	a, _ := testAPI(t)
	owner, _ := readyMember(t, a, "source-owner")
	listener, _ := readyMember(t, a, "source-listener")
	ids := browseSeedTracks(t, a, owner.ID, owner.ID, 3)
	if err := a.store.PutPlaylist(Playlist{ID: "public-source", Name: "Source", TrackIDs: ids}, MutationOptions{UserID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	p, _ := a.store.Playlist("public-source")
	p, err := a.store.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: owner.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	first := browseTestPage(t, browseTestRequest(a, listener, "GET", "/api/v2/browse/playback/public-source?source=public&limit=1", "", 0))
	p.TrackIDs = []string{ids[2], ids[1], ids[0]}
	if err := a.store.PutPlaylist(p, MutationOptions{UserID: owner.ID, ExpectedVersion: p.Version}); err != nil {
		t.Fatal(err)
	}
	next := browseTestRequest(a, listener, "GET", fmt.Sprintf("/api/v2/browse/playback/public-source?source=public&offset=1&limit=1&revision=%.0f", first["revision"]), "", 0)
	if next.Code != 409 {
		t.Fatalf("source changed without restart: %d %s", next.Code, next.Body)
	}
}

func TestAuditBrowseReadDoesNotAcquireWriteMutex(t *testing.T) {
	a, _ := testAPI(t)
	user, _ := readyMember(t, a, "read-without-write")
	a.store.writeMu.Lock()
	done := make(chan int, 1)
	go func() { done <- browseTestRequest(a, user, "GET", "/api/v2/browse/summary", "", 0).Code }()
	select {
	case code := <-done:
		a.store.writeMu.Unlock()
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		a.store.writeMu.Unlock()
		<-done
		t.Fatal("browse read waited for writeMu")
	}
}

func TestAuditCoverLookupUsesExpressionIndexes(t *testing.T) {
	a, _ := testAPI(t)
	for _, table := range []string{"tracks", "playlists"} {
		rows, err := a.store.db.Query("EXPLAIN QUERY PLAN SELECT id FROM "+table+" WHERE json_valid(payload) AND json_extract(payload,'$.cover_id')=?", "example")
		if err != nil {
			t.Fatal(err)
		}
		indexed := false
		for rows.Next() {
			var x, y, z int
			var detail string
			if err := rows.Scan(&x, &y, &z, &detail); err != nil {
				t.Fatal(err)
			}
			indexed = indexed || strings.Contains(detail, "INDEX")
		}
		rows.Close()
		if !indexed {
			t.Errorf("%s cover lookup scans payloads", table)
		}
	}
}
