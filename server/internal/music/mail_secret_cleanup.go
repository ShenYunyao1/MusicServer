package music

import (
	"errors"
	"io"
	"log"
	"os"
	"strings"

	"pikmelo-server/internal/activity"
)

func (s *Store) mailCleanupWarning(message string) {
	s.mail.warningMu.Lock()
	s.mail.cleanupWarning = message
	s.mail.warningMu.Unlock()
}
func (s *Store) CleanupMailSecrets() error {
	s.mail.mu.Lock()
	defer s.mail.mu.Unlock()
	return s.cleanupMailSecretsLocked()
}

// Caller holds mail.mu across both credential creation and settings commit.
// Master encryption material and unknown entries are never garbage collected.
func (s *Store) cleanupMailSecretsLocked() (failure error) {
	defer func() {
		if failure != nil {
			s.mailCleanupWarning("存在未能清理的旧邮件凭据，后台将重试；请检查秘密目录权限。")
			log.Printf("mail credential cleanup failed: %v", failure)
			_ = s.activity.Emit("mail.cleanup_failed", activity.Fields{Reason: "MAIL_UNAVAILABLE"})
		}
	}()
	record, err := readMailSettings(s.readDatabase())
	if err != nil {
		return err
	}
	if record.SecretID != "" && !validResumeID(record.SecretID) {
		return errMailUnavailable
	}
	directory := s.mailSecretsDirectory()
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		s.mailCleanupWarning("")
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errMailUnavailable
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	removed, scanned := 0, 0
	var failures []error
	for {
		entries, readErr := dir.ReadDir(64)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		for _, entry := range entries {
			scanned++
			name := entry.Name()
			id, ok := strings.CutSuffix(name, ".json")
			if !ok || !validResumeID(id) || id == record.SecretID || !entry.Type().IsRegular() {
				continue
			}
			if s.mail.secretRemoveHook != nil {
				err = s.mail.secretRemoveHook(name)
			} else {
				err = root.Remove(name)
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, err)
			} else {
				removed++
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if removed >= 32 || scanned >= 4096 {
			s.mailCleanupWarning("旧邮件凭据分批清理中，后台将继续处理。")
			return errors.Join(failures...)
		}
	}
	if len(failures) == 0 {
		s.mailCleanupWarning("")
	}
	return errors.Join(failures...)
}
