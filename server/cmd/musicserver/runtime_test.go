package main

import (
	"os"
	"path/filepath"
	"testing"

	"musicserver/internal/music"
)

func TestRuntimeDefaultsFollowExecutable(t *testing.T) {
	base := t.TempDir()
	executable := filepath.Join(base, "portable", "musicserver.exe")
	paths := runtimeDefaults(executable)
	if paths.MediaRoot != filepath.Dir(executable) || paths.Data != filepath.Join(filepath.Dir(executable), "data", "library.sqlite") {
		t.Fatalf("defaults must follow executable: %+v", paths)
	}
	if got := resolveRuntimePath(paths.MediaRoot, "music"); got != filepath.Join(paths.MediaRoot, "music") {
		t.Fatalf("relative override: %s", got)
	}
}

func TestFirstStartInitializesOnlyOnce(t *testing.T) {
	store, err := music.NewStore(filepath.Join(t.TempDir(), "library.sqlite"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	original := readConsolePassword
	t.Cleanup(func() { readConsolePassword = original })
	calls := 0
	readConsolePassword = func() (string, error) { calls++; return "first startup administrator password", nil }
	if err := ensureAdministrator(store); err != nil {
		t.Fatal(err)
	}
	if err := ensureAdministrator(store); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("password prompted %d times", calls)
	}
	if _, _, _, err := store.Login("admin", "first startup administrator password", "test"); err != nil {
		t.Fatal(err)
	}
}

func TestPortableLibrarySurvivesWholeDirectoryMove(t *testing.T) {
	base := t.TempDir()
	before := filepath.Join(base, "before")
	after := filepath.Join(base, "after")
	if err := os.MkdirAll(filepath.Join(before, "album"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(before, "album", "Song.mp3"), []byte("portable music fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := runtimeDefaults(filepath.Join(before, "musicserver.exe"))
	store, err := music.NewStore(paths.Data, paths.MediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InitializeAdmin("admin", "portable administrator password"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	tracks := store.Tracks()
	if len(tracks) != 1 || tracks[0].Path != "album/Song.mp3" {
		t.Fatalf("relative path: %+v", tracks)
	}
	originalID := tracks[0].ID
	if err := store.UpdateTrack(tracks[0].ID, map[string]string{"title": "Keep my title"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(before, after); err != nil {
		t.Fatal(err)
	}
	paths = runtimeDefaults(filepath.Join(after, "musicserver.exe"))
	store, err = music.NewStore(paths.Data, paths.MediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	tracks = store.Tracks()
	if len(tracks) != 1 || tracks[0].ID != originalID || tracks[0].Title != "Keep my title" || tracks[0].Missing {
		t.Fatalf("move lost track state: %+v", tracks)
	}
	if _, err := os.Stat(filepath.Join(paths.MediaRoot, filepath.FromSlash(tracks[0].Path))); err != nil {
		t.Fatal(err)
	}
}
