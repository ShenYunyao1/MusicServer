package music

import (
	"fmt"
	"strings"
	"testing"
)

func TestTagImportHTTPAndAccountTaskCleanup(t *testing.T) {
	a, adminToken := tagHTTPAPI(t)
	s := a.store
	admin, _, _ := s.sessionIdentity(adminToken)
	user, token := readyMember(t, a, "task-cleanup")
	auth := tagMemberAuth(t, s, user.Username)
	track := s.Tracks()[0]
	runGenreImport(t, s, track.ID, []string{"Unknown genre"})
	w := mailRequest(a, token, "GET", "/api/v2/tracks/"+track.ID+"/tag-import-source", "", 0)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Unknown genre") {
		t.Fatal(w.Code, w.Body)
	}
	personal := mustLinkTag(t, s, auth, "personal", "Private task label")
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", TrackIDs: []string{track.ID}, AddIDs: []string{personal.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserEnabled(user.ID, false, user.Version); err != nil {
		t.Fatal(err)
	}
	user, _ = readUser(s.db, user.ID)
	if err := s.DeleteUser(user.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: user.Version}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM tag_batches WHERE id=?", job.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted account task survived", count, err)
	}
	source, err := s.GetTrackTagImport(tagTestAuth(t, s, "admin"), track.ID)
	if err != nil || len(source.Unmapped) != 1 {
		t.Fatal("deleted account erased public source", source, err)
	}
	w = mailRequest(a, adminToken, "GET", fmt.Sprintf("/api/v2/tracks/%s/tag-import-source?user_id=other", track.ID), "", 0)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
}
