package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuditDictionaryCompactionPreservesMergedRedirect(t *testing.T) {
	s, auth := tagTestStore(t)
	source := mustLinkTag(t, s, auth, "public", "Redirect source")
	target := mustLinkTag(t, s, auth, "public", "Redirect target")
	if _, err := s.MergeTags(auth, source.ID, target.ID, source.Version, target.Version); err != nil {
		t.Fatal(err)
	}
	if err := s.tagTransaction(false, func(tx *sql.Tx) error { return compactTagTombstones(tx, "public") }); err != nil {
		t.Fatal(err)
	}
	actor, err := tagActor(s.db, auth)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := resolveFilterTag(s.db, actor, source.ID, "public", "genre"); err != nil || id != target.ID {
		t.Fatal("compaction lost redirect", id, err)
	}
	if _, err := resolveFilterTag(s.db, actor, uuid.NewString(), "public", "genre"); !errors.Is(err, errFilterReference) {
		t.Fatal("missing reference has wrong error", err)
	}
}

func TestAuditContributionRetryMatchesDailyWindow(t *testing.T) {
	a, _, _, member, _, _ := tagLinkFixture(t)
	s := a.store
	actor, err := tagActor(s.db, member)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-2 * time.Hour)
	raw, _ := json.Marshal(map[string]any{"after": map[string]string{"scope": "public"}})
	err = s.transaction(func(tx *sql.Tx) error {
		for i := 0; i < 100; i++ {
			if _, e := tx.Exec("INSERT INTO audit(actor,action,entity_id,changes,created_at,actor_type,user_id) VALUES('test','tag.create','test',?,?,'session',?)", string(raw), persistedTime(at), actor.ID); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.tagTransaction(false, func(tx *sql.Tx) error { return checkPublicContributionBudget(tx, actor) })
	if !errors.Is(err, errTagContributionBudget) {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	writeTagError(w, err)
	seconds, e := strconv.Atoi(w.Header().Get("Retry-After"))
	if e != nil || seconds < 21*3600 || seconds > 22*3600+1 {
		t.Fatal("daily retry is inaccurate", w.Header(), w.Body)
	}
}

func TestAuditDictionaryMaintenanceDoesNotNeedContributionBudget(t *testing.T) {
	s, _ := tagTestStore(t)
	if _, err := s.db.Exec(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO tags(id,scope_id,group_id,name,normalized_name,version,deleted,created_at,updated_at) SELECT printf('00000000-0000-4000-8000-%012d',x),'public','custom','old-'||x,'old-'||x,1,1,'created','updated' FROM n`, publicTagLimit); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupTagDictionary(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM tags WHERE scope_id='public'").Scan(&count); err != nil || count >= publicTagLimit {
		t.Fatal("maintenance required create admission", count, err)
	}
}
