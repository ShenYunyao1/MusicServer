package main

import (
	"os"
	"path/filepath"
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
