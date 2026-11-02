package music

import (
	"context"
	"os"
	"path/filepath"
	"sync"
)

// A scan owns its index. Negative lookups are never reused by a later scan.
// ReadDir reads names and types without statting every audio file in the folder.
type assetDirectoryIndex struct {
	mu          sync.Mutex
	directories map[string]map[string]string
	read        func(*os.Root, string) ([]os.DirEntry, error)
}
type assetDirectoryIndexKey struct{}

func readAssetDirectory(root *os.Root, dir string) ([]os.DirEntry, error) {
	f, err := root.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}
func (index *assetDirectoryIndex) find(root *os.Root, relative string) string {
	dir, name := filepath.ToSlash(filepath.Dir(relative)), filepath.Base(relative)
	index.mu.Lock()
	defer index.mu.Unlock()
	if index.directories == nil {
		index.directories = map[string]map[string]string{}
	}
	names, exists := index.directories[dir]
	if !exists {
		read := index.read
		if read == nil {
			read = readAssetDirectory
		}
		entries, err := read(root, dir)
		if err != nil {
			return ""
		}
		names = map[string]string{}
		for _, entry := range entries {
			key := backupFoldName(entry.Name())
			if _, exists := names[key]; !exists {
				names[key] = entry.Name()
			}
		}
		index.directories[dir] = names
	}
	actual := names[backupFoldName(name)]
	if actual == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(dir, actual))
}
func assetIndex(ctx context.Context) *assetDirectoryIndex {
	if index, ok := ctx.Value(assetDirectoryIndexKey{}).(*assetDirectoryIndex); ok {
		return index
	}
	return &assetDirectoryIndex{}
}
func assetLookup(indices []*assetDirectoryIndex) *assetDirectoryIndex {
	if len(indices) > 0 && indices[0] != nil {
		return indices[0]
	}
	return &assetDirectoryIndex{}
}
