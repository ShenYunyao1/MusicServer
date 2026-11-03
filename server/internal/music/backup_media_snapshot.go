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
func (s *Store) freezeBackupMedia(ctx context.Context, root *os.Root, input backupInput, target string, progress ...func(int64)) (backupInput, bool, error) {
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
	if input.SHA256 == "" {
		input.Size, input.SHA256, err = backupFileInfo(ctx, source)
		if err != nil {
			return input, false, err
		}
		if _, err = source.Seek(0, io.SeekStart); err != nil {
			return input, false, err
		}
	}
	if err := s.checkSpace(s.backupDirectory(), info.Size()); err != nil {
		return input, false, err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return input, false, err
	}
	hash := sha256.New()
	var report func(int64)
	if len(progress) > 0 {
		report = progress[0]
	}
	n, copyErr := backupCopy(ctx, io.MultiWriter(&backupSpaceWriter{store: s, path: s.backupDirectory(), writer: output}, hash), io.LimitReader(source, info.Size()+1), report)
	err = errors.Join(copyErr, output.Close())
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
	if n != info.Size() || after.Size() != info.Size() || digest != input.SHA256 {
		os.Remove(target)
		return input, true, nil
	}
	input.Local = target
	input.Frozen = true
	input.Size = n
	input.SHA256 = digest
	return input, false, nil
}
