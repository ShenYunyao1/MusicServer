package main

import (
	"fmt"
	"os"
	"path/filepath"

	"musicserver/internal/music"
)

type runtimePaths struct{ MediaRoot, Data string }

func runtimeDefaults(executable string) runtimePaths {
	root := filepath.Dir(executable)
	return runtimePaths{MediaRoot: root, Data: filepath.Join(root, "data", "library.sqlite")}
}

func executableDefaults() (runtimePaths, error) {
	executable, err := os.Executable()
	if err != nil {
		return runtimePaths{}, err
	}
	return runtimeDefaults(executable), nil
}

func resolveRuntimePath(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func ensureAdministrator(store *music.Store) error {
	if err := store.RequireAdministrator(); err == nil {
		return nil
	}
	initialized, err := store.HasAdministrator()
	if err != nil {
		return err
	}
	if initialized {
		return store.RequireAdministrator()
	}
	fmt.Fprintln(os.Stderr, "First startup: create administrator 'admin'. Your password will be hidden.")
	password, err := readConsolePassword()
	if err != nil {
		return fmt.Errorf("initialize administrator in a terminal with start.bat or musicserver admin init: %w", err)
	}
	if _, err := store.InitializeAdmin("admin", password); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Administrator created. Starting server...")
	return nil
}
