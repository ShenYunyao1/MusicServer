package music

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"musicserver/internal/activity"
)

const resumableChunkSize = 4 << 20
const resumableMaxChunks = 4096
const resumableTTL = 24 * time.Hour
const resumableMetaPrefix = "upload_resume/"
const resumableAPIPath = "/api/v2/uploads/resumable"

// The deterministic stage ID allows finish to recover the validated preview if
// the process stops after staging but before its checkpoint transaction commits.
type resumableStageIDKey struct{}

type resumableChunk struct {
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type resumableUpload struct {
	ID          string            `json:"id"`
	OwnerID     string            `json:"-"`
	Name        string            `json:"name"`
	Size        int64             `json:"size"`
	Offset      int64             `json:"offset"`
	ChunkSize   int               `json:"chunk_size"`
	Chunks      []resumableChunk  `json:"chunks"`
	State       string            `json:"state"`
	CreatedAt   time.Time         `json:"created_at"`
	ExpiresAt   time.Time         `json:"expires_at"`
	StageID     string            `json:"stage_id,omitempty"`
	Fields      map[string]string `json:"fields,omitempty"`
	Attachments map[string]string `json:"attachments,omitempty"`
	ReceiveMS   int64             `json:"receive_ms,omitempty"`
	Preview     *UploadPreview    `json:"-"`
}

// Identity and preview are persisted, but are not exposed by task responses.
type resumableCheckpoint struct {
	resumableUpload
	Owner    string         `json:"owner"`
	Finished *UploadPreview `json:"finished,omitempty"`
}

type resumableFinish struct {
	Fields      map[string]string `json:"fields"`
	Lyrics      []byte            `json:"lyrics"`
	Cover       []byte            `json:"cover"`
	CoverSource string            `json:"cover_source"`
}

var resumableCreateMu sync.Mutex
var resumableLocks [257]sync.Mutex
var errResumeCorrupt = errors.New("upload checkpoint or file is damaged; cancel and select the original file again")
var errResumeConflict = errors.New("upload offset, size or checksum conflicts with the confirmed checkpoint")
var errResumeExpiredPreview = errors.New("upload preview expired; cancel this task and upload again")
var errResumeLimit = errors.New("too many pending uploads (maximum 16 per account and 256 total)")
var errResumeBusy = errors.New("too many active uploads; retry shortly")

var resumableReceiveSlots = make(chan struct{}, 16)
var resumableFinishSlots = make(chan struct{}, 2)
var resumableAdmissionMu sync.Mutex
var resumableOwners = map[resumableAdmissionOwner]int{}

type resumableAdmissionOwner struct {
	store *Store
	owner string
}

// Reject excess bodies before allocating either a 4 MiB block or a finish JSON
// payload. Separate finish slots also bound cover/lyrics JSON decoding memory.
func (s *Store) admitResumeBody(owner string, finish bool) (func(), error) {
	slots := resumableReceiveSlots
	if finish {
		slots = resumableFinishSlots
	}
	key := resumableAdmissionOwner{s, owner}
	resumableAdmissionMu.Lock()
	defer resumableAdmissionMu.Unlock()
	if resumableOwners[key] >= 2 {
		return nil, errResumeBusy
	}
	select {
	case slots <- struct{}{}:
	default:
		return nil, errResumeBusy
	}
	resumableOwners[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			resumableAdmissionMu.Lock()
			defer resumableAdmissionMu.Unlock()
			resumableOwners[key]--
			if resumableOwners[key] == 0 {
				delete(resumableOwners, key)
			}
			<-slots
		})
	}, nil
}

func validResumeID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

func resumableLock(id string) *sync.Mutex {
	h := sha256.Sum256([]byte(id))
	return &resumableLocks[(int(h[0])<<8|int(h[1]))%len(resumableLocks)]
}

func resumeFilePath(id string) string { return ".staging/resume/" + id + ".part" }

func resumeActor(reader sqlReader, auth authContext) error {
	u, err := readUser(reader, auth.userID)
	if err != nil || !auth.session || !u.Enabled || u.DeletedAt != "" || u.MustChangePassword || u.AuthVersion != auth.authVersion {
		return errResourceHidden
	}
	var valid bool
	now := time.Now()
	if err := reader.QueryRow("SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND user_id=? AND auth_version=? AND revoked_at='' AND expires_at>? AND last_active_at>?)", auth.keyID, auth.userID, auth.authVersion, persistedTime(now), persistedTime(now.Add(-sessionIdle))).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errResourceHidden
	}
	return nil
}

func readResume(reader sqlReader, id string) (resumableUpload, error) {
	var raw string
	if !validResumeID(id) {
		return resumableUpload{}, errResourceHidden
	}
	if err := reader.QueryRow("SELECT value FROM meta WHERE key=?", resumableMetaPrefix+id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resumableUpload{}, errResourceHidden
		}
		return resumableUpload{}, err
	}
	var checkpoint resumableCheckpoint
	if json.Unmarshal([]byte(raw), &checkpoint) != nil || checkpoint.ID != id || checkpoint.Owner == "" {
		return resumableUpload{}, errResumeCorrupt
	}
	checkpoint.OwnerID, checkpoint.Preview = checkpoint.Owner, checkpoint.Finished
	return checkpoint.resumableUpload, nil
}

func saveResume(tx *sql.Tx, task resumableUpload) error {
	encoded, err := json.Marshal(resumableCheckpoint{task, task.OwnerID, task.Preview})
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", resumableMetaPrefix+task.ID, string(encoded))
	return err
}

func (s *Store) resumeIDs() ([]string, error) {
	rows, err := s.db.Query("SELECT key FROM meta WHERE key LIKE 'upload_resume/%' ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		ids = append(ids, strings.TrimPrefix(key, resumableMetaPrefix))
	}
	return ids, rows.Err()
}

func (s *Store) resumeOwned(id string, auth authContext) (resumableUpload, error) {
	if err := resumeActor(s.db, auth); err != nil {
		return resumableUpload{}, err
	}
	task, err := readResume(s.db, id)
	if err != nil {
		return task, err
	}
	if task.OwnerID != auth.userID || !time.Now().UTC().Before(task.ExpiresAt) {
		return resumableUpload{}, errResourceHidden
	}
	return task, nil
}

// recoverResume discards unacknowledged tails. At startup it also verifies every
// acknowledged block before any offset is advertised to another browser.
func (s *Store) recoverResume(task resumableUpload, verify bool) error {
	if task.State == "canceled" || task.State == "ready" {
		return nil
	}
	if task.State != "uploading" || task.Size <= 0 || task.Offset < 0 || task.Offset > task.Size || len(task.Chunks) > resumableMaxChunks {
		return errResumeCorrupt
	}
	var offset int64
	for _, chunk := range task.Chunks {
		decoded, err := hex.DecodeString(chunk.SHA256)
		if chunk.Offset != offset || chunk.Size <= 0 || chunk.Size > resumableChunkSize || err != nil || len(decoded) != sha256.Size || chunk.Size > task.Size-offset {
			return errResumeCorrupt
		}
		offset += chunk.Size
	}
	if offset != task.Offset {
		return errResumeCorrupt
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.OpenFile(resumeFilePath(task.ID), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return errResumeCorrupt
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < task.Offset {
		return errResumeCorrupt
	}
	if info.Size() > task.Offset {
		if err := f.Truncate(task.Offset); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return err
		}
	}
	if verify {
		for _, chunk := range task.Chunks {
			hash := sha256.New()
			if _, err := io.CopyN(hash, f, chunk.Size); err != nil {
				return errResumeCorrupt
			}
			if hex.EncodeToString(hash.Sum(nil)) != chunk.SHA256 {
				return errResumeCorrupt
			}
		}
	}
	return nil
}

func (s *Store) createResume(auth authContext, name string, size int64, fields, attachments map[string]string) (resumableUpload, error) {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || len(name) > 255 || !audioExtensions[strings.ToLower(filepath.Ext(name))] || size <= 0 || size > s.Settings().MaxUploadBytes {
		return resumableUpload{}, errors.New("unsupported audio name or configured upload size limit exceeded")
	}
	if err := validMetadataFields(fields); err != nil {
		return resumableUpload{}, err
	}
	for kind, name := range attachments {
		if (kind != "cover" && kind != "lyrics") || len(name) > 255 {
			return resumableUpload{}, errors.New("invalid attachment name")
		}
	}
	// File creation and the bounded counter are coordinated, never chunk bodies.
	resumableCreateMu.Lock()
	defer resumableCreateMu.Unlock()
	if err := checkResumeUploadActor(s.db, auth); err != nil {
		return resumableUpload{}, err
	}
	if err := s.checkSpace(s.mediaRoot, 0); err != nil {
		return resumableUpload{}, err
	}
	if err := s.checkSpace(filepath.Dir(s.dataPath), 1<<20); err != nil {
		return resumableUpload{}, err
	}
	id, err := randomToken(16)
	if err != nil {
		return resumableUpload{}, err
	}
	now := time.Now().UTC()
	task := resumableUpload{ID: id, OwnerID: auth.userID, Name: name, Size: size, ChunkSize: resumableChunkSize, Chunks: []resumableChunk{}, State: "uploading", CreatedAt: now, ExpiresAt: now.Add(resumableTTL), Fields: fields, Attachments: attachments}
	lock := resumableLock(id)
	lock.Lock()
	defer lock.Unlock()
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return resumableUpload{}, err
	}
	defer root.Close()
	if err = root.MkdirAll(".staging/resume", 0o700); err != nil {
		return resumableUpload{}, err
	}
	f, err := root.OpenFile(resumeFilePath(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return resumableUpload{}, err
	}
	err = errors.Join(f.Sync(), f.Close())
	if err != nil {
		_ = root.Remove(resumeFilePath(id))
		return resumableUpload{}, err
	}
	err = s.transaction(func(tx *sql.Tx) error {
		if err := checkResumeUploadActor(tx, auth); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT value FROM meta WHERE key LIKE 'upload_resume/%'")
		if err != nil {
			return err
		}
		var ownerCount, total int
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				_ = rows.Close()
				return err
			}
			var c resumableCheckpoint
			if json.Unmarshal([]byte(raw), &c) == nil && c.State != "canceled" && now.Before(c.ExpiresAt) {
				total++
				if c.Owner == auth.userID {
					ownerCount++
				}
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		if total >= 256 || ownerCount >= 16 {
			return errResumeLimit
		}
		return saveResume(tx, task)
	})
	if err != nil {
		_ = root.Remove(resumeFilePath(id))
	}
	return task, err
}

func (s *Store) patchResume(ctx context.Context, auth authContext, task resumableUpload, offset int64, digest string, body io.Reader) (resumableUpload, error) {
	started := time.Now()
	if task.State != "uploading" {
		return task, errResumeConflict
	}
	if err := s.recoverResume(task, false); err != nil {
		return task, err
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, body}, resumableChunkSize+1))
	if err != nil {
		return task, err
	}
	if len(data) == 0 || len(data) > resumableChunkSize || offset < 0 || offset > task.Size || int64(len(data)) > task.Size-offset {
		return task, errResumeConflict
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != digest {
		return task, errResumeConflict
	}
	if offset < task.Offset {
		for _, chunk := range task.Chunks {
			if chunk.Offset == offset && chunk.Size == int64(len(data)) && chunk.SHA256 == digest {
				return task, checkResumeUploadActor(s.db, auth)
			}
		}
		return task, errResumeConflict
	}
	if offset != task.Offset || len(task.Chunks) >= resumableMaxChunks {
		return task, errResumeConflict
	}
	if err := ctx.Err(); err != nil {
		return task, err
	}
	if err := checkResumeUploadActor(s.db, auth); err != nil {
		return task, err
	}
	if err := s.checkSpace(filepath.Dir(s.dataPath), 1<<20+int64(len(task.Chunks))*256); err != nil {
		return task, err
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return task, err
	}
	defer root.Close()
	f, err := root.OpenFile(resumeFilePath(task.ID), os.O_WRONLY, 0)
	if err != nil {
		return task, err
	}
	defer f.Close()
	if _, err = f.Seek(task.Offset, io.SeekStart); err != nil {
		return task, err
	}
	confirmed := false
	defer func() {
		if !confirmed {
			_ = f.Truncate(task.Offset)
			_ = f.Sync()
		}
	}()
	written, err := (storageWriter{s, s.mediaRoot, f}).Write(data)
	if err != nil {
		return task, err
	}
	if written != len(data) {
		return task, io.ErrShortWrite
	}
	if err = f.Sync(); err != nil {
		return task, err
	}
	next := task
	next.ReceiveMS += time.Since(started).Milliseconds()
	next.Offset += int64(len(data))
	next.Chunks = append(append([]resumableChunk{}, task.Chunks...), resumableChunk{offset, int64(len(data)), digest})
	if err := ctx.Err(); err != nil {
		return task, err
	}
	// A disabled/deleted actor cannot advance a checkpoint, even if its HTTP
	// authentication completed before the administrator's mutation.
	err = s.transaction(func(tx *sql.Tx) error {
		if err := checkResumeUploadActor(tx, auth); err != nil {
			return err
		}
		current, err := readResume(tx, task.ID)
		if err != nil {
			return err
		}
		if current.Offset != task.Offset || current.State != "uploading" || current.OwnerID != auth.userID || !time.Now().Before(current.ExpiresAt) {
			return errResumeConflict
		}
		return saveResume(tx, next)
	})
	if err != nil {
		return task, err
	}
	confirmed = true
	return next, nil
}

func resumeChecksum(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || parts[0] != "sha256" {
		return "", false
	}
	decoded, err := hex.DecodeString(parts[1])
	if err != nil || len(decoded) != sha256.Size {
		decoded, err = base64.StdEncoding.DecodeString(parts[1])
	}
	return hex.EncodeToString(decoded), err == nil && len(decoded) == sha256.Size
}

func (s *Store) finishResume(ctx context.Context, auth authContext, task resumableUpload, input resumableFinish, options MutationOptions) (UploadPreview, error) {
	if task.State == "canceled" {
		return UploadPreview{}, errResourceHidden
	}
	if task.State == "ready" {
		if task.Preview == nil || !time.Now().Before(task.Preview.ExpiresAt) {
			return UploadPreview{}, errResumeExpiredPreview
		}
		preview, exists, err := s.UploadStage(task.StageID)
		if err != nil {
			return UploadPreview{}, err
		} else if !exists {
			return UploadPreview{}, errResumeExpiredPreview
		}
		if preview.Track.OwnerID != auth.userID {
			return UploadPreview{}, errResourceHidden
		}
		return preview, checkResumeUploadActor(s.db, auth)
	}
	if task.Offset != task.Size {
		return UploadPreview{}, errResumeConflict
	}
	if err := s.recoverResume(task, true); err != nil {
		return UploadPreview{}, err
	}
	if err := checkResumeUploadActor(s.db, auth); err != nil {
		return UploadPreview{}, err
	}
	preview, exists, err := s.UploadStage(task.ID)
	if err != nil {
		return UploadPreview{}, err
	}
	if exists && preview.Track.OwnerID != auth.userID {
		return UploadPreview{}, errResourceHidden
	}
	if !exists {
		fields := map[string]string{}
		for key, value := range task.Fields {
			fields[key] = value
		}
		for key, value := range input.Fields {
			fields[key] = value
		}
		root, err := os.OpenRoot(s.mediaRoot)
		if err != nil {
			return UploadPreview{}, err
		}
		defer root.Close()
		file, err := root.Open(resumeFilePath(task.ID))
		if err != nil {
			return UploadPreview{}, err
		}
		defer file.Close()
		ctx = context.WithValue(ctx, uploadActivityKey{}, activity.Fields{UserID: auth.userID, Bytes: task.Size, Timings: map[string]int64{"receive_ms": task.ReceiveMS}})
		preview, err = s.stageAudioWithAssetsContext(context.WithValue(ctx, resumableStageIDKey{}, task.ID), task.Name, file, fields, uploadAssets{Lyrics: input.Lyrics, Cover: input.Cover, CoverSource: input.CoverSource}, options)
		if err != nil {
			return UploadPreview{}, err
		}
	}
	if err := s.checkSpace(filepath.Dir(s.dataPath), 1<<20); err != nil {
		return UploadPreview{}, err
	}
	next := task
	next.State = "ready"
	next.StageID = preview.ID
	next.Preview = &preview
	err = s.transaction(func(tx *sql.Tx) error {
		if err := checkResumeUploadActor(tx, auth); err != nil {
			return err
		}
		return saveResume(tx, next)
	})
	if err != nil {
		return UploadPreview{}, err
	}
	root, openErr := os.OpenRoot(s.mediaRoot)
	if openErr == nil {
		_ = root.Remove(resumeFilePath(task.ID))
		_ = root.Close()
	}
	return preview, nil
}

func (s *Store) cancelResume(auth authContext, task resumableUpload) error {
	next := task
	next.State = "canceled"
	next.Chunks = []resumableChunk{}
	next.Fields = nil
	next.Attachments = nil
	next.Preview = nil
	if err := s.transaction(func(tx *sql.Tx) error {
		if err := resumeActor(tx, auth); err != nil {
			return err
		}
		return saveResume(tx, next)
	}); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(resumeFilePath(task.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.removeResumeStage(task)
}

func (s *Store) removeResumeStage(task resumableUpload) error {
	id := task.StageID
	if id == "" {
		id = task.ID
	}
	// This also handles a finish that persisted its deterministic preview but
	// stopped before the task checkpoint acquired its stage_id.
	var owner string
	err := s.db.QueryRow("SELECT user_id FROM upload_staging WHERE id=?", id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if owner != task.OwnerID {
		return errResourceHidden
	}
	err = s.DeleteUploadStage(id)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func writeResumeError(w http.ResponseWriter, err error) {
	if writeStorageError(w, err) {
		return
	}
	switch {
	case errors.Is(err, errResourceHidden), errors.Is(err, os.ErrNotExist):
		writeError(w, 404, "NOT_FOUND", "resource not found")
	case errors.Is(err, errStorageLow):
		writeError(w, 507, "STORAGE_LOW", err.Error())
	case errors.Is(err, errResumeLimit):
		writeError(w, 429, "UPLOAD_TASK_LIMIT", err.Error())
	case errors.Is(err, errResumeBusy):
		w.Header().Set("Retry-After", "1")
		writeError(w, 429, "UPLOAD_BUSY", err.Error())
	case errors.Is(err, errResumeExpiredPreview):
		writeError(w, 410, "UPLOAD_PREVIEW_EXPIRED", err.Error())
	case errors.Is(err, errResumeConflict):
		writeError(w, 409, "UPLOAD_CONFLICT", err.Error())
	case errors.Is(err, errResumeCorrupt):
		writeError(w, 409, "UPLOAD_CORRUPT", err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, 408, "UPLOAD_INTERRUPTED", "upload interrupted; query the confirmed offset before retrying")
	default:
		writeError(w, 400, "UPLOAD_INVALID", err.Error())
	}
}

func decodeResumeJSON(w http.ResponseWriter, r *http.Request, limit int64, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

// resumableUploads runs after normal authentication and CSRF validation, before
// generic resource authorization. API keys can never create/read upload tasks.
func (a *API) resumableUploads(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != resumableAPIPath && !strings.HasPrefix(r.URL.Path, resumableAPIPath+"/") {
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
	if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodPut {
		if err := checkUploadEmailPolicy(a.store.db, auth.userID); err != nil {
			writeResumeError(w, err)
			return true
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == resumableAPIPath {
		switch r.Method {
		case http.MethodPost:
			var input struct {
				Name        string            `json:"name"`
				Size        int64             `json:"size"`
				Fields      map[string]string `json:"fields"`
				Attachments map[string]string `json:"attachments"`
			}
			if err := decodeResumeJSON(w, r, 3<<20, &input); err != nil {
				writeResumeError(w, err)
				return true
			}
			task, err := a.store.createResume(auth, input.Name, input.Size, input.Fields, input.Attachments)
			if err != nil {
				writeResumeError(w, err)
			} else {
				w.Header().Set("Location", resumableAPIPath+"/"+task.ID)
				w.Header().Set("Upload-Offset", "0")
				writeJSON(w, 201, task)
			}
		case http.MethodGet:
			ids, err := a.store.resumeIDs()
			if err != nil {
				writeResumeError(w, err)
				return true
			}
			items := []resumableUpload{}
			for _, id := range ids {
				lock := resumableLock(id)
				lock.Lock()
				task, e := a.store.resumeOwned(id, auth)
				if e == nil && task.State == "uploading" {
					if err := a.store.recoverResume(task, false); err != nil {
						task.State = "corrupt"
					}
				}
				lock.Unlock()
				if e == nil && task.State != "canceled" {
					items = append(items, task)
				}
			}
			writeJSON(w, 200, map[string]any{"items": items})
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		}
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, resumableAPIPath+"/"), "/")
	if len(parts) > 2 || !validResumeID(parts[0]) || (len(parts) == 2 && parts[1] != "finish") {
		writeResumeError(w, errResourceHidden)
		return true
	}
	id := parts[0]
	if r.Method == http.MethodPatch || len(parts) == 2 && r.Method == http.MethodPost {
		// Preserve the same 404 for foreign and absent IDs even under load.
		if _, err := a.store.resumeOwned(id, auth); err != nil {
			writeResumeError(w, err)
			return true
		}
		release, err := a.store.admitResumeBody(auth.userID, len(parts) == 2)
		if err != nil {
			writeResumeError(w, err)
			return true
		}
		defer release()
	}
	lock := resumableLock(id)
	lock.Lock()
	defer lock.Unlock()
	task, err := a.store.resumeOwned(id, auth)
	if err != nil {
		writeResumeError(w, err)
		return true
	}
	if len(parts) == 2 {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			return true
		}
		var input resumableFinish
		if err := decodeResumeJSON(w, r, 32<<20, &input); err != nil {
			writeResumeError(w, err)
			return true
		}
		preview, err := a.store.finishResume(r.Context(), auth, task, input, mutationOptions(r))
		if err != nil {
			writeResumeError(w, err)
		} else {
			preview.Track = publicTrack(preview.Track, auth)
			writeJSON(w, 200, preview)
		}
		return true
	}
	if task.State == "canceled" && r.Method != http.MethodDelete {
		writeResumeError(w, errResourceHidden)
		return true
	}
	switch r.Method {
	case http.MethodGet:
		if err := a.store.recoverResume(task, false); err != nil {
			writeResumeError(w, err)
		} else {
			w.Header().Set("Upload-Offset", strconv.FormatInt(task.Offset, 10))
			writeJSON(w, 200, task)
		}
	case http.MethodPatch:
		offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
		digest, ok := resumeChecksum(r.Header.Get("Upload-Checksum"))
		if err != nil || !ok {
			writeResumeError(w, errResumeConflict)
			return true
		}
		if r.ContentLength > resumableChunkSize {
			writeError(w, 413, "UPLOAD_CHUNK_TOO_LARGE", "chunk exceeds 4 MiB")
			return true
		}
		next, err := a.store.patchResume(r.Context(), auth, task, offset, digest, r.Body)
		if err != nil {
			w.Header().Set("Upload-Offset", strconv.FormatInt(task.Offset, 10))
			writeResumeError(w, err)
		} else {
			w.Header().Set("Upload-Offset", strconv.FormatInt(next.Offset, 10))
			writeJSON(w, 200, next)
		}
	case http.MethodDelete:
		if err := a.store.cancelResume(auth, task); err != nil {
			writeResumeError(w, err)
		} else {
			w.WriteHeader(204)
		}
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
	}
	return true
}

// InitializeResumableUploads is called after SQLite opens, before serving. No
// schema changes are needed: temporary checkpoints live in existing meta rows.
func (s *Store) InitializeResumableUploads() error {
	ids, err := s.resumeIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		lock := resumableLock(id)
		lock.Lock()
		task, e := readResume(s.db, id)
		if e == nil && task.State == "uploading" && time.Now().Before(task.ExpiresAt) {
			e = s.recoverResume(task, true)
			if errors.Is(e, errResumeCorrupt) {
				task.State = "corrupt"
				e = s.transaction(func(tx *sql.Tx) error { return saveResume(tx, task) })
			}
		}
		lock.Unlock()
		if e != nil && !errors.Is(e, errResumeCorrupt) {
			return fmt.Errorf("recover upload %s: %w", id, e)
		}
	}
	return s.CleanupResumableUploads()
}

// CleanupResumableUploads skips a task while a chunk/finish/cancel owns its lock.
// The resume subdirectory is deliberately excluded from normal stage sweeping.
func (s *Store) CleanupResumableUploads() error {
	ids, err := s.resumeIDs()
	if err != nil {
		return err
	}
	retained := map[string]bool{}
	var cleanupErrors []error
	root, err := os.OpenRoot(s.mediaRoot)
	if errors.Is(err, os.ErrNotExist) && len(ids) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	for _, id := range ids {
		if !validResumeID(id) {
			continue
		}
		retained[id+".part"] = true
		lock := resumableLock(id)
		if !lock.TryLock() {
			continue
		}
		task, e := readResume(s.db, id)
		expired := e != nil || !time.Now().Before(task.ExpiresAt)
		if !expired {
			u, userErr := readUser(s.db, task.OwnerID)
			expired = userErr != nil || !u.Enabled || u.MustChangePassword || u.DeletedAt != ""
		}
		if expired {
			if e == nil {
				e = s.removeResumeStage(task)
			}
			if e != nil && !errors.Is(e, errResumeCorrupt) {
				lock.Unlock()
				cleanupErrors = append(cleanupErrors, e)
				continue
			}
			_, e = s.db.Exec("DELETE FROM meta WHERE key=?", resumableMetaPrefix+id)
			if e == nil {
				e = root.Remove(resumeFilePath(id))
				if errors.Is(e, os.ErrNotExist) {
					e = nil
				}
				delete(retained, id+".part")
			}
		} else if task.State == "ready" || task.State == "canceled" {
			e = root.Remove(resumeFilePath(id))
			if errors.Is(e, os.ErrNotExist) {
				e = nil
			}
			if e == nil && task.State == "canceled" {
				e = s.removeResumeStage(task)
			}
		}
		lock.Unlock()
		if e != nil {
			cleanupErrors = append(cleanupErrors, e)
		}
	}
	directory, err := root.Open(".staging/resume")
	if errors.Is(err, os.ErrNotExist) {
		return errors.Join(cleanupErrors...)
	}
	if err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	entries, err := directory.ReadDir(-1)
	_ = directory.Close()
	if err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".part")
		if !entry.Type().IsRegular() || name != id+".part" || !validResumeID(id) || retained[name] {
			continue
		}
		lock := resumableLock(id)
		if !lock.TryLock() {
			continue
		}
		// A task may have been created after the initial query: recheck under
		// its own lock so cleanup cannot unlink a live chunk's file.
		_, lookupErr := readResume(s.db, id)
		if errors.Is(lookupErr, errResourceHidden) {
			if e := root.Remove(".staging/resume/" + name); e != nil && !errors.Is(e, os.ErrNotExist) {
				cleanupErrors = append(cleanupErrors, e)
			}
		}
		lock.Unlock()
	}
	return errors.Join(cleanupErrors...)
}
