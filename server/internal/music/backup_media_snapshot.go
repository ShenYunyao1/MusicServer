package music

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// Return changed=true for source disappearance or size/hash changes. Storage,
// permission and cancellation failures remain fatal and are never hidden.
func (s *Store) freezeBackupMedia(ctx context.Context, root *os.Root, input backupInput, target string) (backupInput, bool, error) {
	source, err := root.Open(input.Local)
	if errors.Is(err, os.ErrNotExist) {
		return input, true, nil
	}
	if err != nil {
		return input, false, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return input, false, err
	}
	if !info.Mode().IsRegular() || input.SHA256 != "" && info.Size() != input.Size {
		return input, true, nil
	}
	if info.Size() > maxBackupBytes {
		return input, false, errors.New("backup input exceeds format limit")
	}
	if err := s.checkSpace(s.backupDirectory(), info.Size()); err != nil {
		return input, false, err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return input, false, err
	}
	hash := sha256.New()
	n, copyErr := backupCopy(ctx, io.MultiWriter(storageWriter{s, s.backupDirectory(), output}, hash), io.LimitReader(source, info.Size()+1), nil)
	err = errors.Join(copyErr, output.Sync(), output.Close())
	if err != nil {
		os.Remove(target)
		return input, false, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	after, err := source.Stat()
	if err != nil {
		os.Remove(target)
		return input, false, err
	}
	if n != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || input.SHA256 != "" && digest != input.SHA256 {
		os.Remove(target)
		return input, true, nil
	}
	input.Local = target
	input.Frozen = true
	input.Size = n
	input.SHA256 = digest
	return input, false, nil
}
