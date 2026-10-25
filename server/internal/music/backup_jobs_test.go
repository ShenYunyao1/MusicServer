package music

import (
	"encoding/json"
	"testing"
	"time"
)

func waitBackupJob(t *testing.T, s *Store) BackupJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job := s.BackupJob()
		if job.State != "running" && job.State != "cancelling" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("backup job did not finish")
	return BackupJob{}
}

func TestManagedBackupJobDownloadDeleteAndPolicy(t *testing.T) {
	api, _ := testAPI(t)
	policy, err := api.store.BackupPolicy()
	if err != nil || policy.Enabled || policy.KeepLast != 3 {
		t.Fatal("unexpected default backup policy")
	}
	r := callAPI(api, "POST", "/api/v2/admin/backups", `{"include_media":true}`)
	if r.Code != 202 {
		t.Fatalf("start backup: %d %s", r.Code, r.Body)
	}
	job := waitBackupJob(t, api.store)
	if job.State != "completed" || job.Archive == nil {
		t.Fatalf("backup failed: %+v", job)
	}
	base := "/api/v2/admin/backups/" + job.Archive.ID
	download := callAPI(api, "GET", base+"/download", "")
	if download.Code != 200 || int64(download.Body.Len()) != job.Archive.Size || download.Header().Get("Content-Disposition") == "" {
		t.Fatalf("download: %d", download.Code)
	}
	api.store.backupMu.Lock()
	api.store.backupReaders = map[string]int{job.Archive.ID: 1}
	api.store.backupMu.Unlock()
	if r := callAPI(api, "DELETE", base, ""); r.Code != 409 {
		t.Fatalf("deleted active download: %d", r.Code)
	}
	api.store.backupMu.Lock()
	delete(api.store.backupReaders, job.Archive.ID)
	api.store.backupMu.Unlock()
	if r := callAPI(api, "DELETE", base, ""); r.Code != 204 {
		t.Fatalf("delete: %d", r.Code)
	}
	if r := callAPI(api, "GET", base+"/download", ""); r.Code != 404 {
		t.Fatalf("deleted archive downloaded: %d", r.Code)
	}
	if r := callAPI(api, "PUT", "/api/v2/admin/backups/policy", `{"enabled":true,"interval_hours":12,"keep_last":2,"include_media":false}`); r.Code != 200 {
		t.Fatalf("policy: %d", r.Code)
	}
	stored, _ := api.store.BackupPolicy()
	if !stored.Enabled || stored.IntervalHours != 12 || stored.KeepLast != 2 || stored.IncludeMedia {
		t.Fatal("backup policy was not persisted")
	}
	if r := callAPI(api, "PUT", "/api/v2/admin/backups/policy", `{"enabled":true,"interval_hours":0,"keep_last":2,"include_media":false}`); r.Code != 400 {
		t.Fatalf("invalid policy: %d", r.Code)
	}
}

func TestManagedBackupCancelWhileWaitingForMedia(t *testing.T) {
	api, _ := testAPI(t)
	api.store.mediaMu.Lock()
	defer api.store.mediaMu.Unlock()
	r := callAPI(api, "POST", "/api/v2/admin/backups", `{"include_media":true}`)
	if r.Code != 202 {
		t.Fatal(r.Code)
	}
	var job BackupJob
	json.Unmarshal(r.Body.Bytes(), &job)
	if r := callAPI(api, "POST", "/api/v2/admin/backups", `{}`); r.Code != 409 {
		t.Fatalf("parallel backup allowed: %d", r.Code)
	}
	if r := callAPI(api, "DELETE", "/api/v2/admin/backups/jobs/"+job.ID, ""); r.Code != 204 {
		t.Fatal(r.Code)
	}
	if got := waitBackupJob(t, api.store); got.State != "cancelled" {
		t.Fatalf("cancelled backup: %+v", got)
	}
}

func TestManagedBackupSchedulerPrunesOnlyAutomaticArchives(t *testing.T) {
	api, _ := testAPI(t)
	admin, _, _ := loginTestAccount(t, api.store, "admin")
	if err := api.store.MaybeRunScheduledBackup(time.Now()); err != nil || api.store.BackupJob().ID != "" {
		t.Fatal("disabled scheduler ran")
	}
	if _, err := api.store.StartBackup(true, false, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	manual := waitBackupJob(t, api.store)
	if manual.Archive == nil {
		t.Fatal(manual.Error)
	}
	if err := api.store.SetBackupPolicy(BackupPolicy{Enabled: true, IntervalHours: 1, KeepLast: 1, IncludeMedia: true}, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		for _, key := range []string{"backup_last_success", "backup_last_attempt"} {
			if _, err := api.store.db.Exec("UPDATE meta SET value=? WHERE key=?", persistedTime(time.Now().Add(-2*time.Hour)), key); err != nil {
				t.Fatal(err)
			}
		}
		if err := api.store.MaybeRunScheduledBackup(time.Now()); err != nil {
			t.Fatal(err)
		}
		if job := waitBackupJob(t, api.store); job.State != "completed" || job.Archive == nil || !job.Archive.Automatic {
			t.Fatalf("scheduled backup: %+v", job)
		}
	}
	items, err := api.store.BackupArchives()
	if err != nil || len(items) != 2 {
		t.Fatalf("retention: %d %v", len(items), err)
	}
	manualExists := false
	for _, item := range items {
		manualExists = manualExists || item.ID == manual.Archive.ID
	}
	if !manualExists {
		t.Fatal("retention deleted a manual archive")
	}
	last := api.store.BackupJob().ID
	if err := api.store.MaybeRunScheduledBackup(time.Now()); err != nil || api.store.BackupJob().ID != last {
		t.Fatal("scheduler ignored interval")
	}
}
