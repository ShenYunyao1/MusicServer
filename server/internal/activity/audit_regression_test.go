package activity

import (
	"os"
	"testing"
)

func TestAuditRoutesAndCrashTail(t *testing.T) {
	for _, route := range []string{"/api/v2/community/events", "/api/v2/browse/tracks"} {
		if SafeRoute(route) == "/api/v2/unknown" {
			t.Fatalf("unknown route %s", route)
		}
	}
	directory := t.TempDir()
	j, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.Emit("server.start", Fields{}); err != nil {
		t.Fatal(err)
	}
	j.Close()
	file, err := os.OpenFile(j.path(0), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(`{"partial":`)
	file.Close()
	j, err = Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.Emit("server.stop", Fields{}); err != nil {
		t.Fatal(err)
	}
	j.Close()
	j, err = Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	page := j.Query(Query{Limit: 10})
	if len(page.Entries) != 2 {
		t.Fatalf("lost completed entry after crash tail: %+v", page)
	}
}
