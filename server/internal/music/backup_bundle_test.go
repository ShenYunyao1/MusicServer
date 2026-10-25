package music

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupBundleReleasesMediaPublicationLockAfterSnapshot(t *testing.T) {
	api, _ := testAPI(t)
	checked := false
	_, err := api.store.CreateBackupBundle(context.Background(), true, func(phase string, _, _ int64) {
		if phase == "packing" && !checked {
			checked = true
			if !api.store.mediaMu.TryLock() {
				t.Error("archive compression blocks unrelated upload publication")
			} else {
				api.store.mediaMu.Unlock()
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("packing phase was not reached")
	}
}

func TestBackupBundlePreservesSupportedResourceNamesOnly(t *testing.T) {
	api, root := testAPI(t)
	track := api.store.Tracks()[0]
	stem := strings.TrimSuffix(track.Path, filepath.Ext(track.Path))
	for name, data := range map[string][]byte{stem + ".LRC": []byte("[00:01]Uppercase"), stem + ".gif": []byte("GIF89a"), "private-account-screenshot.jpg": []byte("not a music cover")} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive, err := api.store.CreateBackupBundle(context.Background(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := RestoreBackup(context.Background(), filepath.Join(api.store.backupDirectory(), archive.Name), filepath.Join(t.TempDir(), "restore"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, file := range manifest.Files {
		names[file.Path] = true
	}
	if !names["media/"+filepath.ToSlash(stem)+".LRC"] || !names["media/"+filepath.ToSlash(stem)+".gif"] {
		t.Error("supported mixed-case lyrics or GIF artwork were omitted")
	}
	if names["media/private-account-screenshot.jpg"] {
		t.Error("unrelated image was included")
	}
}

func TestBackupBundleAlreadyCancelledDoesNotWaitForDatabaseWriter(t *testing.T) {
	api, _ := testAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api.store.writeMu.Lock()
	done := make(chan error, 1)
	go func() { _, err := api.store.CreateBackupBundle(ctx, true, nil); done <- err }()
	select {
	case err := <-done:
		api.store.writeMu.Unlock()
		if err == nil {
			t.Fatal("cancelled backup succeeded")
		}
	case <-time.After(300 * time.Millisecond):
		api.store.writeMu.Unlock()
		<-done
		t.Fatal("cancelled backup waited for a database writer")
	}
}

func TestBackupRestoreRejectsSchemaMetadataMismatch(t *testing.T) {
	api, _ := testAPI(t)
	snapshot, err := api.store.Backup()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(snapshot)
	db, err := openSQLite(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("UPDATE meta SET value='different' WHERE key='schema_version'")
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(api.store.Settings())
	manifest := BackupManifest{Format: 1, ID: "b0e3e30d-00be-4fdd-bc75-398e3a21f321", CreatedAt: time.Now(), Version: "test", Schema: 7}
	contents := map[string][]byte{"database.sqlite": database, "config.json": config}
	for name, data := range contents {
		hash := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, BackupFile{Path: name, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])})
	}
	filePath := filepath.Join(t.TempDir(), "metadata-mismatch.tar.gz")
	file, _ := os.Create(filePath)
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	encoded, _ := json.Marshal(manifest)
	archive.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(encoded))})
	archive.Write(encoded)
	for _, input := range manifest.Files {
		archive.WriteHeader(&tar.Header{Name: input.Path, Mode: 0600, Size: input.Size})
		archive.Write(contents[input.Path])
	}
	archive.Close()
	compressed.Close()
	file.Close()
	destination := filepath.Join(t.TempDir(), "restore")
	if _, err := RestoreBackup(context.Background(), filePath, destination); err == nil {
		t.Fatal("restore accepted a database which NewStore would reject")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("invalid restored database was published")
	}
}

func TestBackupBundleRestoresIdentityMediaAndConfiguration(t *testing.T) {
	api, root := testAPI(t)
	admin, _, token := loginTestAccount(t, api.store, "admin")
	track := api.store.Tracks()[0]
	if err := api.store.PutPlaylist(Playlist{ID: "backup-playlist", Name: "Keep notes", TrackIDs: []string{track.ID}, TrackNotes: map[string]string{track.ID: "Private note"}}); err != nil {
		t.Fatal(err)
	}
	key, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "Keep Key", AllowedPlaylistIDs: []string{"backup-playlist"}})
	lyrics := strings.TrimSuffix(track.Path, filepath.Ext(track.Path)) + ".lrc"
	if err := os.WriteFile(filepath.Join(root, lyrics), []byte("[00:01]Backed up lyric"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "never-back-up-secret.env"), []byte("private system setting"), 0600); err != nil {
		t.Fatal(err)
	}
	archive, err := api.store.CreateBackupBundle(context.Background(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored")
	manifest, err := RestoreBackup(context.Background(), filepath.Join(api.store.backupDirectory(), archive.Name), destination)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.IncludeMedia || len(manifest.MissingMedia) != 0 || manifest.ID != archive.ID {
		t.Fatal("incorrect restored manifest")
	}
	if _, err := os.Stat(filepath.Join(destination, "never-back-up-secret.env")); !os.IsNotExist(err) {
		t.Fatal("backup included a non-media system file")
	}
	if data, err := os.ReadFile(filepath.Join(destination, lyrics)); err != nil || string(data) != "[00:01]Backed up lyric" {
		t.Fatal("sidecar was not restored")
	}
	restored, err := NewStore(filepath.Join(destination, "data/library.sqlite"), destination)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, ok := restored.Track(track.ID)
	playlist, exists := restored.Playlist("backup-playlist")
	if !ok || !exists || got.Path != track.Path || got.ContentHash != track.ContentHash || playlist.TrackNotes[track.ID] != "Private note" || restored.Settings().MediaRoot != destination {
		t.Fatal("restored identities, notes or application settings changed")
	}
	if user, _, _, err := restored.Login("admin", "test administrator password", "restored"); err != nil || user.ID != admin.ID {
		t.Fatal("account or password was not restored")
	}
	if _, _, ok := restored.sessionIdentity(token); ok {
		t.Fatal("restore retained an old browser session")
	}
	if got, ok := restored.AuthenticateSecret(secret); !ok || got.ID != key.ID {
		t.Fatal("restore lost the existing player Key")
	}
}

func TestBackupBundleRejectsChangedMediaAndOccupiedRestoreTarget(t *testing.T) {
	api, root := testAPI(t)
	track := api.store.Tracks()[0]
	archive, err := api.store.CreateBackupBundle(context.Background(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	occupied := t.TempDir()
	marker := filepath.Join(occupied, "keep.txt")
	os.WriteFile(marker, []byte("keep"), 0600)
	if _, err := RestoreBackup(context.Background(), filepath.Join(api.store.backupDirectory(), archive.Name), occupied); err == nil {
		t.Fatal("restore overwrote an occupied directory")
	}
	if data, _ := os.ReadFile(marker); string(data) != "keep" {
		t.Fatal("failed restore changed existing content")
	}
	data, err := os.ReadFile(filepath.Join(root, track.Path))
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	os.WriteFile(filepath.Join(root, track.Path), data, 0600)
	if _, err := api.store.CreateBackupBundle(context.Background(), true, nil); err == nil {
		t.Fatal("backup accepted bytes inconsistent with the database hash")
	}
	items, err := api.store.BackupArchives()
	if err != nil || len(items) != 1 || items[0].ID != archive.ID {
		t.Fatal("failed backup damaged or added a completed archive")
	}
}

func TestBackupRestoreRejectsUnsafePathsLinksAndChecksumMismatch(t *testing.T) {
	for _, kind := range []string{"path", "link", "checksum"} {
		t.Run(kind, func(t *testing.T) {
			manifest := BackupManifest{Format: 1, ID: "b0e3e30d-00be-4fdd-bc75-398e3a21f321", CreatedAt: time.Now(), Version: "test", Schema: 7, Files: []BackupFile{{Path: "database.sqlite", Size: 1, SHA256: strings.Repeat("0", 64)}, {Path: "config.json", Size: 1, SHA256: strings.Repeat("0", 64)}}}
			if kind == "path" {
				manifest.Files[0].Path = "media/../../escape.mp3"
			}
			filePath := filepath.Join(t.TempDir(), "bad.tar.gz")
			file, _ := os.Create(filePath)
			compressed := gzip.NewWriter(file)
			archive := tar.NewWriter(compressed)
			encoded, _ := json.Marshal(manifest)
			archive.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(encoded))})
			archive.Write(encoded)
			if kind == "link" {
				archive.WriteHeader(&tar.Header{Name: "database.sqlite", Typeflag: tar.TypeSymlink, Linkname: "../../other"})
			} else {
				archive.WriteHeader(&tar.Header{Name: "database.sqlite", Mode: 0600, Size: 1})
				archive.Write([]byte("x"))
			}
			archive.Close()
			compressed.Close()
			file.Close()
			destination := filepath.Join(t.TempDir(), "destination")
			if _, err := RestoreBackup(context.Background(), filePath, destination); err == nil {
				t.Fatal("unsafe or corrupt backup was accepted")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("failed restore published a destination")
			}
		})
	}
}

func TestManagedBackupEndpointsRequireAdministrator(t *testing.T) {
	api, _ := testAPI(t)
	if response := callAPI(api, "GET", "/api/v2/admin/backups", ""); response.Code != 200 {
		t.Fatalf("backup list missing: %d", response.Code)
	}
	_, token := readyMember(t, api, "backup-member")
	if response := sessionRequest(api, token, "GET", "/api/v2/admin/backups", ""); response.Code != 403 {
		t.Fatalf("member read backups: %d", response.Code)
	}
	_, secret := createPersistentKey(t, api, APIKeyCreateRequest{Name: "player"})
	if response := callAPIWithKey(api, "GET", "/api/v2/admin/backups", "", secret); response.Code != 403 {
		t.Fatalf("key read backups: %d", response.Code)
	}
}
