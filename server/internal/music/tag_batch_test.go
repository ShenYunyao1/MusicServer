package music

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func batchStore(t *testing.T) (*Store, authContext) {
	t.Helper()
	s, auth := tagTestStore(t)
	if _, err := s.db.Exec(tagBatchSchema); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	return s, auth
}

func batchTracks(t *testing.T, s *Store, n int) []string {
	t.Helper()
	ids := []string{}
	base := s.Tracks()[0]
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			track := base
			track.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
			track.Title = fmt.Sprintf("Batch %04d", i)
			track.Path = fmt.Sprintf("batch-%04d.mp3", i)
			if err := writeTrack(tx, track); err != nil {
				return err
			}
			ids = append(ids, track.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestTagBatchFrozenPreviewAndAtomicConflict(t *testing.T) {
	s, auth := batchStore(t)
	ids := batchTracks(t, s, 2)
	tag := mustLinkTag(t, s, auth, "personal", "Batch private")
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: ids, AddIDs: []string{tag.ID}})
	if err != nil || job.Total != 2 || job.State != "preview" {
		t.Fatal(job, err)
	}
	if _, err := s.ChangeTrackTags(auth, ids[1], "personal", 1, []string{tag.ID}, nil); err != nil {
		t.Fatal(err)
	}
	job, err = s.StartTagBatch(auth, job.ID, job.Version)
	if !errors.Is(err, errVersionConflict) || job.State != "failed" {
		t.Fatal(job, err)
	}
	view, err := s.GetTrackTags(auth, ids[0])
	if err != nil || len(view.Personal) != 0 {
		t.Fatal("small batch was not atomic", view, err)
	}
	job, err = s.RetryTagBatch(auth, job.ID, job.Version)
	if err != nil || job.State != "preview" {
		t.Fatal(job, err)
	}
	job, err = s.StartTagBatch(auth, job.ID, job.Version)
	if err != nil || job.State != "completed" || job.Completed != 2 {
		t.Fatal(job, err)
	}
	for _, id := range ids {
		v, _ := s.GetTrackTags(auth, id)
		if len(v.Personal) != 1 {
			t.Fatal(v)
		}
	}
}

func TestTagBatchQueryFreezesIDsAndProtectsOwner(t *testing.T) {
	s, auth := batchStore(t)
	ids := batchTracks(t, s, 2)
	tag := mustLinkTag(t, s, auth, "public", "Batch public")
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "public", Query: &TagBatchQuery{Search: "Batch", Filter: "all"}, AddIDs: []string{tag.ID}})
	if err != nil || job.Total != 2 {
		t.Fatal(job, err)
	}
	if err := s.tagTransaction(false, func(tx *sql.Tx) error {
		tr, _ := readTrackFrom(tx, ids[0], false)
		tr.ID = "00000000-0000-4000-8000-999999999999"
		tr.Path = "later.mp3"
		return writeTrack(tx, tr)
	}); err != nil {
		t.Fatal(err)
	}
	job, err = s.StartTagBatch(auth, job.ID, job.Version)
	if err != nil || job.Completed != 2 {
		t.Fatal(job, err)
	}
	v, err := s.GetTrackTags(auth, "00000000-0000-4000-8000-999999999999")
	if err != nil || len(v.Public) != 0 {
		t.Fatal("late track entered frozen batch", v, err)
	}
	other := auth
	other.userID = "not-owner"
	if _, err := s.GetTagBatch(other, job.ID); !errors.Is(err, errTagSession) {
		t.Fatal(err)
	}
}

func waitTagBatch(t *testing.T, s *Store, auth authContext, id, state string) TagBatch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := s.GetTagBatch(auth, id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == state {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("batch did not reach state", state)
	return TagBatch{}
}

func TestTagBatchLargeStopsAfterRevocationAndRetryRequiresPreview(t *testing.T) {
	s, auth := batchStore(t)
	ids := batchTracks(t, s, 65)
	tag := mustLinkTag(t, s, auth, "personal", "Private background")
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: ids, AddIDs: []string{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	after := make(chan struct{}, 1)
	release := make(chan struct{})
	s.tagBatches.afterChunk = func() {
		select {
		case after <- struct{}{}:
		default:
		}
		<-release
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	job, err = s.StartTagBatch(auth, job.ID, job.Version)
	if err != nil || job.State != "running" {
		t.Fatal(job, err)
	}
	select {
	case <-after:
	case <-time.After(5 * time.Second):
		t.Fatal("first chunk did not finish")
	}
	if err := s.RevokeSession(auth.userID, auth.keyID); err != nil {
		t.Fatal(err)
	}
	close(release)
	fresh := tagTestAuth(t, s, "admin")
	job = waitTagBatch(t, s, fresh, job.ID, "blocked")
	if job.Completed != 32 {
		t.Fatal("revocation did not stop following chunks", job)
	}
	job, err = s.RetryTagBatch(fresh, job.ID, job.Version)
	if err != nil || job.State != "preview" || job.Completed != 32 {
		t.Fatal(job, err)
	}
	view, _ := s.GetTrackTags(fresh, ids[64])
	if len(view.Personal) != 0 {
		t.Fatal("retry wrote before new confirmation")
	}
	job, err = s.StartTagBatch(fresh, job.ID, job.Version)
	if err != nil {
		t.Fatal(job, err)
	}
	job = waitTagBatch(t, s, fresh, job.ID, "completed")
	if job.Completed != 65 {
		t.Fatal(job)
	}
	view, _ = s.GetTrackTags(fresh, ids[0])
	if view.PersonalVersion != 2 {
		t.Fatal("retry reran already successful item", view)
	}
}

func TestTagBatchLargeCancellationAndInterruptedRestart(t *testing.T) {
	s, auth := batchStore(t)
	ids := batchTracks(t, s, 64)
	tag := mustLinkTag(t, s, auth, "public", "Background public")
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "public", TrackIDs: ids, AddIDs: []string{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	after := make(chan struct{}, 1)
	release := make(chan struct{})
	s.tagBatches.afterChunk = func() {
		select {
		case after <- struct{}{}:
		default:
		}
		<-release
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	job, err = s.StartTagBatch(auth, job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-after:
	case <-time.After(5 * time.Second):
		t.Fatal("first chunk did not finish")
	}
	job, err = s.GetTagBatch(auth, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.CancelTagBatch(auth, job.ID, job.Version)
	if err != nil || job.Completed != 32 || job.Cancelled != 32 {
		t.Fatal(job, err)
	}
	close(release)
	job = waitTagBatch(t, s, auth, job.ID, "cancelled")
	view, _ := s.GetTrackTags(auth, ids[63])
	if len(view.Public) != 0 {
		t.Fatal("cancelled item was mutated", view)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.tagBatches.mu.Lock()
		running := s.tagBatches.running[job.ID]
		s.tagBatches.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled worker did not stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A persisted running task without this process's worker must require a
	// fresh preview and confirmation, rather than automatically resume.
	if _, err := s.db.Exec("UPDATE tag_batches SET payload=json_set(payload,'$.state','running') WHERE id=?", job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = s.GetTagBatch(auth, job.ID)
	if err != nil || job.State != "interrupted" {
		t.Fatal(job, err)
	}
	if _, err := s.StartTagBatch(auth, job.ID, job.Version); !errors.Is(err, errVersionConflict) {
		t.Fatal("restart auto-resumed", err)
	}
}

func TestTagBatchPreviewEditPreservesFrozenVersionsAndBounds(t *testing.T) {
	s, auth := batchStore(t)
	id := batchTracks(t, s, 1)[0]
	tag := mustLinkTag(t, s, auth, "personal", "Draft tag")
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartTagBatch(auth, job.ID, job.Version); !errors.Is(err, errTagInvalid) {
		t.Fatal(err)
	}
	if _, err := s.ChangeTrackTags(auth, id, "personal", 1, []string{tag.ID}, nil); err != nil {
		t.Fatal(err)
	}
	job, err = s.EditTagBatch(auth, job.ID, job.Version, nil, []string{tag.ID})
	if err != nil || job.Items[0].Version != 1 {
		t.Fatal("editing replaced frozen version", job, err)
	}
	if _, err := s.StartTagBatch(auth, job.ID, job.Version); !errors.Is(err, errVersionConflict) {
		t.Fatal("stale preview overwrote newer edit", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: []string{id}}); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: []string{id}}); !errors.Is(err, errTagLimit) {
		t.Fatal("unbounded task admission", err)
	}
	if _, err := s.db.Exec("UPDATE tag_batches SET expires_at='2000-01-01T00:00:00Z'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTagBatch(auth, job.ID); !errors.Is(err, errResourceHidden) {
		t.Fatal("expired task visible", err)
	}
	if _, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: []string{id}}); err != nil {
		t.Fatal("expired previews not cleaned", err)
	}
}

func TestTagBatchHistoryIsBoundedWithoutDeletingActivePreview(t *testing.T) {
	s, auth := batchStore(t)
	id := batchTracks(t, s, 1)[0]
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<110)
INSERT INTO tag_batches SELECT printf('00000000-0000-4000-9000-%012d',x),user_id,json_set(payload,'$.id',printf('00000000-0000-4000-9000-%012d',x),'$.state','cancelled','$.finished_at','2000-01-01T00:00:00Z'),created_at,expires_at FROM tag_batches,n WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	active, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.CancelTagBatch(auth, job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	var terminal int
	if err := s.db.QueryRow("SELECT count(*) FROM tag_batches WHERE json_extract(payload,'$.state') NOT IN ('preview','running')").Scan(&terminal); err != nil || terminal != 100 {
		t.Fatal(terminal, err)
	}
	if _, err := s.GetTagBatch(auth, active.ID); err != nil {
		t.Fatal("history retention deleted active preview", err)
	}
	if _, err := s.GetTagBatch(auth, job.ID); err != nil {
		t.Fatal("history retention deleted most recently completed task", err)
	}
	summaries, err := s.ListTagBatches(auth)
	if err != nil || len(summaries) != 20 {
		t.Fatal("summary list is not bounded to twenty", len(summaries), err)
	}
}
