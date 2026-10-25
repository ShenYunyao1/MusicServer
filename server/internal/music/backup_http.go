package music

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

func (a *API) backups(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v2/admin/backups"), "/")
	if path == "" {
		switch r.Method {
		case "GET":
			items, err := a.store.BackupArchives()
			if err != nil {
				writeError(w, 500, "BACKUP_LIST_FAILED", err.Error())
				return
			}
			policy, err := a.store.BackupPolicy()
			if err != nil {
				writeError(w, 500, "BACKUP_POLICY_FAILED", "cannot read backup policy")
				return
			}
			sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
			writeJSON(w, 200, map[string]any{"items": items, "policy": policy, "job": a.store.BackupJob()})
		case "POST":
			include := true
			if r.ContentLength != 0 {
				var body struct {
					IncludeMedia *bool `json:"include_media"`
				}
				r.Body = http.MaxBytesReader(w, r.Body, 1024)
				if decodeStrict(r, &body) != nil {
					writeError(w, 400, "INVALID_BACKUP", "invalid backup request")
					return
				}
				if body.IncludeMedia != nil {
					include = *body.IncludeMedia
				}
			}
			job, err := a.store.StartBackup(include, false, mutationOptions(r))
			if err != nil {
				status := 500
				if errors.Is(err, errBackupBusy) {
					status = 409
				}
				writeError(w, status, "BACKUP_START_FAILED", err.Error())
				return
			}
			writeJSON(w, 202, job)
		default:
			writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		}
		return
	}
	if path == "policy" && r.Method == "PUT" {
		var policy BackupPolicy
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if decodeStrict(r, &policy) != nil {
			writeError(w, 400, "INVALID_BACKUP_POLICY", "invalid backup policy")
			return
		}
		if err := a.store.SetBackupPolicy(policy, mutationOptions(r)); err != nil {
			writeError(w, 400, "INVALID_BACKUP_POLICY", err.Error())
			return
		}
		writeJSON(w, 200, policy)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] == "jobs" && r.Method == "DELETE" {
		a.store.backupMu.Lock()
		if a.store.backupJob.ID != parts[1] {
			a.store.backupMu.Unlock()
			writeError(w, 404, "BACKUP_JOB_NOT_FOUND", "backup task not found")
			return
		}
		if a.store.backupCancel != nil {
			a.store.backupJob.State = "cancelling"
			a.store.backupCancel()
		}
		a.store.backupMu.Unlock()
		w.WriteHeader(204)
		return
	}
	name, err := backupArchiveName(parts[0])
	if err != nil {
		writeError(w, 404, "BACKUP_NOT_FOUND", "backup not found")
		return
	}
	if len(parts) == 1 && r.Method == "DELETE" {
		if err := a.store.DeleteBackup(parts[0], mutationOptions(r)); err != nil {
			status := 500
			if errors.Is(err, os.ErrNotExist) {
				status = 404
			}
			if errors.Is(err, errBackupInUse) {
				status = 409
			}
			writeError(w, status, "BACKUP_DELETE_FAILED", err.Error())
			return
		}
		w.WriteHeader(204)
		return
	}
	if len(parts) == 2 && parts[1] == "download" && (r.Method == "GET" || r.Method == "HEAD") {
		root, err := os.OpenRoot(a.store.backupDirectory())
		if err != nil {
			writeError(w, 404, "BACKUP_NOT_FOUND", "backup not found")
			return
		}
		defer root.Close()
		a.store.backupMu.Lock()
		file, err := root.Open(name)
		if err != nil {
			a.store.backupMu.Unlock()
			writeError(w, 404, "BACKUP_NOT_FOUND", "backup not found")
			return
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			a.store.backupMu.Unlock()
			writeError(w, 404, "BACKUP_NOT_FOUND", "backup not found")
			return
		}
		if a.store.backupReaders == nil {
			a.store.backupReaders = map[string]int{}
		}
		a.store.backupReaders[parts[0]]++
		a.store.backupMu.Unlock()
		defer func() {
			file.Close()
			a.store.backupMu.Lock()
			a.store.backupReaders[parts[0]]--
			if a.store.backupReaders[parts[0]] == 0 {
				delete(a.store.backupReaders, parts[0])
			}
			a.store.backupMu.Unlock()
		}()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
		w.Header().Set("ETag", fmt.Sprintf("\"backup-%s\"", parts[0]))
		http.ServeContent(w, r, name, info.ModTime(), file)
		return
	}
	writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
}
