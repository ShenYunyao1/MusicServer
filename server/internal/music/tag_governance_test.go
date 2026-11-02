package music

import (
	"errors"
	"fmt"
	"testing"
)

func TestPublicTagContributionBudgetAndAdminRecovery(t *testing.T) {
	a, admin, _, member, _, _ := tagLinkFixture(t)
	for i := 0; i < 20; i++ {
		if _, err := a.store.CreateTag(member, TagInput{Scope: "public", GroupID: "language", Name: fmt.Sprintf("member-contribution-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.store.CreateTag(member, TagInput{Scope: "public", GroupID: "language", Name: "member-over-budget"}); !errors.Is(err, errTagLimit) {
		t.Fatal("member contribution budget bypassed", err)
	}
	if _, err := a.store.CreateTag(admin, TagInput{Scope: "public", GroupID: "language", Name: "administrator-unblocked"}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicPreviewCannotVetoDictionaryMaintenance(t *testing.T) {
	s, auth := batchStore(t)
	tag := mustLinkTag(t, s, auth, "public", "Preview no veto")
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "public", TrackIDs: []string{s.Tracks()[0].ID}, AddIDs: []string{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE tag_batches SET payload=json_set(payload,'$.state','running') WHERE id=?", job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTag(auth, tag.ID, tag.Version); !errors.Is(err, errTagInUse) {
		t.Fatal("running batch lost protection", err)
	}
	if _, err := s.db.Exec("UPDATE tag_batches SET payload=json_set(payload,'$.state','preview') WHERE id=?", job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTag(auth, tag.ID, tag.Version); err != nil {
		t.Fatal("preview vetoed public dictionary", err)
	}
	if _, err := s.StartTagBatch(auth, job.ID, job.Version); err == nil {
		t.Fatal("deleted preview tag executed")
	}
}

func TestPublicTagStorageRecoversUnusedTombstones(t *testing.T) {
	s, auth := batchStore(t)
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM tags WHERE scope_id='public'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO tags(id,scope_id,group_id,name,normalized_name,version,deleted,created_at,updated_at) SELECT printf('00000000-0000-4000-8000-%012d',x),'public','custom','old-'||x,'old-'||x,1,1,'created','updated' FROM n`, publicTagLimit-count); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(auth, TagInput{Scope: "public", GroupID: "custom", Name: "Recovered space"}); err != nil {
		t.Fatal("public tombstones permanently exhausted dictionary", err)
	}
}
