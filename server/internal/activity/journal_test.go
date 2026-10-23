package activity

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestJournalRotationRestartAndFiltering(t *testing.T) {
	dir := t.TempDir()
	j, err := open(dir, 700, 3, 20)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if err := j.Emit("upload.ready", Fields{Bytes: int64(i), Timings: map[string]int64{"probe_ms": int64(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	last := j.Query(Query{Limit: 5, Category: "upload"})
	if len(last.Entries) != 5 || !last.HasMore {
		t.Fatalf("pagination: %+v", last)
	}
	older := j.Query(Query{Limit: 5, Before: last.Oldest, Category: "upload"})
	if len(older.Entries) != 5 || older.Newest >= last.Oldest {
		t.Fatal("older page overlaps")
	}
	if err := j.Emit("request.failed", Fields{Status: 500}); err != nil {
		t.Fatal(err)
	}
	delta := j.Query(Query{After: last.Newest, Limit: 5})
	if len(delta.Entries) != 1 || delta.Entries[0].Level != "error" {
		t.Fatal("incremental query incorrect")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "activity*.jsonl"))
	if err != nil || len(files) > 3 {
		t.Fatal("rotation did not bound disk usage")
	}
	j, err = open(dir, 700, 3, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	recovered := j.Query(Query{Limit: 20, Level: "error"})
	if len(recovered.Entries) != 1 || recovered.Entries[0].Status != 500 {
		t.Fatal("restart lost recent journal")
	}
}

func TestJournalNeverRecordsRawSecretsAndSupportsConcurrentEvents(t *testing.T) {
	dir := t.TempDir()
	j, err := open(dir, 1<<20, 3, 200)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	secret := "private-password-key-cookie-never-log"
	if err := j.Emit(secret, Fields{UserID: secret, RequestID: secret, Route: "/api/v2/tracks/" + secret + "?api_key=" + secret, Method: secret, Timings: map[string]int64{secret: 1}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = j.Emit("request.completed", Fields{Route: "/api/v2/auth/keys/secret-value?token=secret-value", Status: 200})
		}()
	}
	wg.Wait()
	events := j.Query(Query{Limit: 100})
	ids := map[string]bool{}
	for _, entry := range events.Entries {
		if ids[entry.ID] {
			t.Fatal("duplicate journal identity")
		}
		ids[entry.ID] = true
	}
	raw, err := os.ReadFile(filepath.Join(dir, "activity.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "secret-value") {
		t.Fatal("untrusted fields leaked into persistent logs")
	}
}

func TestJournalStorageFailureKeepsBoundedMemoryAndReportsWarning(t *testing.T) {
	j, err := open(t.TempDir(), 1<<20, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	_ = j.file.Close()
	for i := 0; i < 20; i++ {
		if err := j.Emit("server.start", Fields{}); err == nil {
			t.Fatal("write failure was hidden")
		}
	}
	page := j.Query(Query{Limit: 100})
	if len(page.Entries) != 10 || page.Warning == "" {
		t.Fatal("journal failure lost diagnostics or memory bound")
	}
}
