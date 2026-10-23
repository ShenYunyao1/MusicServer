package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"sort"
	"strings"
	"time"
)

func archive(path, platform string, entries map[string][]byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	if platform == "windows" {
		z := zip.NewWriter(f)
		for _, name := range names {
			w, e := z.Create(name)
			if e != nil {
				return e
			}
			if _, e = w.Write(entries[name]); e != nil {
				return e
			}
		}
		if err = z.Close(); err != nil {
			return err
		}
	} else {
		gz := gzip.NewWriter(f)
		t := tar.NewWriter(gz)
		for _, name := range names {
			mode := int64(0644)
			if name == "musicserver" || name == "musicserver-updater" || strings.HasSuffix(name, ".sh") {
				mode = 0755
			}
			h := &tar.Header{Name: name, Size: int64(len(entries[name])), Mode: mode, ModTime: time.Unix(0, 0)}
			if err = t.WriteHeader(h); err != nil {
				return err
			}
			if _, err = t.Write(entries[name]); err != nil {
				return err
			}
		}
		if err = t.Close(); err != nil {
			return err
		}
		if err = gz.Close(); err != nil {
			return err
		}
	}
	return f.Sync()
}
