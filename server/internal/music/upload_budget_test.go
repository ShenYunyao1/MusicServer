package music

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestTemporaryUploadBudgetCombinesResumeAndStageAndReleasesAfterCancel(t *testing.T) {
	a, _ := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	audio := validTestAudio(t)
	limits := defaultTemporaryUploadLimits()
	limits.OwnerBytes = int64(len(audio)) + 299
	limits.GlobalBytes = limits.OwnerBytes
	a.store.tempLimits = &limits
	task := resumeCreate(t, a, u, 300)
	if _, err := a.store.StageAudio("budget.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: u.ID}); !errors.Is(err, errUploadTemporaryLimit) {
		t.Fatalf("stage bypassed outstanding resume reservation: %v", err)
	}
	auth := tagTestAuth(t, a.store, "admin")
	if err := a.store.cancelResume(auth, task); err != nil {
		t.Fatal(err)
	}
	p, err := a.store.StageAudio("budget.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: u.ID})
	if err != nil {
		t.Fatal("cancel did not release budget", err)
	}
	if err := a.store.DeleteUploadStage(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.StageAudio("budget.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: u.ID}); err != nil {
		t.Fatal("stage deletion did not release budget", err)
	}
}

func TestTemporaryUploadConcurrentReservationsAreBounded(t *testing.T) {
	a, _ := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	limits := defaultTemporaryUploadLimits()
	limits.OwnerBytes = 100
	limits.GlobalBytes = 100
	a.store.tempLimits = &limits
	var wg sync.WaitGroup
	results := make(chan func(), 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			release, err := a.store.reserveTemporaryUpload(u.ID, fmt.Sprint(i), 30)
			if err == nil {
				results <- release
			} else if !errors.Is(err, errUploadTemporaryLimit) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	close(results)
	if len(results) != 3 {
		t.Fatalf("admitted %d reservations; expected exactly 3", len(results))
	}
	for release := range results {
		release()
	}
	release, err := a.store.reserveTemporaryUpload(u.ID, "again", 100)
	if err != nil {
		t.Fatal("failed reservation leaked capacity", err)
	}
	release()
}

func TestTemporaryUploadMetadataBoundAndShrinkingCleanup(t *testing.T) {
	a, _ := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	limits := defaultTemporaryUploadLimits()
	limits.OwnerMetadata = 50
	limits.GlobalMetadata = 50
	err := a.store.transaction(func(tx *sql.Tx) error {
		return checkTemporaryMetadata(tx, u.ID, "", "", 51, limits)
	})
	if !errors.Is(err, errUploadTemporaryLimit) {
		t.Fatal("unbounded metadata", err)
	}
	limits.OwnerMetadata = 1
	limits.GlobalMetadata = 1
	task := resumeCreate(t, a, u, 100)
	// Existing rows are retained after a policy decrease; cancellation must be
	// able to shrink an over-budget checkpoint rather than trapping its bytes.
	a.store.tempLimits = &limits
	if err := a.store.cancelResume(tagTestAuth(t, a.store, "admin"), task); err != nil {
		t.Fatal(err)
	}
}

func TestTemporaryUploadMetadataRejectsNewStageWithoutLeakingAudio(t *testing.T) {
	a, root := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	audio := validTestAudio(t)
	p, err := a.store.StageAudio("first.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	var used int64
	if err := a.store.db.QueryRow("SELECT length(CAST(track_json AS BLOB)) FROM upload_staging WHERE id=?", p.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	limits := defaultTemporaryUploadLimits()
	limits.OwnerMetadata = used + 1
	limits.GlobalMetadata = used + 1
	a.store.tempLimits = &limits
	if _, err := a.store.StageAudio("second.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: u.ID}); !errors.Is(err, errUploadTemporaryLimit) {
		t.Fatal("metadata admission escaped", err)
	}
	files, err := filepath.Glob(filepath.Join(root, ".staging", "*.wav"))
	if err != nil || len(files) != 1 {
		t.Fatal("rejected metadata leaked audio", files, err)
	}
	if _, ok, err := a.store.UploadStage(p.ID); err != nil || !ok {
		t.Fatal("old preview damaged", err)
	}
}

func TestTemporaryUploadCountsOrphanBytesAndExistingResumeAfterReopen(t *testing.T) {
	a, root := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	task := resumeCreate(t, a, u, 80)
	if err := os.WriteFile(filepath.Join(root, ".staging", "orphan.wav"), make([]byte, 30), 0600); err != nil {
		t.Fatal(err)
	}
	// A second offline handle reconstructs accounting from files/checkpoints;
	// it does not rely on the original runtime's reservation map.
	s, err := NewOfflineStore(a.store.dataPath, a.store.mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	limits := defaultTemporaryUploadLimits()
	limits.OwnerBytes = 100
	limits.GlobalBytes = 100
	s.tempLimits = &limits
	if release, err := s.reserveTemporaryUpload(u.ID, task.ID, 0); !errors.Is(err, errUploadTemporaryLimit) {
		if release != nil {
			release()
		}
		t.Fatal("orphan bytes bypassed global limit", err)
	}
	if err := os.Remove(filepath.Join(root, ".staging", "orphan.wav")); err != nil {
		t.Fatal(err)
	}
	if release, err := s.reserveTemporaryUpload(u.ID, "new", 21); !errors.Is(err, errUploadTemporaryLimit) {
		if release != nil {
			release()
		}
		t.Fatal("persisted full-size resume reservation forgotten", err)
	}
}
