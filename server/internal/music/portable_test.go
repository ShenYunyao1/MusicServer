package music

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootLibrarySkipsServerDataAndArchives(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{"album", "data/updates", ".server-archive/old"} {
		if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, folder, "Song.mp3"), []byte("test audio"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := NewStore(filepath.Join(root, "data", "library.sqlite"), root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	tracks := store.Tracks()
	if len(tracks) != 1 || tracks[0].Path != "album/Song.mp3" {
		t.Fatalf("scanned internal server files: %+v", tracks)
	}
}
