package music

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"musicserver/internal/diskspace"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditDuplicateMissingFileNotDiscarded(t *testing.T) {
	a, _ := testAPI(t)
	data := validTestAudio(t)
	tr, err := a.store.ImportAudio("initial.wav", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := a.store.MediaPath(tr)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	next, err := a.store.ImportAudioWithMetadata("replacement.wav", bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := a.store.MediaPath(next)
	if _, err = os.Stat(p); err != nil {
		t.Fatal("fresh upload discarded", err)
	}
}

func TestAuditCoverReserveAndOrphanCollection(t *testing.T) {
	a, _ := testAPI(t)
	tr := a.store.Tracks()[0]
	a.store.spaceProbe = func(string) (diskspace.Space, error) { return diskspace.Space{Total: 1 << 30, Available: 1}, nil }
	if err := a.store.SetTrackCover(tr.ID, tinyPNG()); err == nil {
		t.Fatal("cover bypassed disk reserve")
	}
	a.store.spaceProbe = nil
	if err := a.store.transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO cover_assets VALUES('orphan','image/png',?)", []byte("old"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetTrackCover(tr.ID, tinyPNG()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := a.store.db.QueryRow("SELECT count(*) FROM cover_assets WHERE id='orphan'").Scan(&n); err != nil || n != 0 {
		t.Fatal("orphan cover retained", n, err)
	}
}

func TestAuditAssetGCRespectsSharedAndStagedReferences(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	tr := s.Tracks()[0]
	if err := s.SetTrackCover(tr.ID, tinyPNG()); err != nil {
		t.Fatal(err)
	}
	old := hashBytes(tinyPNG())
	p, err := s.StageAudio("reference.wav", bytes.NewReader(validTestAudio(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	s.db.QueryRow("SELECT track_json FROM upload_staging WHERE id=?", p.ID).Scan(&raw)
	var record stagedRecord
	json.Unmarshal([]byte(raw), &record)
	record.Track.CoverID = "staged-only"
	encoded, _ := json.Marshal(record)
	if err := s.transaction(func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO cover_assets VALUES('staged-only','image/png',?)", []byte("held")); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE upload_staging SET track_json=? WHERE id=?", string(encoded), p.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.transaction(cleanupDetachedAssets); err != nil {
		t.Fatal(err)
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM cover_assets WHERE id IN (?, 'staged-only')", old).Scan(&count)
	if count != 2 {
		t.Fatal("GC deleted referenced cover", count)
	}
	if err := s.DeleteUploadStage(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupUploadStages(); err != nil {
		t.Fatal(err)
	}
	s.db.QueryRow("SELECT count(*) FROM cover_assets WHERE id='staged-only'").Scan(&count)
	if count != 0 {
		t.Fatal("detached stage asset retained")
	}
}

func TestAuditPublicationOrphanNotScanned(t *testing.T) {
	a, _ := testAPI(t)
	root := a.store.mediaRoot
	os.MkdirAll(filepath.Join(root, "uploads"), 0755)
	orphan := filepath.Join(root, "uploads", "0123456789abcdef0123456789abcdef.wav")
	if err := os.WriteFile(orphan, validTestAudio(t), 0600); err != nil {
		t.Fatal(err)
	}
	before := len(a.store.Tracks())
	if _, err := a.store.runScan(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if len(a.store.Tracks()) != before {
		t.Fatal("uncommitted upload adopted by scanner")
	}
}

func TestAuditCleanupFailureDoesNotBlockUploadsOrReceiptGC(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	p, err := s.StageAudio("expired.wav", bytes.NewReader(validTestAudio(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	if err := s.db.QueryRow("SELECT temp_path FROM upload_staging WHERE id=?", p.ID).Scan(&path); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(s.mediaRoot, filepath.FromSlash(path)))
	os.Mkdir(filepath.Join(s.mediaRoot, filepath.FromSlash(path)), 0700)
	os.WriteFile(filepath.Join(s.mediaRoot, filepath.FromSlash(path), "occupied"), []byte("keep"), 0600)
	if _, err := s.db.Exec("UPDATE upload_staging SET expires_at=? WHERE id=?", persistedTime(time.Now().Add(-time.Hour)), p.ID); err != nil {
		t.Fatal(err)
	}
	receipt, _ := json.Marshal(uploadCommitReceipt{Owner: "test", CreatedAt: persistedTime(time.Now().Add(-48 * time.Hour))})
	s.db.Exec("INSERT INTO meta VALUES('upload_receipt/old',?)", string(receipt))
	if _, err := s.StageAudio("new.wav", bytes.NewReader(validTestAudio(t)), nil); err != nil {
		t.Fatal("unrelated upload blocked", err)
	}
	if err := s.CleanupUploadStages(); err == nil {
		t.Fatal("maintenance cleanup error not observable")
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM meta WHERE key='upload_receipt/old'").Scan(&count)
	if count != 0 {
		t.Fatal("receipt GC skipped on stage failure")
	}
}

func TestAuditPublicationRecoveryRetainsOwnerAndDraft(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	member, _ := readyMember(t, a, "publication-owner")
	p, err := s.StageAudio("owned.wav", bytes.NewReader(validTestAudio(t)), map[string]string{"title": "User draft title"}, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	var stage string
	s.db.QueryRow("SELECT temp_path FROM upload_staging WHERE id=?", p.ID).Scan(&stage)
	final := "uploads/" + p.ID + ".wav"
	os.MkdirAll(filepath.Join(s.mediaRoot, "uploads"), 0755)
	if err := os.Link(filepath.Join(s.mediaRoot, filepath.FromSlash(stage)), filepath.Join(s.mediaRoot, filepath.FromSlash(final))); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.mediaRoot, "uploads", p.ID+".lrc"), []byte("pending"), 0600)
	if _, err := s.db.Exec("INSERT INTO meta VALUES(?,?)", "upload_publish/"+p.ID, final); err != nil {
		t.Fatal(err)
	}
	if err := s.recoverUploadPublications(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.mediaRoot, filepath.FromSlash(stage))); err != nil {
		t.Fatal("recovery lost draft audio", err)
	}
	tr, _, err := s.CommitUploadStage(p.ID, MutationOptions{UserID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	if tr.OwnerID != member.ID || tr.UploadedBy != member.ID || tr.Title != "User draft title" {
		t.Fatal("recovered upload lost identity or draft", tr)
	}
}

func TestAuditPurgeOwnedSidecarsAndDetachedValidation(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	p, err := s.stageAudioWithAssets("sides.wav", bytes.NewReader(validTestAudio(t)), nil, uploadAssets{Lyrics: []byte("[00:01]Side lyric"), Cover: tinyPNG(), CoverSource: "external"})
	if err != nil {
		t.Fatal(err)
	}
	tr, _, err := s.CommitUploadStage(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	stem := strings.TrimSuffix(tr.Path, filepath.Ext(tr.Path))
	for _, extension := range []string{".lrc", ".png"} {
		if _, err := os.Stat(filepath.Join(s.mediaRoot, filepath.FromSlash(stem+extension))); err != nil {
			t.Fatal("fixture missing sidecar", err)
		}
	}
	admin, _, _ := loginTestAccount(t, s, "admin")
	if err := s.DeleteTrack(tr.ID); err != nil {
		t.Fatal(err)
	}
	tr = s.RemovedTracks()[0]
	if err := s.PurgeTrack(tr.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: tr.Version}); err != nil {
		t.Fatal(err)
	}
	for _, extension := range []string{".lrc", ".png"} {
		if _, err := os.Stat(filepath.Join(s.mediaRoot, filepath.FromSlash(stem+extension))); !os.IsNotExist(err) {
			t.Fatal("owned sidecar retained", extension, err)
		}
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM meta WHERE key=?", "validated_audio/"+tr.ContentHash).Scan(&count)
	if count != 0 {
		t.Fatal("detached validation marker retained")
	}
}
