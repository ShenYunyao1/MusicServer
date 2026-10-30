package music

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
)

type uploadPhaseKey struct{}
type uploadSourceKey struct{}
type uploadPhaseIdentity struct {
	store *Store
	id    string
}

var uploadPhases sync.Map

func setUploadPhase(ctx context.Context, phase string) {
	if callback, ok := ctx.Value(uploadPhaseKey{}).(func(string)); ok {
		callback(phase)
	}
}

func (a *API) uploadPreflight(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v2/uploads/preflight" {
		return false
	}
	if !a.requireAccount(w, r) {
		return true
	}
	auth := authFromRequest(r)
	if err := resumeActor(a.store.db, auth); err != nil {
		writeResumeError(w, err)
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return true
	}
	used, err := a.store.accountUsageForIDs(a.store.db, []string{auth.userID})
	if err != nil {
		writeResumeError(w, err)
		return true
	}
	var quota StorageQuota
	if err := readStorageJSON(a.store.db, "storage_quota/"+auth.userID, &quota); err != nil {
		writeResumeError(w, err)
		return true
	}
	writeJSON(w, 200, map[string]any{"max_upload_bytes": a.store.Settings().MaxUploadBytes, "used_bytes": used[auth.userID], "limit_bytes": quota.LimitBytes, "upload_allowed": checkUploadEmailPolicy(a.store.db, auth.userID) == nil})
	return true
}

// linkUploadFile publishes a second immutable name on the same volume. If the
// volume lacks hard links, fall back to the bounded existing copy path.
func (s *Store) materializeUpload(ctx context.Context, root *os.Root, target string, reader io.Reader, max int64) (int64, error) {
	if source, ok := ctx.Value(uploadSourceKey{}).(string); ok {
		if info, e := root.Lstat(source); e == nil && !info.Mode().IsRegular() {
			return 0, errors.New("upload source is not a regular file")
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return 0, e
		}
		// A crash can leave a completed link before the checkpoint commits.
		// Reuse only the same regular inode; never overwrite an unrelated file.
		if from, e := root.Lstat(source); e == nil && from.Mode().IsRegular() {
			if to, e := root.Lstat(target); e == nil && to.Mode().IsRegular() && os.SameFile(from, to) {
				if to.Size() > max {
					return 0, errors.New("file exceeds configured upload limit")
				}
				return to.Size(), nil
			}
		}
		if err := root.Link(source, target); err == nil {
			if info, e := root.Lstat(target); e != nil || !info.Mode().IsRegular() {
				_ = root.Remove(target)
				if e != nil {
					return 0, e
				}
				return 0, errors.New("linked upload is not a regular file")
			}
			file, err := root.Open(target)
			if err != nil {
				_ = root.Remove(target)
				return 0, err
			}
			info, err := file.Stat()
			_ = file.Close()
			if err != nil {
				_ = root.Remove(target)
				return 0, err
			}
			if info.Size() > max {
				_ = root.Remove(target)
				return 0, errors.New("file exceeds configured upload limit")
			}
			return info.Size(), nil
		} else if _, e := root.Lstat(target); e == nil {
			return 0, err
		}
	}
	file, err := root.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(storageWriter{s, s.mediaRoot, file}, io.LimitReader(contextReader{ctx, reader}, max+1))
	err = errors.Join(err, file.Sync(), file.Close())
	if n > max {
		err = errors.Join(err, errors.New("file exceeds configured upload limit"))
	}
	if err != nil {
		_ = root.Remove(target)
	}
	return n, err
}

// Matching is based on bytes actually received and hashed by this server.
func (s *Store) existingUpload(ctx context.Context, root *os.Root, path string) (Track, bool, error) {
	f, err := root.Open(path)
	if err != nil {
		return Track{}, false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx, f}); err != nil {
		return Track{}, false, err
	}
	var id string
	err = s.db.QueryRow("SELECT id FROM tracks WHERE content_hash=? AND removed=0 AND missing=0 ORDER BY id LIMIT 1", hex.EncodeToString(h.Sum(nil))).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Track{}, false, nil
	}
	if err != nil {
		return Track{}, false, err
	}
	t, err := readTrackFrom(s.db, id, false)
	return t, err == nil, err
}

// Duplicate commits publish no audio/sidecars and never apply the caller's
// metadata to the shared source. Tags retain their normal version checks.
func (s *Store) commitExistingUpload(id, owner, hash string, edits *UploadTagEdits, options []MutationOptions) (Track, bool, error) {
	var track Track
	var candidate string
	if err := s.db.QueryRow("SELECT id FROM tracks WHERE content_hash=? AND removed=0 AND missing=0 ORDER BY id LIMIT 1", hash).Scan(&candidate); errors.Is(err, sql.ErrNoRows) {
		return track, false, nil
	} else if err != nil {
		return track, false, err
	}
	found, changed := false, false
	transact := s.transaction
	if edits != nil {
		transact = func(run func(*sql.Tx) error) error { return s.tagTransaction(false, run) }
	}
	err := transact(func(tx *sql.Tx) error {
		if err := checkStageUploadActor(tx, owner, options); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRow("SELECT id FROM tracks WHERE content_hash=? AND removed=0 AND missing=0 ORDER BY id LIMIT 1", hash).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		changed, err = applyUploadTagsTx(tx, existing, edits, options)
		if err != nil {
			return err
		}
		track, err = readTrackFrom(tx, existing, false)
		if err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM upload_staging WHERE id=?", id)
		return err
	})
	if err == nil && changed {
		s.notifyCommunityCommit()
	}
	return track, found, err
}
