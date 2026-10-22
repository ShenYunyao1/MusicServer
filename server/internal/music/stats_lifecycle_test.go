package music

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestStatsFollowKeyCreationRenameAndRevocation(t *testing.T) {
	api, _ := testAPI(t)
	key, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "New player"})
	stats := api.readCallStats()
	if stats.err != nil || len(stats.Keys) != 1 || stats.Keys[0].KeyID != key.ID || stats.Total.TotalRequests != 0 {
		t.Fatalf("new key must have a zero statistics row: %+v", stats)
	}
	if response := callAPIWithKey(api, "GET", "/api/v2/playlists", "", secret); response.Code != http.StatusOK {
		t.Fatal(response.Body)
	}
	name := "Renamed player"
	if _, err := api.store.UpdateAPIKey(key.ID, APIKeyUpdateRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	stats = api.readCallStats()
	encoded, _ := json.Marshal(stats.Keys[0])
	var row map[string]any
	if err := json.Unmarshal(encoded, &row); err != nil {
		t.Fatal(err)
	}
	if row["key_name"] != name || stats.Total.TotalRequests != 1 {
		t.Fatalf("renamed key stats: %+v", stats)
	}
	if err := api.store.RevokeAPIKey(key.ID); err != nil {
		t.Fatal(err)
	}
	// A request that finishes after revocation must not recreate deleted statistics.
	api.recordCall(key.ID, 200, 100, time.Millisecond)
	api.concurrencyMu.Lock()
	api.active[key.ID] = 1
	api.concurrencyMu.Unlock()
	stats = api.readCallStats()
	if stats.err != nil || len(stats.Keys) != 0 || stats.Total.TotalRequests != 0 || stats.Total.ActiveRequests != 0 {
		t.Fatalf("revoked key leaked into stats: %+v", stats)
	}
	var count int
	if err := api.store.db.QueryRow("SELECT count(*) FROM api_call_stats WHERE key_id=?", key.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("revoked key statistics still persisted: %d %v", count, err)
	}
	if _, err := api.store.RotateAPIKey(key.ID); err != nil {
		t.Fatal(err)
	}
	stats = api.readCallStats()
	if len(stats.Keys) != 1 || stats.Total.TotalRequests != 0 {
		t.Fatalf("reactivated key should start at zero: %+v", stats)
	}
}

func TestStatsStartupPrunesLegacyRowsAndKeepsCurrentKeyCalls(t *testing.T) {
	api, _ := testAPI(t)
	key, _ := createPersistentKey(t, api, APIKeyCreateRequest{Name: "Keep"})
	api.recordCall(key.ID, 200, 123, time.Millisecond)
	for _, id := range []string{"startup-admin", "anonymous", "old-session"} {
		if _, err := api.store.db.Exec("INSERT INTO api_call_stats(key_id,total_requests) VALUES(?,99)", id); err != nil {
			t.Fatal(err)
		}
	}
	if err := api.store.BeginServer(); err != nil {
		t.Fatal(err)
	}
	stats := api.readCallStats()
	if stats.err != nil || len(stats.Keys) != 1 || stats.Keys[0].KeyID != key.ID || stats.Total.TotalRequests != 1 {
		t.Fatalf("legacy rows or current counts were wrong: %+v", stats)
	}
	var count int
	if err := api.store.db.QueryRow("SELECT count(*) FROM api_call_stats").Scan(&count); err != nil || count != 1 {
		t.Fatalf("startup did not prune old rows: %d %v", count, err)
	}
}

func TestBulkKeyRevocationAlsoRemovesStats(t *testing.T) {
	api, _ := testAPI(t)
	key, _ := createPersistentKey(t, api, APIKeyCreateRequest{Name: "Player"})
	api.recordCall(key.ID, 200, 1, time.Millisecond)
	if err := api.store.RevokeAccountKeys(key.UserID); err != nil {
		t.Fatal(err)
	}
	if stats := api.readCallStats(); stats.err != nil || len(stats.Keys) != 0 || stats.Total.TotalRequests != 0 {
		t.Fatalf("bulk-revoked key remains in stats: %+v", stats)
	}
}

func TestStatsCleanupFailureRollsBackKeyRevocation(t *testing.T) {
	api, _ := testAPI(t)
	key, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "Player"})
	api.recordCall(key.ID, 200, 1, time.Millisecond)
	if _, err := api.store.db.Exec("CREATE TRIGGER reject_stats_delete BEFORE DELETE ON api_call_stats BEGIN SELECT RAISE(ABORT,'test cleanup failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := api.store.RevokeAPIKey(key.ID); err == nil {
		t.Fatal("revocation ignored statistics cleanup failure")
	}
	if _, ok := api.store.AuthenticateSecret(secret); !ok {
		t.Fatal("failed transaction revoked key")
	}
	if stats := api.readCallStats(); stats.Total.TotalRequests != 1 {
		t.Fatalf("failed transaction lost stats: %+v", stats)
	}
}
