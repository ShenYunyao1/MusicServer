package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"musicserver/internal/activity"

	"github.com/google/uuid"
)

const tagBatchSchema = `CREATE TABLE tag_batches (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 payload TEXT NOT NULL CHECK(json_valid(payload)),created_at TEXT NOT NULL,expires_at TEXT NOT NULL);
CREATE INDEX tag_batches_owner_expiry ON tag_batches(user_id,expires_at);`

const tagBatchSize = 32
const tagBatchMaxTracks = 5000

var errTagBatchEmpty = errors.New("no tracks match the batch selection")

type TagBatchQuery struct {
	Search     string               `json:"q"`
	Filter     string               `json:"filter"`
	Conditions *TrackFilter         `json:"conditions,omitempty"`
	Baseline   *TrackFilterBaseline `json:"baseline,omitempty"`
}
type TagBatchInput struct {
	Scope     string         `json:"scope"`
	TrackIDs  []string       `json:"track_ids,omitempty"`
	Query     *TagBatchQuery `json:"query,omitempty"`
	AddIDs    []string       `json:"add_ids,omitempty"`
	RemoveIDs []string       `json:"remove_ids,omitempty"`
}
type TagBatchItem struct {
	TrackID string `json:"track_id"`
	Version int64  `json:"version"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
}
type TagBatch struct {
	ID            string         `json:"id"`
	Scope         string         `json:"scope"`
	State         string         `json:"state"`
	Version       int64          `json:"version"`
	Total         int            `json:"total"`
	Completed     int            `json:"completed"`
	Conflicts     int            `json:"conflicts"`
	Failed        int            `json:"failed"`
	Cancelled     int            `json:"cancelled"`
	Items         []TagBatchItem `json:"items"`
	AddIDs        []string       `json:"add_ids"`
	RemoveIDs     []string       `json:"remove_ids"`
	CommonTagIDs  []string       `json:"common_tag_ids"`
	PartialTagIDs []string       `json:"partial_tag_ids"`
	CreatedAt     string         `json:"created_at"`
	ExpiresAt     string         `json:"expires_at"`
	FinishedAt    string         `json:"finished_at,omitempty"`
	Error         string         `json:"error,omitempty"`
}

// Summary projection intentionally excludes track UUIDs, dictionary IDs and
// operations. A reloaded page can find its own task without downloading every
// frozen selection merely to render the task picker.
type TagBatchSummary struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	State     string `json:"state"`
	Version   int64  `json:"version"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	Conflicts int    `json:"conflicts"`
	Failed    int    `json:"failed"`
	Cancelled int    `json:"cancelled"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}
type tagBatchRuntime struct {
	mu       sync.Mutex
	running  map[string]bool
	failures map[string]string
	// Deterministic cancellation/revocation regression between committed chunks.
	afterChunk func()
}

func readTagBatch(tx *sql.Tx, auth authContext, id string) (TagBatch, error) {
	var job TagBatch
	if _, err := tagActor(tx, auth); err != nil {
		return job, err
	}
	var raw string
	if err := tx.QueryRow("SELECT payload FROM tag_batches WHERE id=? AND user_id=? AND expires_at>?", id, auth.userID, persistedTime(time.Now())).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return job, errResourceHidden
		}
		return job, err
	}
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return job, err
	}
	return job, nil
}
func saveTagBatch(tx *sql.Tx, job *TagBatch) error {
	if job.State == "preview" || job.State == "running" {
		job.FinishedAt = ""
	} else if job.FinishedAt == "" {
		job.FinishedAt = persistedTime(time.Now())
	}
	job.Completed, job.Conflicts, job.Failed, job.Cancelled = 0, 0, 0, 0
	for _, item := range job.Items {
		switch item.State {
		case "completed":
			job.Completed++
		case "conflict":
			job.Conflicts++
		case "failed":
			job.Failed++
		case "cancelled":
			job.Cancelled++
		}
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE tag_batches SET payload=? WHERE id=?", string(raw), job.ID)
	if err != nil {
		return err
	}
	return pruneTagBatchHistory(tx, job.ID)
}

func pruneTagBatchHistory(tx *sql.Tx, id string) error {
	// Keep only the most recently finished 100 tasks for this account. Active
	// previews/workers have their separate admission limit and are never pruned.
	_, err := tx.Exec(`DELETE FROM tag_batches WHERE user_id=(SELECT user_id FROM tag_batches WHERE id=?) AND id IN (
SELECT id FROM tag_batches WHERE user_id=(SELECT user_id FROM tag_batches WHERE id=?) AND json_extract(payload,'$.state') NOT IN ('preview','running')
ORDER BY COALESCE(json_extract(payload,'$.finished_at'),created_at) DESC,id DESC LIMIT -1 OFFSET 100)`, id, id)
	return err
}
func validateTagBatchEdits(tx *sql.Tx, auth authContext, scope string, add, remove []string) error {
	actor, err := tagActor(tx, auth)
	if err != nil {
		return err
	}
	scopeID, err := tagScopeID(actor.ID, scope)
	if err != nil {
		return err
	}
	if err := validateTrackTagIDs(add, remove); err != nil {
		return err
	}
	for _, ids := range [][]string{add, remove} {
		for _, id := range ids {
			if !tagUUID(id) {
				return errTagInvalid
			}
			tag, err := readTag(tx, id)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errResourceHidden
				}
				return err
			}
			if err := authorizeTag(actor, tag, false); err != nil {
				return err
			}
			if tag.scopeID != scopeID || tag.Deleted {
				return errResourceHidden
			}
		}
	}
	return nil
}
func tagBatchTrack(tx *sql.Tx, auth authContext, scope, id string) (int64, error) {
	_, err := readTrackFrom(tx, id, false)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errResourceHidden
		}
		return 0, err
	}
	actor, err := tagActor(tx, auth)
	if err != nil {
		return 0, err
	}
	scopeID, err := tagScopeID(actor.ID, scope)
	if err != nil {
		return 0, err
	}
	return trackTagVersion(tx, id, scopeID)
}

func (s *Store) PreviewTagBatch(auth authContext, input TagBatchInput) (TagBatch, error) {
	var job TagBatch
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		if input.Scope != "public" && input.Scope != "personal" || input.Query != nil && len(input.TrackIDs) != 0 {
			return errTagInvalid
		}
		if err := validateTagBatchEdits(tx, auth, input.Scope, input.AddIDs, input.RemoveIDs); err != nil {
			return err
		}
		now := time.Now()
		if _, err := tx.Exec("DELETE FROM tag_batches WHERE expires_at<=?", persistedTime(now)); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM tag_batches WHERE user_id=? AND json_extract(payload,'$.state') IN ('preview','running')", auth.userID).Scan(&count); err != nil {
			return err
		}
		if count >= 10 {
			return errTagLimit
		}
		ids := slices.Clone(input.TrackIDs)
		if input.Query != nil && input.Query.Conditions != nil {
			if input.Query.Search != "" || input.Query.Filter != "" {
				return errTagInvalid
			}
			var err error
			ids, err = filteredTagBatchIDs(tx, auth, *input.Query)
			if err != nil {
				return err
			}
		} else if input.Query != nil {
			q := input.Query
			if q.Baseline != nil {
				return errTagInvalid
			}
			if !utf8.ValidString(q.Search) || utf8.RuneCountInString(q.Search) > 256 {
				return errTagInvalid
			}
			if q.Filter == "" {
				q.Filter = "all"
			}
			switch q.Filter {
			case "all", "mine", "managed", "orphan", "missing":
			default:
				return errTagInvalid
			}
			for offset := 0; ; offset += 100 {
				var page browsePage
				if err := browseTracks(tx, auth, browseQuery{Limit: 100, Offset: offset, Search: strings.TrimSpace(q.Search), Filter: q.Filter}, &page); err != nil {
					return err
				}
				if page.Total > tagBatchMaxTracks {
					return errTagLimit
				}
				for _, track := range page.Items {
					id, ok := track["id"].(string)
					if !ok {
						return errTagInvalid
					}
					ids = append(ids, id)
				}
				if offset+len(page.Items) >= page.Total {
					break
				}
			}
		}
		if len(ids) == 0 {
			return errTagBatchEmpty
		}
		if len(ids) > tagBatchMaxTracks {
			return errTagLimit
		}
		seen := map[string]bool{}
		job = TagBatch{ID: uuid.NewString(), Scope: input.Scope, State: "preview", Version: 1, Total: len(ids), Items: []TagBatchItem{}, AddIDs: slices.Clone(input.AddIDs), RemoveIDs: slices.Clone(input.RemoveIDs), CommonTagIDs: []string{}, PartialTagIDs: []string{}, CreatedAt: persistedTime(now), ExpiresAt: persistedTime(now.Add(24 * time.Hour))}
		counts := map[string]int{}
		scopeID, _ := tagScopeID(auth.userID, input.Scope)
		for _, id := range ids {
			if !tagUUID(id) || seen[id] {
				return errTagInvalid
			}
			seen[id] = true
			version, err := tagBatchTrack(tx, auth, input.Scope, id)
			if err != nil {
				return err
			}
			job.Items = append(job.Items, TagBatchItem{TrackID: id, Version: version, State: "pending"})
			tags, err := trackTagsInScope(tx, id, scopeID)
			if err != nil {
				return err
			}
			for _, tag := range tags {
				counts[tag.ID]++
			}
		}
		for id, count := range counts {
			if count == len(ids) {
				job.CommonTagIDs = append(job.CommonTagIDs, id)
			} else {
				job.PartialTagIDs = append(job.PartialTagIDs, id)
			}
		}
		sort.Strings(job.CommonTagIDs)
		sort.Strings(job.PartialTagIDs)
		raw, err := json.Marshal(job)
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO tag_batches VALUES(?,?,?,?,?)", job.ID, auth.userID, string(raw), job.CreatedAt, job.ExpiresAt)
		return err
	})
	return job, err
}

func (s *Store) GetTagBatch(auth authContext, id string) (TagBatch, error) {
	s.tagBatches.mu.Lock()
	defer s.tagBatches.mu.Unlock()
	var job TagBatch
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		var err error
		job, err = readTagBatch(tx, auth, id)
		if err != nil {
			return err
		}
		if job.State == "running" && !s.tagBatches.running[id] {
			job.State = "interrupted"
			job.Error = "INTERRUPTED"
			if failure := s.tagBatches.failures[id]; failure != "" {
				job.State = "failed"
				job.Error = failure
			}
			job.Version++
			if err := saveTagBatch(tx, &job); err != nil {
				return err
			}
			return nil
		}
		return nil
	})
	if err == nil && job.State != "running" {
		delete(s.tagBatches.failures, id)
	}
	return job, err
}

func (s *Store) ListTagBatches(auth authContext) ([]TagBatchSummary, error) {
	s.tagBatches.mu.Lock()
	defer s.tagBatches.mu.Unlock()
	items := []TagBatchSummary{}
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		if _, err := tagActor(tx, auth); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT id,json_extract(payload,'$.scope'),json_extract(payload,'$.state'),json_extract(payload,'$.version'),
json_extract(payload,'$.total'),json_extract(payload,'$.completed'),json_extract(payload,'$.conflicts'),json_extract(payload,'$.failed'),json_extract(payload,'$.cancelled'),created_at,expires_at
FROM tag_batches WHERE user_id=? AND expires_at>? ORDER BY created_at DESC,id DESC LIMIT 20`, auth.userID, persistedTime(time.Now()))
		if err != nil {
			return err
		}
		for rows.Next() {
			var item TagBatchSummary
			if err := rows.Scan(&item.ID, &item.Scope, &item.State, &item.Version, &item.Total, &item.Completed, &item.Conflicts, &item.Failed, &item.Cancelled, &item.CreatedAt, &item.ExpiresAt); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for i := range items {
			item := &items[i]
			if item.State == "running" && !s.tagBatches.running[item.ID] {
				item.State = "interrupted"
				item.Version++
				code := "INTERRUPTED"
				if failure := s.tagBatches.failures[item.ID]; failure != "" {
					item.State = "failed"
					code = failure
				}
				if _, err := tx.Exec("UPDATE tag_batches SET payload=json_set(payload,'$.state',?,'$.error',?,'$.version',?,'$.finished_at',?) WHERE id=? AND user_id=?", item.State, code, item.Version, persistedTime(time.Now()), item.ID, auth.userID); err != nil {
					return err
				}
				if err := pruneTagBatchHistory(tx, item.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		for _, item := range items {
			if item.State != "running" {
				delete(s.tagBatches.failures, item.ID)
			}
		}
	}
	return items, err
}
func (s *Store) EditTagBatch(auth authContext, id string, version int64, add, remove []string) (TagBatch, error) {
	var job TagBatch
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		var err error
		job, err = readTagBatch(tx, auth, id)
		if err != nil {
			return err
		}
		if job.Version != version || job.State != "preview" {
			return errVersionConflict
		}
		if err := validateTagBatchEdits(tx, auth, job.Scope, add, remove); err != nil {
			return err
		}
		job.AddIDs = slices.Clone(add)
		job.RemoveIDs = slices.Clone(remove)
		job.Version++
		return saveTagBatch(tx, &job)
	})
	return job, err
}

func batchErrorCode(err error) string {
	switch {
	case errors.Is(err, errTagSession):
		return "AUTH_INVALID"
	case errors.Is(err, errVersionConflict):
		return "VERSION_CONFLICT"
	case errors.Is(err, errResourceHidden):
		return "NOT_FOUND"
	case errors.Is(err, errTagLimit):
		return "TAG_LIMIT"
	case errors.Is(err, errTagInvalid):
		return "TAG_INVALID"
	default:
		return "TAG_FAILED"
	}
}

func (s *Store) processTagBatchChunk(auth authContext, id string) (TagBatch, bool, error) {
	var job TagBatch
	changed := false
	bad := -1
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		var err error
		job, err = readTagBatch(tx, auth, id)
		if err != nil {
			return err
		}
		if job.State != "running" {
			return nil
		}
		processed := 0
		for i := range job.Items {
			if job.Items[i].State != "pending" {
				continue
			}
			item := &job.Items[i]
			_, didChange, err := changeTrackTagsTx(tx, auth, item.TrackID, job.Scope, item.Version, job.AddIDs, job.RemoveIDs)
			if err != nil {
				bad = i
				return err
			}
			changed = changed || didChange
			item.State = "completed"
			item.Error = ""
			processed++
			if processed == tagBatchSize {
				break
			}
		}
		pending := false
		for _, item := range job.Items {
			pending = pending || item.State == "pending"
		}
		if !pending {
			job.State = "completed"
			for _, item := range job.Items {
				if item.State != "completed" {
					job.State = "partial"
					break
				}
			}
		}
		job.Version++
		return saveTagBatch(tx, &job)
	})
	if err == nil {
		if changed && job.Scope == "public" {
			s.notifyCommunityCommit()
		}
		return job, job.State == "running", nil
	}
	// The failed chunk was rolled back. Persist only the failure state, never
	// its speculative completed counters or link changes.
	failure := err
	updateErr := s.tagTransaction(false, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRow("SELECT payload FROM tag_batches WHERE id=? AND user_id=?", id, auth.userID).Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			return err
		}
		if job.State != "running" {
			return nil
		}
		job.Error = batchErrorCode(failure)
		if errors.Is(failure, errTagSession) {
			job.State = "blocked"
		} else {
			if bad >= 0 {
				job.Items[bad].State = "failed"
				job.Items[bad].Error = job.Error
				if errors.Is(failure, errVersionConflict) {
					job.Items[bad].State = "conflict"
				}
			}
			if job.Total <= tagBatchSize || bad < 0 {
				job.State = "failed"
			}
		}
		job.Version++
		return saveTagBatch(tx, &job)
	})
	if updateErr != nil {
		return job, false, updateErr
	}
	return job, job.State == "running", failure
}

func (s *Store) StartTagBatch(auth authContext, id string, version int64) (TagBatch, error) {
	s.tagBatches.mu.Lock()
	defer s.tagBatches.mu.Unlock()
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if s.closed {
		return TagBatch{}, errors.New("server is closing")
	}
	var job TagBatch
	interrupted := false
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		var err error
		job, err = readTagBatch(tx, auth, id)
		if err != nil {
			return err
		}
		if job.State == "running" && !s.tagBatches.running[id] {
			job.State = "interrupted"
			job.Error = "INTERRUPTED"
			if failure := s.tagBatches.failures[id]; failure != "" {
				job.State = "failed"
				job.Error = failure
			}
			job.Version++
			interrupted = true
			return saveTagBatch(tx, &job)
		}
		if job.State == "running" || job.State == "completed" {
			return nil
		}
		if job.Version != version || job.State != "preview" {
			return errVersionConflict
		}
		if len(job.AddIDs)+len(job.RemoveIDs) == 0 {
			return errTagInvalid
		}
		if err := validateTagBatchEdits(tx, auth, job.Scope, job.AddIDs, job.RemoveIDs); err != nil {
			if errors.Is(err, errResourceHidden) {
				return errVersionConflict
			}
			return err
		}
		job.State = "running"
		job.Error = ""
		job.Version++
		return saveTagBatch(tx, &job)
	})
	if interrupted && err == nil {
		return job, errVersionConflict
	}
	if err != nil || job.State != "running" || s.tagBatches.running[id] {
		return job, err
	}
	if job.Total <= tagBatchSize {
		job, _, err = s.processTagBatchChunk(auth, id)
		return job, err
	}
	if s.tagBatches.running == nil {
		s.tagBatches.running = map[string]bool{}
	}
	s.tagBatches.running[id] = true
	s.workers.Add(1)
	go s.runTagBatch(auth, id)
	return job, nil
}
func (s *Store) runTagBatch(auth authContext, id string) {
	defer s.workers.Done()
	defer func() { s.tagBatches.mu.Lock(); delete(s.tagBatches.running, id); s.tagBatches.mu.Unlock() }()
	for {
		s.scanMu.Lock()
		closed := s.closed
		s.scanMu.Unlock()
		if closed {
			return
		}
		_, more, err := s.processTagBatchChunk(auth, id)
		if err != nil {
			_ = s.activity.Emit("tag.batch_failed", activity.Fields{UserID: auth.userID, Reason: batchErrorCode(err)})
			var persistedState string
			stateErr := s.db.QueryRow("SELECT json_extract(payload,'$.state') FROM tag_batches WHERE id=? AND user_id=?", id, auth.userID).Scan(&persistedState)
			if !more && (stateErr != nil || persistedState == "running") {
				s.tagBatches.mu.Lock()
				if s.tagBatches.failures == nil {
					s.tagBatches.failures = map[string]string{}
				}
				s.tagBatches.failures[id] = batchErrorCode(err)
				s.tagBatches.mu.Unlock()
			}
		}
		if !more {
			return
		}
		if s.tagBatches.afterChunk != nil {
			s.tagBatches.afterChunk()
		}
	}
}

func (s *Store) CancelTagBatch(auth authContext, id string, version int64) (TagBatch, error) {
	var job TagBatch
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		var err error
		job, err = readTagBatch(tx, auth, id)
		if err != nil {
			return err
		}
		if job.State == "cancelled" {
			return nil
		}
		if job.Version != version || job.State == "completed" {
			return errVersionConflict
		}
		job.State = "cancelled"
		job.Version++
		for i := range job.Items {
			if job.Items[i].State == "pending" {
				job.Items[i].State = "cancelled"
			}
		}
		return saveTagBatch(tx, &job)
	})
	return job, err
}
func (s *Store) RetryTagBatch(auth authContext, id string, version int64) (TagBatch, error) {
	s.tagBatches.mu.Lock()
	defer s.tagBatches.mu.Unlock()
	var job TagBatch
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		var err error
		job, err = readTagBatch(tx, auth, id)
		if err != nil {
			return err
		}
		if job.State == "preview" || job.State == "completed" {
			return nil
		}
		if job.Version != version || s.tagBatches.running[id] {
			return errVersionConflict
		}
		var active int
		if err := tx.QueryRow("SELECT count(*) FROM tag_batches WHERE user_id=? AND expires_at>? AND json_extract(payload,'$.state') IN ('preview','running')", auth.userID, persistedTime(time.Now())).Scan(&active); err != nil {
			return err
		}
		if active >= 10 {
			return errTagLimit
		}
		for i := range job.Items {
			item := &job.Items[i]
			if item.State == "completed" {
				continue
			}
			item.Version, err = tagBatchTrack(tx, auth, job.Scope, item.TrackID)
			if err != nil {
				return err
			}
			item.State = "pending"
			item.Error = ""
		}
		job.State = "preview"
		job.Error = ""
		job.Version++
		return saveTagBatch(tx, &job)
	})
	return job, err
}
