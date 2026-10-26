package music

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func writeStorageError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, errEmailVerificationRequired) {
		writeError(w, 403, "EMAIL_VERIFICATION_REQUIRED", err.Error())
		return true
	}
	if errors.Is(err, errStorageLow) {
		writeError(w, 507, "STORAGE_LOW", err.Error())
		return true
	}
	if errors.Is(err, errStorageQuota) {
		writeError(w, 409, "STORAGE_QUOTA", err.Error())
		return true
	}
	return false
}

// Multipart parsing may spill to the OS temporary volume before StageAudio.
// Check it too, and recheck while receiving unknown/chunked request bodies.
type storageBody struct {
	io.ReadCloser
	store     *Store
	unchecked int64
}

func (b *storageBody) Read(data []byte) (int, error) {
	if b.unchecked <= 0 {
		for _, path := range []string{os.TempDir(), b.store.mediaRoot, filepath.Dir(b.store.dataPath)} {
			if err := b.store.checkSpace(path, int64(len(data))); err != nil {
				return 0, err
			}
		}
		b.unchecked = 1 << 20
	}
	n, err := b.ReadCloser.Read(data)
	b.unchecked -= int64(n)
	return n, err
}
func (a *API) protectUpload(w http.ResponseWriter, r *http.Request) bool {
	required := max(r.ContentLength, 0)
	if err := a.store.checkSpace(filepath.Dir(a.store.dataPath), 1<<20); err != nil {
		if !writeStorageError(w, err) {
			writeError(w, 500, "STORAGE_CHECK_FAILED", "无法检查数据盘空间")
		}
		return false
	}
	for _, path := range []string{os.TempDir(), a.store.mediaRoot} {
		if err := a.store.checkSpace(path, required); err != nil {
			if !writeStorageError(w, err) {
				writeError(w, 500, "STORAGE_CHECK_FAILED", "无法检查磁盘空间")
			}
			return false
		}
	}
	r.Body = &storageBody{ReadCloser: r.Body, store: a.store}
	return true
}
func (a *API) storage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v2/admin/storage/accounts" {
		a.accountStorage(w, r)
		return
	}
	if !a.requireAdmin(w, r) {
		return
	}
	if r.URL.Path == "/api/v2/admin/storage/cleanup-updates" && r.Method == "POST" {
		if a.updater == nil {
			writeJSON(w, 200, map[string]int64{"bytes": 0})
			return
		}
		if err := a.store.transaction(func(tx *sql.Tx) error {
			if err := requireAdminActor(tx, []MutationOptions{mutationOptions(r)}); err != nil {
				return err
			}
			return auditMutation(tx, "storage.cache_cleanup_requested", "updates", nil, nil, []MutationOptions{mutationOptions(r)})
		}); err != nil {
			writeError(w, 403, "ADMIN_REQUIRED", "administrator identity required")
			return
		}
		freed, err := a.updater.CleanupCache()
		if err != nil {
			writeError(w, 409, "CACHE_CLEANUP_FAILED", err.Error())
			return
		}
		writeJSON(w, 200, map[string]int64{"bytes": freed})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/admin/storage")
	if path == "" && r.Method == "GET" {
		result, err := a.store.StorageOverview()
		if err != nil {
			writeError(w, 500, "STORAGE_READ_FAILED", "无法读取容量信息")
			return
		}
		writeJSON(w, 200, result)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var err error
	if path == "/policy" && r.Method == "PUT" {
		var policy StoragePolicy
		if decodeStrict(r, &policy) != nil {
			writeError(w, 400, "INVALID_POLICY", "invalid storage policy")
			return
		}
		err = a.store.SetStoragePolicy(policy, mutationOptions(r))
	} else if strings.HasPrefix(path, "/quotas/") && r.Method == "PUT" {
		var quota StorageQuota
		if decodeStrict(r, &quota) != nil {
			writeError(w, 400, "INVALID_QUOTA", "invalid quota")
			return
		}
		_, err = a.store.SetStorageQuota(strings.TrimPrefix(path, "/quotas/"), quota.LimitBytes, quota.Version, mutationOptions(r))
	} else {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if err != nil {
		status := 400
		if errors.Is(err, errVersionConflict) {
			status = 409
		}
		if errors.Is(err, errResourceHidden) {
			status = 404
		}
		writeError(w, status, "STORAGE_SAVE_FAILED", err.Error())
		return
	}
	w.WriteHeader(204)
}
