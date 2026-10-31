package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"musicserver/internal/activity"

	"github.com/google/uuid"
)

func TestAuditTagBatchEmptyHasDistinctError(t *testing.T) {
	s, auth := batchStore(t)
	tag := mustLinkTag(t, s, auth, "personal", "Empty preview")
	_, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", Query: &TagBatchQuery{Search: "no matching track", Filter: "all"}, AddIDs: []string{tag.ID}})
	if err == nil || errors.Is(err, errTagLimit) {
		t.Fatalf("empty preview must have a distinct error: %v", err)
	}
}

func TestAuditTagBatchProtectsDictionaryReferences(t *testing.T) {
	for _, action := range []string{"delete", "merge"} {
		t.Run(action, func(t *testing.T) {
			s, auth := batchStore(t)
			tag := mustLinkTag(t, s, auth, "personal", "Pending reference")
			target := mustLinkTag(t, s, auth, "personal", "Merge target")
			job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: []string{s.Tracks()[0].ID}, AddIDs: []string{tag.ID}})
			if err != nil {
				t.Fatal(err)
			}
			if action == "delete" {
				err = s.DeleteTag(auth, tag.ID, tag.Version)
			} else {
				_, err = s.MergeTags(auth, tag.ID, target.ID, tag.Version, target.Version)
			}
			if !errors.Is(err, errTagInUse) {
				t.Fatalf("pending task reference was changed: %v", err)
			}
			job, err = s.StartTagBatch(auth, job.ID, job.Version)
			if err != nil || job.State != "completed" {
				t.Fatal(job, err)
			}
		})
	}
}

func TestAuditTagTombstonesCountTowardsStorageLimit(t *testing.T) {
	s, auth := batchStore(t)
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		if err := ensureTagScope(tx, auth.userID); err != nil {
			return err
		}
		for index := 0; index < personalTagLimit; index++ {
			name := fmt.Sprintf("Deleted %d", index)
			if _, err := tx.Exec("INSERT INTO tags VALUES(?,?,?,?,?,1,1,NULL,?,?)", uuid.NewString(), auth.userID, "genre", name, name, "created", "updated"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(auth, TagInput{Scope: "personal", GroupID: "genre", Name: "Overflow"}); !errors.Is(err, errTagLimit) {
		t.Fatalf("tombstone quota bypass: %v", err)
	}
}

func TestAuditTagWorkerRetainsFailureAfterStateWriteFails(t *testing.T) {
	s, auth := batchStore(t)
	journal, err := activity.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	s.activity = journal
	ids := batchTracks(t, s, tagBatchSize+1)
	tag := mustLinkTag(t, s, auth, "personal", "Worker failure")
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: ids, AddIDs: []string{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	job.State = "running"
	raw, _ := json.Marshal(job)
	if _, err := s.db.Exec("UPDATE tag_batches SET payload=? WHERE id=?", string(raw), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER reject_batch_failure BEFORE UPDATE ON tag_batches BEGIN SELECT RAISE(ABORT,'state unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	s.tagBatches.running = map[string]bool{job.ID: true}
	s.workers.Add(1)
	s.runTagBatch(auth, job.ID)
	logged := journal.Query(activity.Query{Limit: 20})
	if len(logged.Entries) != 1 || logged.Entries[0].Event != "tag.batch_failed" || logged.Entries[0].Reason != "TAG_FAILED" {
		t.Fatal("worker failure not journaled", logged)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_batch_failure"); err != nil {
		t.Fatal(err)
	}
	restored, err := s.GetTagBatch(auth, job.ID)
	if err != nil || restored.State != "failed" || restored.Error != "TAG_FAILED" {
		t.Fatalf("worker failure was lost: %+v %v", restored, err)
	}
	var changed int
	if err := s.db.QueryRow("SELECT count(*) FROM track_tags WHERE tag_id=?", tag.ID).Scan(&changed); err != nil || changed != 0 {
		t.Fatal("failed chunk leaked links", changed, err)
	}
}

func TestAuditTagBatchEmptyHTTPCode(t *testing.T) {
	a, token := tagHTTPAPI(t)
	w := mailRequest(a, token, "POST", "/api/v2/tag-batches", `{"scope":"personal","query":{"q":"not present anywhere","filter":"all"}}`, 0)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "TAG_BATCH_EMPTY") {
		t.Fatal(w.Code, w.Body)
	}
}

func TestAuditUploadTagVersionFailureVisible(t *testing.T) {
	s, auth := batchStore(t)
	track := s.Tracks()[0]
	preview := UploadPreview{DuplicateOf: track.ID}
	if err := s.uploadPreviewTags(&preview, stagedRecord{Track: Track{OwnerID: auth.userID}}); err != nil || preview.TagVersions == nil {
		t.Fatal("valid preview", preview, err)
	}
	if _, err := s.db.Exec("DROP TABLE track_tag_states"); err != nil {
		t.Fatal(err)
	}
	preview.TagVersions = nil
	if err := s.uploadPreviewTags(&preview, stagedRecord{Track: Track{OwnerID: auth.userID}}); err == nil {
		t.Fatal("tag version read failure was silently suppressed")
	}
}

func TestAuditTagBatchLegacyInvalidReferenceConflicts(t *testing.T) {
	s, auth := batchStore(t)
	tag := mustLinkTag(t, s, auth, "personal", "Legacy batch reference")
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: []string{s.Tracks()[0].ID}, AddIDs: []string{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE tags SET deleted=1 WHERE id=?", tag.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartTagBatch(auth, job.ID, job.Version); !errors.Is(err, errVersionConflict) {
		t.Fatalf("invalidated preview should conflict, not hide: %v", err)
	}
}
