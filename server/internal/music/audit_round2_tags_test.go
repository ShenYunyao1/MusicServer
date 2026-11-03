package music

import (
	"testing"
)

func TestRound2MergedHistoryDoesNotConsumeActiveDictionarySlots(t *testing.T) {
	s, auth := tagTestStore(t)
	source := mustLinkTag(t, s, auth, "public", "History original")
	target := mustLinkTag(t, s, auth, "public", "History target")
	if _, err := s.MergeTags(auth, source.ID, target.ID, source.Version, target.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO tags(id,scope_id,group_id,name,normalized_name,version,deleted,created_at,updated_at) SELECT printf('00000000-0000-4000-8000-%012d',x),'public','custom','live-'||x,'live-'||x,1,0,'created','updated' FROM n`, publicTagLimit-2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(auth, TagInput{Scope: "public", GroupID: "custom", Name: "Remaining live slot"}); err != nil {
		t.Fatal("merged source consumed active capacity", err)
	}
	if current, err := s.GetTag(auth, source.ID); err != nil || current.MergedInto != target.ID {
		t.Fatal("redirect history lost", current, err)
	}
}
