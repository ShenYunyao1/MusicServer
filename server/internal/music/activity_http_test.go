package music

import (
	"encoding/json"
	"strings"
	"testing"

	"musicserver/internal/activity"
)

func TestSystemLogsAreAdminOnlyAndNeverContainCredentials(t *testing.T) {
	api, key := testAPI(t)
	journal, err := activity.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	api.store.SetActivity(journal)
	_, _, token := loginTestAccount(t, api.store, "admin")
	if err := journal.Emit("upload.ready", activity.Fields{Bytes: 1234, Timings: map[string]int64{"probe_ms": 8}}); err != nil {
		t.Fatal(err)
	}
	w := sessionRequest(api, token, "GET", "/api/v2/admin/logs?category=upload", "")
	if w.Code != 200 {
		t.Fatalf("log listing: %d %s", w.Code, w.Body)
	}
	var page activity.Page
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Entries) != 1 {
		t.Fatalf("bad log page %v", err)
	}
	if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), key) {
		t.Fatal("credential in activity log")
	}
	member, err := api.store.createUser("log-member", "member password for logs", "member", false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, memberToken, err := api.store.Login(member.Username, "member password for logs", "log-member")
	if err != nil {
		t.Fatal(err)
	}
	if denied := sessionRequest(api, memberToken, "GET", "/api/v2/admin/logs", ""); denied.Code != 403 {
		t.Fatalf("member read administrator journal: %d", denied.Code)
	}
	if bad := sessionRequest(api, token, "GET", "/api/v2/admin/logs?limit=9999", ""); bad.Code != 400 {
		t.Fatal("unbounded journal listing allowed")
	}
}
