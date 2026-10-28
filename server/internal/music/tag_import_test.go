package music

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func runGenreImport(t *testing.T, s *Store, id string, values []string) bool {
	t.Helper()
	changed := false
	if err := s.tagTransaction(false, func(tx *sql.Tx) error {
		var err error
		changed, err = syncImportedGenreTags(tx, id, values)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return changed
}

func TestTrackTagImportFullScanAndGenreOverrideKeepIdentity(t *testing.T) {
	a, _ := tagHTTPAPI(t)
	s := a.store
	auth := tagTestAuth(t, s, "admin")
	track := s.Tracks()[0]
	path := filepath.Join(s.mediaRoot, track.Path)
	audio := testID3AudioMetadata()
	if err := os.WriteFile(path, audio, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runScan(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	source, err := s.GetTrackTagImport(auth, track.ID)
	if err != nil || !reflect.DeepEqual(source.Unmapped, []string{"流行"}) {
		t.Fatal(source, err)
	}
	pop := mustLinkTag(t, s, auth, "public", "流行")
	rock := mustLinkTag(t, s, auth, "public", "Rock")
	if _, err := s.runScan(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	view, _ := s.GetTrackTags(auth, track.ID)
	if len(view.Public) != 1 || view.Public[0].ID != pop.ID {
		t.Fatal(view)
	}
	if _, err := s.ChangeTrackTags(auth, track.ID, "public", view.PublicVersion, nil, []string{pop.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runScan(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	view, _ = s.GetTrackTags(auth, track.ID)
	if len(view.Public) != 0 {
		t.Fatal("rescan resurrected suppression", view)
	}
	if err := s.UpdateTrack(track.ID, map[string]string{"genre": "Rock"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runScan(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	view, _ = s.GetTrackTags(auth, track.ID)
	if len(view.Public) != 1 || view.Public[0].ID != rock.ID {
		t.Fatal(view)
	}
	current, _ := s.Track(track.ID)
	if current.ID != track.ID || current.Genre != "Rock" || current.Path != track.Path || current.UploadedBy != track.UploadedBy {
		t.Fatal(current)
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(after, audio) {
		t.Fatal("tag edits rewrote audio", err)
	}
}

func TestTrackTagImportScanFailureRollsBackTrackAndSources(t *testing.T) {
	a, _ := tagHTTPAPI(t)
	s := a.store
	auth := tagTestAuth(t, s, "admin")
	track := s.Tracks()[0]
	_ = mustLinkTag(t, s, auth, "public", "流行")
	if err := os.WriteFile(filepath.Join(s.mediaRoot, track.Path), testID3AudioMetadata(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER reject_import BEFORE INSERT ON track_tag_imports BEGIN SELECT RAISE(ABORT,'import failure'); END"); err != nil {
		t.Fatal(err)
	}
	before := tagDataSnapshot(t, s.db)
	revision := s.Revision()
	if _, err := s.runScan(context.Background(), true, nil); err == nil {
		t.Fatal("injected failure ignored")
	}
	after, _ := s.Track(track.ID)
	if after != track || s.Revision() != revision || !reflect.DeepEqual(before, tagDataSnapshot(t, s.db)) {
		t.Fatal("partial scan committed")
	}
}

func TestTrackTagImportDoesNotDeleteOtherImportedGroups(t *testing.T) {
	s, auth := tagTestStore(t)
	track := s.Tracks()[0]
	other, err := s.CreateTag(auth, TagInput{Scope: "public", GroupID: "language", Name: "Instrumental"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO track_tags VALUES(?,?,'public','imported',?,'created')", track.ID, other.ID, auth.userID); err != nil {
		t.Fatal(err)
	}
	runGenreImport(t, s, track.ID, []string{})
	view, err := s.GetTrackTags(auth, track.ID)
	if err != nil || len(view.Public) != 1 || view.Public[0].ID != other.ID {
		t.Fatal("genre import removed unrelated group", view, err)
	}
}

func TestTrackTagImportMapsAliasesWithoutCreatingVocabulary(t *testing.T) {
	s, auth := tagTestStore(t)
	track := s.Tracks()[0]
	tag, err := s.CreateTag(auth, TagInput{Scope: "public", GroupID: "genre", Name: "Rock", Aliases: []string{"摇滚"}})
	if err != nil {
		t.Fatal(err)
	}
	before := s.Revision()
	if !runGenreImport(t, s, track.ID, []string{"摇滚", "Unclassified", "AC/DC"}) {
		t.Fatal("import not changed")
	}
	view, err := s.GetTrackTags(auth, track.ID)
	if err != nil || len(view.Public) != 1 || view.Public[0].ID != tag.ID {
		t.Fatal(view, err)
	}
	source, err := s.GetTrackTagImport(auth, track.ID)
	if err != nil || !reflect.DeepEqual(source.Unmapped, []string{"Unclassified", "AC/DC"}) {
		t.Fatal(source, err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM tags").Scan(&count); err != nil || count != 1 {
		t.Fatal("automatic vocabulary creation", count, err)
	}
	current, _ := s.Track(track.ID)
	if current != track || s.Revision() != before {
		t.Fatal("import helper rewrote shared track instead of letting caller combine changes")
	}
	if runGenreImport(t, s, track.ID, []string{"摇滚", "Unclassified", "AC/DC"}) {
		t.Fatal("same import not idempotent")
	}
}

func TestTrackTagImportPreservesManualOverridesAndPrivateData(t *testing.T) {
	s, auth := tagTestStore(t)
	track := s.Tracks()[0]
	a := mustLinkTag(t, s, auth, "public", "Rock")
	b := mustLinkTag(t, s, auth, "public", "Pop")
	p := mustLinkTag(t, s, auth, "personal", "My music")
	if _, err := s.ChangeTrackTags(auth, track.ID, "personal", 1, []string{p.ID}, nil); err != nil {
		t.Fatal(err)
	}
	runGenreImport(t, s, track.ID, []string{"Rock", "Pop"})
	v, _ := s.GetTrackTags(auth, track.ID)
	if _, err := s.ChangeTrackTags(auth, track.ID, "public", v.PublicVersion, []string{a.ID}, []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	runGenreImport(t, s, track.ID, []string{})
	runGenreImport(t, s, track.ID, []string{"Pop"})
	v, err := s.GetTrackTags(auth, track.ID)
	if err != nil || len(v.Public) != 1 || v.Public[0].ID != a.ID || len(v.Personal) != 1 || v.Personal[0].ID != p.ID {
		t.Fatal(v, err)
	}
	if runGenreImport(t, s, track.ID, nil) {
		t.Fatal("unreadable metadata changed state")
	}
	source, err := s.GetTrackTagImport(auth, track.ID)
	if err != nil || !reflect.DeepEqual(source.Values, []string{"Pop"}) {
		t.Fatal(source, err)
	}
}

func TestTrackTagImportLimitsAndTransactionRollback(t *testing.T) {
	s, auth := tagTestStore(t)
	track := s.Tracks()[0]
	tag := mustLinkTag(t, s, auth, "public", "Known")
	before := tagDataSnapshot(t, s.db)
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		if _, err := syncImportedGenreTags(tx, track.ID, []string{"Known"}); err != nil {
			return err
		}
		return errVersionConflict
	})
	if err != errVersionConflict || !reflect.DeepEqual(before, tagDataSnapshot(t, s.db)) {
		t.Fatal("import escaped transaction", err)
	}
	values := make([]string, 65)
	for i := range values {
		values[i] = "Known"
	}
	runGenreImport(t, s, track.ID, values)
	source, err := s.GetTrackTagImport(auth, track.ID)
	if err != nil || !source.Truncated || len(source.Values) > 64 {
		t.Fatal(source, err)
	}
	v, _ := s.GetTrackTags(auth, track.ID)
	if len(v.Public) != 1 || v.Public[0].ID != tag.ID {
		t.Fatal(v)
	}
}
