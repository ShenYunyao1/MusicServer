package music

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAuditListeningSearchUsesUpdatedLightweightProjection(t *testing.T) {
	a, u, auth, id := listeningFixture(t)
	now := time.Now().UTC()
	if _, err := a.store.db.Exec("INSERT INTO listening_totals VALUES(?,?,1,?)", u.ID, id, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	track, _ := a.store.Track(id)
	track.Artist = "projectionneedle"
	track.Lyrics = strings.Repeat("irrelevant lyrics", 10000)
	if err := a.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	var size int
	if err := a.store.db.QueryRow("SELECT length(search_text) FROM browse_track_search WHERE track_id=?", id).Scan(&size); err != nil || size > 1000 {
		t.Fatal("large payload in search projection", size, err)
	}
	page, err := a.store.listListening(auth, listeningQuery{period: "all", limit: 20, search: "projectionneedle"}, now)
	if err != nil || page["total"] != 1 {
		t.Fatal(page, err)
	}
	track.Artist = "replacement"
	if err := a.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	page, err = a.store.listListening(auth, listeningQuery{period: "all", limit: 20, search: "projectionneedle"}, now)
	if err != nil || page["total"] != 0 {
		t.Fatal("stale search projection", page, err)
	}
}

func TestAuditReadSnapshotDoesNotOccupyWriterConnection(t *testing.T) {
	a, _ := testAPI(t)
	entered, release := make(chan struct{}), make(chan struct{})
	readDone, writeDone := make(chan error, 1), make(chan error, 1)
	go func() {
		readDone <- a.store.readSnapshot(context.Background(), func(tx *sql.Tx) error {
			var n int
			if err := tx.QueryRow("SELECT count(*) FROM tracks").Scan(&n); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("reader did not start")
	}
	go func() {
		writeDone <- a.store.transaction(func(tx *sql.Tx) error {
			_, err := tx.Exec("INSERT INTO meta(key,value) VALUES('test-concurrent-writer','1')")
			return err
		})
	}()
	select {
	case err := <-writeDone:
		close(release)
		if readErr := <-readDone; readErr != nil {
			t.Fatal(readErr)
		}
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(release)
		<-readDone
		<-writeDone
		t.Fatal("reader occupied sole writer connection")
	}
}

func TestAuditReadSnapshotIsActuallyReadOnly(t *testing.T) {
	a, _ := testAPI(t)
	err := a.store.readSnapshot(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO meta(key,value) VALUES('forbidden-read-write','1')")
		return err
	})
	if err == nil {
		t.Fatal("read snapshot permitted writes")
	}
}

func TestAuditIdleUncountedListeningSessionsDoNotLockAccountForADay(t *testing.T) {
	a, u, auth, id := listeningFixture(t)
	now := time.Now().UTC()
	err := a.store.transaction(func(tx *sql.Tx) error {
		for i := 0; i < 3000; i++ {
			if _, err := tx.Exec("INSERT INTO listening_sessions VALUES(?,?,?,?,0,30000,0,?)", u.ID, fmt.Sprintf("90000000-0000-4000-8000-%012d", i), id, now.Add(-25*time.Minute).UnixMilli(), now.Add(-25*time.Minute).UnixMilli()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("UPDATE listening_sessions SET played_ms=1 WHERE user_id=?", u.ID); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE listening_sessions SET counted=1 WHERE user_id=? AND session_id='90000000-0000-4000-8000-000000000000'", u.ID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := listeningRequest(a, auth, "POST", "sessions", listeningBody("90000000-0000-4000-8001-000000000000", id, 0))
	if w.Code != 200 {
		t.Fatal("idle unconfirmed sessions locked real playback", w.Code, w.Body)
	}
	var count int
	a.store.db.QueryRow("SELECT count(*) FROM listening_sessions WHERE user_id=?", u.ID).Scan(&count)
	if count > 3000 {
		t.Fatal("guard storage escaped cap", count)
	}
	var kept bool
	if err := a.store.db.QueryRow("SELECT counted FROM listening_sessions WHERE user_id=? AND session_id='90000000-0000-4000-8000-000000000000'", u.ID).Scan(&kept); err != nil || !kept {
		t.Fatal("counted replay guard removed", err)
	}
}
