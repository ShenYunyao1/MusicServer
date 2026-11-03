package music

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const restoreMarkerName = ".musicserver-restore-owner.json"

type restoreMarker struct {
	Format  int       `json:"format"`
	Target  string    `json:"target"`
	Stage   string    `json:"stage"`
	Created time.Time `json:"created"`
}

func restoreTargetKey(target string) string {
	if runtime.GOOS == "windows" {
		target = strings.ToLower(target)
	}
	sum := sha256.Sum256([]byte(target))
	return hex.EncodeToString(sum[:16])
}

func writeRestoreMarker(stage, target string) error {
	raw, err := json.Marshal(restoreMarker{1, target, filepath.Base(stage), time.Now().UTC()})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(stage, restoreMarkerName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	return errors.Join(err, f.Sync(), f.Close())
}

func managedRestoreStage(parent, name string) (bool, error) {
	root, err := os.OpenRoot(parent)
	if err != nil {
		return false, err
	}
	defer root.Close()
	relative := filepath.Join(name, restoreMarkerName)
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return false, nil
	}
	f, err := root.Open(relative)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var marker restoreMarker
	decoder := json.NewDecoder(io.LimitReader(f, 4097))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&marker) != nil || decoder.Decode(new(any)) != io.EOF || marker.Format != 1 || marker.Stage != name || marker.Created.IsZero() {
		return false, nil
	}
	if !filepath.IsAbs(marker.Target) || filepath.Clean(marker.Target) != marker.Target {
		return false, nil
	}
	expectedParent := filepath.Dir(marker.Target)
	if runtime.GOOS == "windows" {
		if !strings.EqualFold(expectedParent, parent) {
			return false, nil
		}
	} else if expectedParent != parent {
		return false, nil
	}
	return strings.HasPrefix(name, ".musicserver-restore-"+restoreTargetKey(marker.Target)+"-"), nil
}
