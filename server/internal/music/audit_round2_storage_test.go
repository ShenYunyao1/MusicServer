package music

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRound2AttachmentQuotaDeduplicatesPerAccount(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	alice, _ := readyMember(t, a, "attachment-alice")
	bob, _ := readyMember(t, a, "attachment-bob")
	data := tinyPNG()
	id := hashBytes(data)
	tracks := s.Tracks()
	tr := tracks[0]
	tr.UploadedBy = alice.ID
	tr.OwnerID = alice.ID
	tr.CoverID = id
	tr.CoverSource = "manual"
	tr.Lyrics = "same lyrics"
	tr.LyricsSource = "manual"
	if err := s.transaction(func(tx *sql.Tx) error {
		if err := saveCover(tx, data, "image/png"); err != nil {
			return err
		}
		return writeTrack(tx, tr)
	}); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{alice.ID, bob.ID} {
		for _, suffix := range []string{"one", "two"} {
			p := Playlist{ID: owner + suffix, OwnerID: owner, Name: suffix, CoverID: id, Version: 1}
			if err := s.transaction(func(tx *sql.Tx) error { return writePlaylist(tx, p) }); err != nil {
				t.Fatal(err)
			}
		}
	}
	usage, err := s.accountUsageForIDs(s.db, []string{alice.ID, bob.ID})
	if err != nil {
		t.Fatal(err)
	}
	if usage[alice.ID] != tr.Size+int64(len(data)+len(tr.Lyrics)) || usage[bob.ID] != int64(len(data)) {
		t.Fatalf("attachment accounting: %v", usage)
	}
	// Removing one of two references must not release the shared attachment.
	if err := s.transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM playlists WHERE id=?", bob.ID+"one")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	usage, _ = s.accountUsageForIDs(s.db, []string{bob.ID})
	if usage[bob.ID] != int64(len(data)) {
		t.Fatal(usage)
	}
	if err := s.transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM playlists WHERE id=?", bob.ID+"two")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	usage, _ = s.accountUsageForIDs(s.db, []string{bob.ID})
	if usage[bob.ID] != 0 {
		t.Fatal("last reference retained", usage)
	}
}

func TestRound2PendingUsageIncludesAttachmentsAndLiveReservation(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	u, token := readyMember(t, a, "pending-attachments")
	audio := validTestAudio(t)
	words := "[00:01]pending words"
	cover := tinyPNG()
	p, err := s.stageAudioWithAssets("assets.wav", bytes.NewReader(audio), nil, uploadAssets{Lyrics: []byte(words), Cover: cover}, MutationOptions{UserID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.reserveTemporaryUpload(u.ID, "inflight", 90)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.WriteFile(filepath.Join(s.mediaRoot, ".staging", "inflight.part"), make([]byte, 40), 0600); err != nil {
		t.Fatal(err)
	}
	w := sessionRequest(a, token, "GET", "/api/v2/uploads/preflight", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var out struct {
		Used    int64 `json:"used_bytes"`
		Pending int64 `json:"pending_bytes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	expected := int64(len(audio)+len(words)+len(cover)) + 90
	if out.Used != 0 || out.Pending != expected {
		t.Fatal("preflight differs from admission", out, expected)
	}
	track, _, err := s.CommitUploadStage(p.ID, MutationOptions{UserID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := s.accountUsageForIDs(s.db, []string{u.ID})
	if err != nil || usage[u.ID] != int64(len(audio)+len(words)+len(cover)) {
		t.Fatal("commit usage mismatch", usage, track, err)
	}
}

func TestRound2HardLinkedStagingUsesPhysicalBytesOnce(t *testing.T) {
	dir := t.TempDir()
	part := filepath.Join(dir, "id.part")
	stage := filepath.Join(dir, "id.wav")
	if err := os.WriteFile(part, make([]byte, 100), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(part, stage); err != nil {
		t.Skip("hard links unavailable", err)
	}
	if total, err := stagingPhysicalBytes(dir); err != nil || total != 100 {
		t.Fatal("hard link charged twice", total, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copy.wav"), make([]byte, 100), 0600); err != nil {
		t.Fatal(err)
	}
	if total, err := stagingPhysicalBytes(dir); err != nil || total != 200 {
		t.Fatal("physical copy was undercharged", total, err)
	}
}

func TestRound2MetadataCountersTrackMutations(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	u, _, _ := loginTestAccount(t, s, "admin")
	task := resumeCreate(t, a, u, 100)
	check := func() {
		t.Helper()
		var actual, total int64
		if err := s.db.QueryRow("SELECT COALESCE(sum(length(CAST(value AS BLOB))),0) FROM meta WHERE key LIKE 'upload_resume/%'").Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("SELECT bytes FROM browse_temp_metadata WHERE owner='*'").Scan(&total); err != nil || total != actual {
			t.Fatal(actual, total, err)
		}
	}
	check()
	if err := s.cancelResume(tagTestAuth(t, s, "admin"), task); err != nil {
		t.Fatal(err)
	}
	check()
	if _, err := s.db.Exec("DELETE FROM meta WHERE key=?", resumableMetaPrefix+task.ID); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestRound2AttachmentQuotaRejectsGrowthAndExcludesEmbedded(t *testing.T) {
	a, _ := testAPI(t)
	s := a.store
	admin, _, _ := loginTestAccount(t, s, "admin")
	tr := s.Tracks()[0]
	if _, err := s.SetStorageQuota(admin.ID, tr.Size, 0, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTrackCover(tr.ID, tinyPNG()); !errors.Is(err, errStorageQuota) {
		t.Fatal("cover bypassed quota", err)
	}
	current, _ := s.Track(tr.ID)
	if current.CoverID != "" {
		t.Fatal("rejected cover persisted")
	}
	tr.CoverID = hashBytes(tinyPNG())
	tr.CoverSource = "embedded"
	tr.Lyrics = "embedded"
	tr.EmbeddedLyrics = true
	tr.LyricsSource = "ID3/USLT"
	if err := s.transaction(func(tx *sql.Tx) error {
		if err := saveCover(tx, tinyPNG(), "image/png"); err != nil {
			return err
		}
		return writeTrack(tx, tr)
	}); err != nil {
		t.Fatal("embedded metadata charged twice", err)
	}
	if err := s.UpdateTrack(tr.ID, map[string]string{"lyrics": "new standalone words"}); !errors.Is(err, errStorageQuota) {
		t.Fatal("lyrics bypassed quota", err)
	}
}
