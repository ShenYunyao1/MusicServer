package music

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAssetDirectoryIndexSharedAndScopedToScan(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cover.PNG"), tinyPNG(), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	reads := 0
	index := &assetDirectoryIndex{read: func(root *os.Root, dir string) ([]os.DirEntry, error) {
		reads++
		return readAssetDirectory(root, dir)
	}}
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("song%d.mp3", i)
		if data, _, _ := readExternalCoverForMetadata(root, name, audioMetadata{}, index); len(data) == 0 {
			t.Fatal("case-insensitive artwork lost")
		}
		if _, source := readExternalLyrics(root, name, index); source != "" {
			t.Fatal("unexpected lyrics")
		}
	}
	if reads != 1 {
		t.Fatalf("enumerated directory %d times for 100 songs", reads)
	}
	if err := os.WriteFile(filepath.Join(dir, "SONG0.LRC"), []byte("[00:01]new lyrics"), 0600); err != nil {
		t.Fatal(err)
	}
	if text, _ := readExternalLyrics(root, "song0.mp3", &assetDirectoryIndex{}); text != "[00:01]new lyrics" {
		t.Fatal("new scan reused stale negative lookup")
	}
}

func TestExactArtworkAvoidsDirectoryEnumeration(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "song.jpg"), tinyPNG(), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	index := &assetDirectoryIndex{read: func(*os.Root, string) ([]os.DirEntry, error) {
		t.Fatal("exact cover unnecessarily enumerated directory")
		return nil, nil
	}}
	if data, _, _ := readExternalCoverForMetadata(root, "song.mp3", audioMetadata{}, index); len(data) == 0 {
		t.Fatal("exact artwork lost")
	}
}
