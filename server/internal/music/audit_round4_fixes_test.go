package music

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// round4WAV builds a valid larger PCM wave so a scan sees a real size change.
func round4WAV(dataBytes int) []byte {
	data := make([]byte, 44+dataBytes)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 8000)
	binary.LittleEndian.PutUint32(data[28:32], 16000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(dataBytes))
	for index := 44; index < len(data); index++ {
		data[index] = byte(index)
	}
	return data
}

func TestRound4ScanReconcilesOverQuotaStorage(t *testing.T) {
	a, root := testAPI(t)
	s := a.store
	track := s.Tracks()[0]
	before, err := accountStorageBreakdown(s.db, track.UploadedBy)
	if err != nil {
		t.Fatal(err)
	}
	limit := before.total() + 1024
	raw, _ := json.Marshal(StorageQuota{LimitBytes: limit, Version: 1})
	if err := s.transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "storage_quota/"+track.UploadedBy, string(raw))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	payload := round4WAV(800 + 4096)
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(track.Path)), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	revision := s.Revision()
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan must reconcile over-quota storage instead of failing: %v", err)
	}
	if s.Revision() == revision {
		t.Fatal("scan did not commit the size correction")
	}
	after, err := accountStorageBreakdown(s.db, track.UploadedBy)
	if err != nil {
		t.Fatal(err)
	}
	if after.AudioBytes != int64(len(payload)) {
		t.Fatalf("projection not corrected: %d want %d", after.AudioBytes, len(payload))
	}
	revision = s.Revision()
	if _, err := s.Scan(); err != nil || s.Revision() != revision {
		t.Fatalf("settled scan should be a no-op: revision changed, err=%v", err)
	}
}

// round4StageRow inserts a staged upload without running the ffprobe-dependent
// upload pipeline, so the cleanup/delete paths stay testable offline.
func round4StageRow(t *testing.T, s *Store, root, owner, name string) (string, string) {
	t.Helper()
	id := uuid.NewString()
	payload := validTestAudio(t)
	relative := ".staging/" + id + ".wav"
	if err := os.MkdirAll(filepath.Join(root, ".staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := s.db.Exec("INSERT INTO upload_staging(id,original_name,size,content_hash,temp_path,track_json,created_at,expires_at,user_id) VALUES(?,?,?,?,?,?,?,?,?)",
		id, name, int64(len(payload)), hashBytes(payload), relative, "{}", persistedTime(now), persistedTime(now.Add(time.Hour)), owner); err != nil {
		t.Fatal(err)
	}
	return id, relative
}

func TestRound4ResumeCleanupOnlyDiscardsConfirmedMissingCheckpoints(t *testing.T) {
	live := resumableUpload{ExpiresAt: time.Now().Add(time.Hour)}
	transient := errors.New("database is locked")
	if expired, err := resumeCleanupExpired(live, transient, User{}, nil); expired || !errors.Is(err, transient) {
		t.Fatalf("transient read error treated as expiry: expired=%v err=%v", expired, err)
	}
	if expired, err := resumeCleanupExpired(resumableUpload{}, errResumeCorrupt, User{}, nil); !expired || err != nil {
		t.Fatalf("corrupt checkpoint must be discarded: expired=%v err=%v", expired, err)
	}
	if expired, err := resumeCleanupExpired(resumableUpload{}, errResourceHidden, User{}, nil); !expired || err != nil {
		t.Fatalf("missing checkpoint must be discarded: expired=%v err=%v", expired, err)
	}
	if expired, err := resumeCleanupExpired(resumableUpload{ExpiresAt: time.Now().Add(-time.Minute)}, nil, User{Enabled: true}, nil); !expired || err != nil {
		t.Fatalf("expired checkpoint not discarded: expired=%v err=%v", expired, err)
	}
	if expired, err := resumeCleanupExpired(live, nil, User{Enabled: true}, nil); expired || err != nil {
		t.Fatalf("live checkpoint discarded: expired=%v err=%v", expired, err)
	}
	if expired, err := resumeCleanupExpired(live, nil, User{Enabled: false}, nil); !expired || err != nil {
		t.Fatalf("disabled owner checkpoint kept: expired=%v err=%v", expired, err)
	}
	if expired, err := resumeCleanupExpired(live, nil, User{Enabled: true}, sql.ErrNoRows); !expired || err != nil {
		t.Fatalf("removed owner checkpoint kept: expired=%v err=%v", expired, err)
	}
	if expired, err := resumeCleanupExpired(live, nil, User{Enabled: true}, transient); expired || !errors.Is(err, transient) {
		t.Fatalf("transient owner read error treated as expiry: expired=%v err=%v", expired, err)
	}
}

func TestRound4StageDeleteWhileActiveIsRetryable(t *testing.T) {
	a, root := testAPI(t)
	s := a.store
	u, token := readyMember(t, a, "stage-delete-busy")
	id, path := round4StageRow(t, s, root, u.ID, "busy.wav")
	s.stageMu.Lock()
	s.activeStages[path] = true
	s.stageMu.Unlock()
	w := sessionRequest(a, token, "DELETE", "/api/v2/uploads/stage/"+id, "")
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" || !strings.Contains(w.Body.String(), "UPLOAD_BUSY") {
		t.Fatalf("busy stage delete should be retryable: %d %s", w.Code, w.Body)
	}
	s.stageMu.Lock()
	delete(s.activeStages, path)
	s.stageMu.Unlock()
	w = sessionRequest(a, token, "DELETE", "/api/v2/uploads/stage/"+id, "")
	if w.Code != 204 {
		t.Fatalf("idle stage delete failed: %d %s", w.Code, w.Body)
	}
}

func TestRound4StorageOverviewAggregatesAllAccounts(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	alice, _ := readyMember(t, a, "overview-alice")
	bob, _ := readyMember(t, a, "overview-bob")
	cover := tinyPNG()
	coverID := hashBytes(cover)
	for _, owner := range []User{alice, bob} {
		playlist := Playlist{ID: owner.ID + "-overview", OwnerID: owner.ID, Name: owner.Username, CoverID: coverID, Version: 1}
		if err := s.transaction(func(tx *sql.Tx) error {
			if err := saveCover(tx, cover, "image/png"); err != nil {
				return err
			}
			return writePlaylist(tx, playlist)
		}); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(StorageQuota{LimitBytes: 12345, Version: 3})
	if err := s.transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "storage_quota/"+alice.ID, string(raw))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := s.StorageOverview()
	if err != nil {
		t.Fatal(err)
	}
	accounts := map[string]AccountStorage{}
	for _, account := range summary.Accounts {
		accounts[account.ID] = account
	}
	var media int64
	for id, account := range accounts {
		want, err := accountStorageBreakdown(s.db, id)
		if err != nil {
			t.Fatal(err)
		}
		if account.storageBreakdown != want || account.UsedBytes != want.total() {
			t.Fatalf("account %s breakdown mismatch: got %+v used %d want %+v", id, account.storageBreakdown, account.UsedBytes, want)
		}
		media += account.AudioBytes
	}
	if summary.MediaBytes != media {
		t.Fatalf("media total mismatch: %d want %d", summary.MediaBytes, media)
	}
	if accounts[alice.ID].StorageQuota.LimitBytes != 12345 {
		t.Fatalf("quota not batched: %+v", accounts[alice.ID].StorageQuota)
	}
	if accounts[bob.ID].CoverBytes == 0 || accounts[alice.ID].CoverBytes == 0 {
		t.Fatalf("expected attachment refs for both accounts: %+v", accounts)
	}
}

func TestRound4DiscardAuditMarksPendingFilesystemCleanup(t *testing.T) {
	a, root := testAPI(t)
	s := a.store
	u, _ := readyMember(t, a, "discard-incomplete")
	admin, _, _ := loginTestAccount(t, s, "admin")
	_, path := round4StageRow(t, s, root, u.ID, "incomplete.wav")
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(full, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "keep"), []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.SetUserEnabled(u.ID, false, u.Version)
	u, _ = readUser(s.db, u.ID)
	if err := s.PurgeUser(u.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: u.Version}); err == nil {
		t.Fatal("purge ignored failed preview cleanup")
	}
	var actions int
	if err := s.db.QueryRow("SELECT count(*) FROM audit WHERE action='account.temporary_discard_incomplete' AND entity_id=?", u.ID).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if actions != 1 {
		t.Fatalf("pending-cleanup audit missing: %d", actions)
	}
}
