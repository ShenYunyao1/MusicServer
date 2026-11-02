package music

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"musicserver/internal/version"
)

func TestBackupTemporaryCleanupPreservesArchivesAndManualSnapshots(t *testing.T) {
	a, _ := testAPI(t)
	dir := a.store.backupDirectory()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	id := "b0e3e30d-00be-4fdd-bc75-398e3a21f321"
	remove := []string{".backup-snapshot-" + id + ".sqlite", ".backup-snapshot-" + id + ".sqlite-journal", ".backup-config-1234567890", "backup-" + id + ".tar.gz.partial"}
	keep := []string{"backup-" + id + ".tar.gz", "20261102T120000-" + id + ".sqlite", ".backup-config-user-copy", ".backup-snapshot-not-a-uuid.sqlite"}
	for _, name := range append(append([]string{}, remove...), keep...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.store.backupBundleMu.Lock()
	err := a.store.CleanupBackupTemporary()
	a.store.backupBundleMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range remove {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("active backup temporary removed", err)
		}
	}
	if err := a.store.CleanupBackupTemporary(); err != nil {
		t.Fatal(err)
	}
	for _, name := range remove {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("orphan retained", name, err)
		}
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("non-temporary backup removed", name, err)
		}
	}
}

func TestLegacySQLiteSnapshotCanBeInspectedDownloadedAndDeleted(t *testing.T) {
	a, _ := testAPI(t)
	snapshot, err := a.store.Backup()
	if err != nil {
		t.Fatal(err)
	}
	items, err := a.store.BackupArchives()
	if err != nil || len(items) != 1 || items[0].Kind != "snapshot" || items[0].Warning == "" {
		t.Fatal("legacy snapshot invisible", items, err)
	}
	item := items[0]
	response := callAPI(a, "GET", "/api/v2/admin/backups/"+item.ID+"/download", "")
	if response.Code != 200 || int64(response.Body.Len()) != item.Size || response.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatal("legacy snapshot download failed", response.Code, response.Header())
	}
	if response := callAPI(a, "DELETE", "/api/v2/admin/backups/"+item.ID, ""); response.Code != 204 {
		t.Fatal("legacy snapshot cannot be deleted", response.Code, response.Body)
	}
	if _, err := os.Stat(snapshot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("snapshot still exists", err)
	}
	for _, id := range []string{"snapshot-../../library", "snapshot-20269999T120000-b0e3e30d-00be-4fdd-bc75-398e3a21f321", "snapshot-20261102T120000-not-a-uuid"} {
		if _, err := backupManagedName(id); err == nil {
			t.Fatal("invalid snapshot path accepted", id)
		}
	}
}

func TestRestoreStagingCleanupIsDestinationScoped(t *testing.T) {
	dir := t.TempDir()
	prefix := ".musicserver-restore-11111111111111111111111111111111-"
	remove := prefix + "12345"
	keep := []string{".musicserver-restore-22222222222222222222222222222222-12345", prefix + "manual-copy", ".musicserver-restore-12345"}
	for _, name := range append([]string{remove}, keep...) {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "data"), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupRestoreTemporary(dir, prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, remove)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("abandoned staging remained", err)
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name, "data")); err != nil {
			t.Fatal("another destination affected", err)
		}
	}
}

func TestRestoreMountRootRejectedBeforeArchiveRead(t *testing.T) {
	target := t.TempDir()
	_, err := restoreBackupWithMountCheck(context.Background(), "archive-must-not-be-opened", target, version.Schema, func(string) (bool, error) { return true, nil })
	if err == nil || !strings.Contains(err.Error(), "挂载点") {
		t.Fatal("missing early mount guidance", err)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatal("mount directory changed", entries, err)
	}
	if mounted, err := restoreDestinationMount(target); err != nil || mounted {
		t.Fatal("ordinary directory identified as mount", mounted, err)
	}
}

func manifestArchive(t *testing.T, manifest BackupManifest) []byte {
	t.Helper()
	var data bytes.Buffer
	compressed := gzip.NewWriter(&data)
	archive := tar.NewWriter(compressed)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(encoded)), Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(encoded); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestUnsupportedBackupSchemaIsDistinctFromCorruptManifest(t *testing.T) {
	a, _ := testAPI(t)
	manifest := BackupManifest{Format: 1, ID: "b0e3e30d-00be-4fdd-bc75-398e3a21f321", CreatedAt: time.Now(), Schema: 7, Files: []BackupFile{{Path: "database.sqlite", Size: 1, SHA256: strings.Repeat("a", 64)}, {Path: "config.json", Size: 1, SHA256: strings.Repeat("b", 64)}}}
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			manifest.Files[0].Path = "../escape"
		}
		data := manifestArchive(t, manifest)
		compressed, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := readBackupManifestForTarget(tar.NewReader(compressed), 9)
		compressed.Close()
		var schemaErr *backupSchemaError
		if readErr == nil || errors.As(readErr, &schemaErr) == corrupt {
			t.Fatal("invalid schema/format classification", corrupt, readErr)
		}
		if !corrupt {
			if err := os.MkdirAll(a.store.backupDirectory(), 0700); err != nil {
				t.Fatal(err)
			}
			name, _ := backupArchiveName(manifest.ID)
			if err := os.WriteFile(filepath.Join(a.store.backupDirectory(), name), data, 0600); err != nil {
				t.Fatal(err)
			}
			items, err := a.store.BackupArchives()
			if err != nil || len(items) != 1 || !strings.Contains(items[0].Warning, "隔离") || items[0].Error != "" {
				t.Fatal("list mislabels historical schema", items, err)
			}
		}
	}
}
