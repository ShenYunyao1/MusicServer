package music

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uploadTagOptions(t *testing.T, s *Store, token string) MutationOptions {
	t.Helper()
	u, session, ok := s.sessionIdentity(token)
	if !ok {
		t.Fatal("session unavailable")
	}
	return MutationOptions{UserID: u.ID, CredentialID: session.ID, ActorType: "session", Actor: u.Username}
}

func TestTagUploadDraftAtomicCommitAndDedupPermissions(t *testing.T) {
	a, adminToken := tagHTTPAPI(t)
	s := a.store
	admin := tagTestAuth(t, s, "admin")
	public := mustLinkTag(t, s, admin, "public", "Upload public")
	_, aliceToken := readyMember(t, a, "tag-upload-alice")
	aliceOptions := uploadTagOptions(t, s, aliceToken)
	alice := tagMemberAuth(t, s, "tag-upload-alice")
	personal := mustLinkTag(t, s, alice, "personal", "Upload personal")
	data := validTestAudio(t)
	preview, err := s.StageAudio("tagged.wav", bytes.NewReader(data), nil, uploadTagOptions(t, s, adminToken))
	if err != nil {
		t.Fatal(err)
	}
	if preview.TagVersions == nil || preview.TagVersions.PublicVersion != 1 || preview.TagVersions.PersonalVersion != 1 {
		t.Fatal(preview)
	}
	var links int
	if err := s.db.QueryRow("SELECT count(*) FROM track_tags").Scan(&links); err != nil || links != 0 {
		t.Fatal(links, err)
	}
	beforeFailedCommit := s.Revision()
	_, _, err = s.commitUploadStageWithTags(preview.ID, nil, &UploadTagEdits{Public: &UploadTagEdit{Version: 1, AddIDs: []string{public.ID}}, Personal: &UploadTagEdit{Version: 9}}, uploadTagOptions(t, s, adminToken))
	if !errors.Is(err, errVersionConflict) {
		t.Fatal("mixed scope conflict accepted", err)
	}
	if s.Revision() != beforeFailedCommit {
		t.Fatal("failed commit changed shared revision")
	}
	if err := s.db.QueryRow("SELECT count(*) FROM track_tags").Scan(&links); err != nil || links != 0 {
		t.Fatal("failed commit left tag links", links, err)
	}
	if files, err := os.ReadDir(filepath.Join(s.mediaRoot, "uploads")); err != nil || len(files) != 0 {
		t.Fatal("failed commit left media copies", files, err)
	}
	track, duplicate, err := s.commitUploadStageWithTags(preview.ID, nil, &UploadTagEdits{Public: &UploadTagEdit{Version: 1, AddIDs: []string{public.ID}}}, uploadTagOptions(t, s, adminToken))
	if err != nil || duplicate {
		t.Fatal(track, duplicate, err)
	}
	view, err := s.GetTrackTags(admin, track.ID)
	if err != nil || len(view.Public) != 1 {
		t.Fatal(view, err)
	}
	second, err := s.StageAudio("duplicate.wav", bytes.NewReader(data), nil, aliceOptions)
	if err != nil {
		t.Fatal(err)
	}
	if second.DuplicateOf != track.ID || second.TagVersions == nil || second.TagVersions.PublicVersion != view.PublicVersion || second.TagVersions.PersonalVersion != 1 {
		t.Fatal(second)
	}
	_, _, err = s.commitUploadStageWithTags(second.ID, nil, &UploadTagEdits{Public: &UploadTagEdit{Version: view.PublicVersion, RemoveIDs: []string{public.ID}}}, aliceOptions)
	if !errors.Is(err, errResourceHidden) {
		t.Fatal("duplicate bypassed public owner", err)
	}
	if _, ok, err := s.UploadStage(second.ID); err != nil || !ok {
		t.Fatal("failed commit consumed draft", ok, err)
	}
	_, _, err = s.commitUploadStageWithTags(second.ID, nil, &UploadTagEdits{Personal: &UploadTagEdit{Version: 9, AddIDs: []string{personal.ID}}}, aliceOptions)
	if !errors.Is(err, errVersionConflict) {
		t.Fatal("stale personal version accepted", err)
	}
	before := s.Revision()
	unwatch := s.watchCommunity()
	defer unwatch()
	signal := s.communitySignal()
	got, duplicate, err := s.commitUploadStageWithTags(second.ID, nil, &UploadTagEdits{Personal: &UploadTagEdit{Version: 1, AddIDs: []string{personal.ID}}}, aliceOptions)
	if err != nil || !duplicate || got.ID != track.ID || got.ContentHash != track.ContentHash {
		t.Fatal(got, duplicate, err)
	}
	if s.Revision() != before {
		t.Fatal("personal duplicate changed global revision")
	}
	select {
	case <-signal:
		t.Fatal("personal duplicate emitted public notification")
	default:
	}
	view, err = s.GetTrackTags(alice, track.ID)
	if err != nil || len(view.Public) != 1 || len(view.Personal) != 1 {
		t.Fatal(view, err)
	}
}

func TestTagUploadSchemaGateAndRevokedSessionPreserveStage(t *testing.T) {
	a, _ := testAPIForSchema(t, 7)
	_, _, token := loginTestAccount(t, a.store, "admin")
	options := uploadTagOptions(t, a.store, token)
	preview, err := a.store.StageAudio("plain.wav", bytes.NewReader(validTestAudio(t)), nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if preview.TagVersions != nil {
		t.Fatal("schema7 advertised tags")
	}
	_, _, err = a.store.commitUploadStageWithTags(preview.ID, nil, &UploadTagEdits{Personal: &UploadTagEdit{Version: 1}}, options)
	if !errors.Is(err, errTagsUnavailable) {
		t.Fatal(err)
	}
	if _, ok, err := a.store.UploadStage(preview.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := initializeSQLiteSchemaForTarget(a.store.db, 8); err != nil {
		t.Fatal(err)
	}
	if err := a.store.RevokeSession(options.UserID, options.CredentialID); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.store.commitUploadStageWithTags(preview.ID, nil, &UploadTagEdits{Personal: &UploadTagEdit{Version: 1}}, options)
	if err == nil {
		t.Fatal("revoked session committed tags")
	}
	if _, ok, err := a.store.UploadStage(preview.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestTagUploadCommitParserStrictAndCompatible(t *testing.T) {
	for _, body := range []string{`{"title":"Title","tag_edits":{"public":{"version":1,"add_ids":[]}}}`, `{"genre":"AC/DC"}`, ``} {
		r := httptest.NewRequest("POST", "http://example.com", strings.NewReader(body))
		if _, _, err := parseUploadCommit(r); err != nil {
			t.Fatal(body, err)
		}
	}
	for _, body := range []string{`null`, `{"unknown":"value"}`, `{} {}`, `{"tag_edits":{}}`, `{"tag_edits":{"public":{"version":0}}}`, `{"tag_edits":{"personal":{"version":1,"user_id":"other"}}}`, `{"title":null}`} {
		r := httptest.NewRequest("POST", "http://example.com", strings.NewReader(body))
		if _, _, err := parseUploadCommit(r); err == nil {
			t.Fatal("accepted invalid payload", body)
		}
	}
}

func TestTagUploadEmailQuotaAndCancellation(t *testing.T) {
	a, adminToken := tagHTTPAPI(t)
	s := a.store
	u, token := readyMember(t, a, "tag-upload-quota")
	options := uploadTagOptions(t, s, token)
	private := mustLinkTag(t, s, tagMemberAuth(t, s, u.Username), "personal", "Quota private")
	edits := &UploadTagEdits{Personal: &UploadTagEdit{Version: 1, AddIDs: []string{private.ID}}}
	p, err := s.StageAudio("quota.wav", bytes.NewReader(validTestAudio(t)), nil, options)
	if err != nil {
		t.Fatal(err)
	}
	setEmailTestRequirement(t, s, u.ID, true)
	if _, _, err := s.commitUploadStageWithTags(p.ID, nil, edits, options); err == nil {
		t.Fatal("tagged upload bypassed email")
	}
	setEmailTestRequirement(t, s, u.ID, false)
	if _, err := s.SetStorageQuota(u.ID, 1, 0, uploadTagOptions(t, s, adminToken)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.commitUploadStageWithTags(p.ID, nil, edits, options); !errors.Is(err, errStorageQuota) {
		t.Fatal("tagged upload bypassed quota", err)
	}
	if _, ok, err := s.UploadStage(p.ID); err != nil || !ok {
		t.Fatal("denied draft consumed", ok, err)
	}
	if err := s.DeleteUploadStage(p.ID); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := s.db.QueryRow("SELECT count(*) FROM track_tags").Scan(&links); err != nil || links != 0 {
		t.Fatal("canceled draft created links", links, err)
	}
}

func TestTagUploadCommitHTTPGatesAndConflict(t *testing.T) {
	a, token := tagHTTPAPI(t)
	s := a.store
	personal := mustLinkTag(t, s, tagTestAuth(t, s, "admin"), "personal", "HTTP personal")
	p, err := s.StageAudio("http.wav", bytes.NewReader(validTestAudio(t)), nil, uploadTagOptions(t, s, token))
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/uploads/stage/" + p.ID + "/commit"
	w := mailRequest(a, token, "POST", path, fmt.Sprintf(`{"tag_edits":{"personal":{"version":9,"add_ids":[%q]}}}`, personal.ID), 0)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	w = mailRequest(a, token, "POST", path, fmt.Sprintf(`{"title":"Tagged HTTP","tag_edits":{"personal":{"version":1,"add_ids":[%q]}}}`, personal.ID), 0)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Tagged HTTP") {
		t.Fatal(w.Code, w.Body)
	}
}
