package music

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"musicserver/internal/diskspace"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStorageConcurrentQuotaAndMidWriteLowSpace(t *testing.T) {
	api, root := testAPI(t)
	member, _ := readyMember(t, api, "quota-concurrent")
	admin, _, _ := loginTestAccount(t, api.store, "admin")
	audio := validTestAudio(t)
	second := append([]byte(nil), audio...)
	second[len(second)-1] ^= 1
	if _, err := api.store.SetStorageQuota(member.ID, int64(len(audio)), 0, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	first, err := api.store.StageAudio("one.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	next, err := api.store.StageAudio("two.wav", bytes.NewReader(second), nil, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{first.ID, next.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, _, err := api.store.CommitUploadStage(id, MutationOptions{UserID: member.ID})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success, rejected := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, errStorageQuota) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatalf("concurrent quota escaped: %d/%d", success, rejected)
	}
	files, _ := filepath.Glob(filepath.Join(root, "uploads", "*.wav"))
	if len(files) != 1 {
		t.Fatal("failed quota commit leaked a published file")
	}
	checks := 0
	api.store.spaceProbe = func(string) (diskspace.Space, error) {
		checks++
		if checks > 1 {
			return diskspace.Space{Available: 0}, nil
		}
		return diskspace.Space{Available: 1 << 40}, nil
	}
	if _, err := api.store.StageAudio("low-mid.wav", bytes.NewReader(audio), nil); !errors.Is(err, errStorageLow) {
		t.Fatalf("mid-write low space not rejected: %v", err)
	}
	var stages int
	api.store.db.QueryRow("SELECT count(*) FROM upload_staging").Scan(&stages)
	staged, _ := filepath.Glob(filepath.Join(root, ".staging", "*"))
	if len(staged) != stages {
		t.Fatal("mid-write failure leaked a file")
	}
}

func TestStorageLowWaterPreservesDataAndCleansFailedUploads(t *testing.T) {
	api, root := testAPI(t)
	api.store.spaceProbe = func(string) (diskspace.Space, error) { return diskspace.Space{Total: 1 << 30, Available: 1}, nil }
	if _, err := api.store.StageAudio("low.wav", bytes.NewReader(validTestAudio(t)), nil); !errors.Is(err, errStorageLow) {
		t.Fatalf("low disk accepted upload: %v", err)
	}
	if _, err := api.store.CreateBackupBundle(context.Background(), true, nil); !errors.Is(err, errStorageLow) {
		t.Fatalf("low disk accepted backup: %v", err)
	}
	if len(api.store.Tracks()) != 1 {
		t.Fatal("low disk modified library")
	}
	files, _ := filepath.Glob(filepath.Join(root, ".staging", "*"))
	if len(files) != 0 {
		t.Fatal("failed upload leaked temporary file")
	}
}

func TestStorageSeparateDatabaseVolumeLowWater(t *testing.T) {
	api, _ := testAPI(t)
	dataDir := filepath.Clean(filepath.Dir(api.store.dataPath))
	api.store.spaceProbe = func(path string) (diskspace.Space, error) {
		if filepath.Clean(path) == dataDir {
			return diskspace.Space{Available: 1}, nil
		}
		return diskspace.Space{Available: 1 << 40}, nil
	}
	if _, err := api.store.StageAudio("low-data.wav", bytes.NewReader(validTestAudio(t)), nil); !errors.Is(err, errStorageLow) {
		t.Fatalf("database low-water bypass: %v", err)
	}
	if r := callAPI(api, "POST", "/api/v2/uploads/stage", ""); r.Code != 507 {
		t.Fatalf("database low-water receive bypass: %d", r.Code)
	}
}

func TestStorageQuotaDedupTransferAndVersionBoundary(t *testing.T) {
	api, _ := testAPI(t)
	member, _ := readyMember(t, api, "quota-a")
	other, _ := readyMember(t, api, "quota-b")
	audio := validTestAudio(t)
	admin, _, _ := loginTestAccount(t, api.store, "admin")
	options := MutationOptions{UserID: admin.ID}
	quota, err := api.store.SetStorageQuota(member.ID, 1, 0, options)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := api.store.StageAudio("quota.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.store.CommitUploadStage(preview.ID, MutationOptions{UserID: member.ID}); !errors.Is(err, errStorageQuota) {
		t.Fatalf("quota did not reject commit: %v", err)
	}
	if _, err := api.store.SetStorageQuota(member.ID, 0, 0, options); !errors.Is(err, errVersionConflict) {
		t.Fatal("stale quota update accepted")
	}
	if _, err := api.store.SetStorageQuota(member.ID, 0, quota.Version, options); err != nil {
		t.Fatal(err)
	}
	track, dup, err := api.store.CommitUploadStage(preview.ID, MutationOptions{UserID: member.ID})
	if err != nil || dup {
		t.Fatal(err)
	}
	if _, err := api.store.SetStorageQuota(other.ID, 1, 0, options); err != nil {
		t.Fatal(err)
	}
	duplicate, err := api.store.StageAudio("same.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got, dup, err := api.store.CommitUploadStage(duplicate.ID, MutationOptions{UserID: other.ID}); err != nil || !dup || got.ID != track.ID {
		t.Fatalf("dedup charged again: %v", err)
	}
	// Ownership transfer never alters the original uploader used for accounting.
	track.OwnerID = other.ID
	if err := api.store.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	overview, err := api.store.StorageOverview()
	if err != nil {
		t.Fatal(err)
	}
	var a, b int64
	for _, account := range overview.Accounts {
		if account.ID == member.ID {
			a = account.UsedBytes
		}
		if account.ID == other.ID {
			b = account.UsedBytes
		}
	}
	if a != int64(len(audio)) || b != 0 {
		t.Fatalf("provenance accounting changed: %d/%d", a, b)
	}
	if err := os.Remove(filepath.Join(api.store.mediaRoot, track.Path)); err != nil {
		t.Fatal(err)
	}
	overview, err = api.store.StorageOverview()
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range overview.Accounts {
		if account.ID == member.ID && account.UsedBytes != 0 {
			t.Fatal("missing physical file still charged")
		}
	}
}

func TestStorageRechecksDatabaseAfterPublicationCopies(t *testing.T) {
	api, root := testAPI(t)
	preview, err := api.store.StageAudio("published.wav", bytes.NewReader(validTestAudio(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Clean(filepath.Dir(api.store.dataPath))
	final := filepath.Join(root, "uploads", preview.ID+".wav")
	api.store.spaceProbe = func(path string) (diskspace.Space, error) {
		if filepath.Clean(path) == dataDir {
			if stat, err := os.Stat(final); err == nil && stat.Size() > 0 {
				return diskspace.Space{Available: 1}, nil
			}
		}
		return diskspace.Space{Available: 1 << 40}, nil
	}
	if _, _, err := api.store.CommitUploadStage(preview.ID); !errors.Is(err, errStorageLow) {
		t.Fatalf("publication copy bypassed data reserve: %v", err)
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatal("failed data-space check left a published copy")
	}
	if len(api.store.Tracks()) != 1 {
		t.Fatal("failed data-space check changed library")
	}
}

func TestStorageAdminBoundary(t *testing.T) {
	api, _ := testAPI(t)
	member, token := readyMember(t, api, "quota-member")
	if r := callAPI(api, "GET", "/api/v2/admin/storage", ""); r.Code != 200 {
		t.Fatalf("storage overview: %d", r.Code)
	}
	if r := sessionRequest(api, token, "GET", "/api/v2/admin/storage", ""); r.Code != 403 {
		t.Fatalf("member storage access: %d", r.Code)
	}
	if _, err := api.store.SetStorageQuota(member.ID, 1024, 0, MutationOptions{UserID: member.ID}); err == nil {
		t.Fatal("member changed quota")
	}
}
