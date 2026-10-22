package main

import (
	"musicserver/internal/music"
	"path/filepath"
	"testing"
)

func TestAdminInitializeAndOfflineReset(t *testing.T) {
	original := readConsolePassword
	t.Cleanup(func() { readConsolePassword = original })
	readConsolePassword = func() (string, error) { return "first administrator password", nil }
	data := filepath.Join(t.TempDir(), "accounts.sqlite")
	if err := adminCommand([]string{"init", "--data", data, "--username", "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := adminCommand([]string{"init", "--data", data}); err == nil {
		t.Fatal("duplicate init accepted")
	}
	unlock, err := music.LockDatabaseProcess(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminCommand([]string{"reset-password", "--data", data}); err == nil {
		unlock()
		t.Fatal("reset while locked was allowed")
	}
	unlock()
	readConsolePassword = func() (string, error) { return "second administrator password", nil }
	if err := adminCommand([]string{"reset-password", "--data", data, "--username", "admin", "--revoke-keys"}); err != nil {
		t.Fatal(err)
	}
	store, err := music.NewStore(data, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, _, _, err := store.Login("admin", "first administrator password", "test"); err == nil {
		t.Fatal("old password still accepted")
	}
	if _, _, _, err := store.Login("admin", "second administrator password", "test"); err != nil {
		t.Fatal(err)
	}
}
