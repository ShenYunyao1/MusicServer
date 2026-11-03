package music

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditTemporaryQuotaIncludesCommittedAndPendingBytes(t *testing.T) {
	a, _ := testAPI(t)
	user, _ := readyMember(t, a, "pending-quota")
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	audio := validTestAudio(t)
	if _, err := a.store.SetStorageQuota(user.ID, int64(len(audio)), 0, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	preview, err := a.store.StageAudio("first.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := temporaryPendingBytes(a.store.db, user.ID); err != nil || pending != int64(len(audio)) {
		t.Fatal("pending upload quota not exposed", pending, err)
	}
	if _, err = a.store.StageAudio("next.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: user.ID}); !errors.Is(err, errStorageQuota) {
		t.Fatal("pending bytes escaped quota", err)
	}
	if _, _, err = a.store.CommitUploadStage(preview.ID, MutationOptions{UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.StageAudio("third.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: user.ID}); !errors.Is(err, errStorageQuota) {
		t.Fatal("committed bytes escaped admission", err)
	}
}

func TestAuditResumePromotionReusesItsReservation(t *testing.T) {
	a, _ := testAPI(t)
	user, _, _ := loginTestAccount(t, a.store, "admin")
	task := resumeCreate(t, a, user, 100)
	limits := defaultTemporaryUploadLimits()
	limits.OwnerBytes = 100
	limits.GlobalBytes = 100
	a.store.tempLimits = &limits
	release, err := a.store.reserveTemporaryUpload(user.ID, task.ID, 100)
	if err != nil {
		t.Fatal("promotion double charged", err)
	}
	release()
	if _, err = a.store.db.Exec("INSERT INTO meta(key,value) VALUES('upload_resume/invalid','broken json')"); err != nil {
		t.Fatal(err)
	}
	if pending, err := temporaryPendingBytes(a.store.db, user.ID); err != nil || pending != 100 {
		t.Fatal("preflight failed on unrelated bad checkpoint", pending, err)
	}
	release, err = a.store.reserveTemporaryUpload(user.ID, task.ID, 0)
	if err != nil {
		t.Fatal("malformed task blocked other uploads", err)
	}
	release()
}

func TestAuditResumeCapacityFailureHasNoVerificationCooldown(t *testing.T) {
	a, _ := testAPI(t)
	now := time.Now()
	id := "capacity"
	if err := a.store.admitResumeVerification(id, now); err != nil {
		t.Fatal(err)
	}
	a.store.finishResumeVerification(id, errStorageLow, now)
	if err := a.store.admitResumeVerification(id, now.Add(time.Millisecond)); err != nil {
		t.Fatal("capacity failure hidden by cooldown", err)
	}
}

func TestAuditDisabledAccountPurgeCleansUnfinishedResume(t *testing.T) {
	a, root := testAPI(t)
	user, _ := readyMember(t, a, "purge-resume")
	admin, _, _ := loginTestAccount(t, a.store, "admin")
	task := resumeCreate(t, a, user, 100)
	if err := a.store.SetUserEnabled(user.ID, false, user.Version); err != nil {
		t.Fatal(err)
	}
	user, _ = readUser(a.store.db, user.ID)
	if err := a.store.PurgeUser(user.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: user.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, resumeFilePath(task.ID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("purge leaked part", err)
	}
}
