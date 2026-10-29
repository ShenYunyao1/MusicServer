package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTrackTagLinksIsolatePersonalAndKeepPublicTrackIdentity(t *testing.T) {
	s, auth := tagTestStore(t)
	track := s.Tracks()[0]
	personal, err := s.CreateTag(auth, TagInput{Scope: "personal", GroupID: "genre", Name: "private-only-marker"})
	if err != nil {
		t.Fatal(err)
	}
	revision := s.Revision()
	unwatch := s.watchCommunity()
	defer unwatch()
	signal := s.communitySignal()
	view, err := s.ChangeTrackTags(auth, track.ID, "personal", 1, []string{personal.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Personal) != 1 || len(view.Public) != 0 || view.PersonalVersion != 2 || view.PublicVersion != 1 {
		t.Fatalf("view=%+v", view)
	}
	current, _ := s.Track(track.ID)
	if current.Version != track.Version || current.Path != track.Path || current.Genre != track.Genre || current.ContentHash != track.ContentHash || s.Revision() != revision {
		t.Fatal("private tags changed the shared track")
	}
	select {
	case <-signal:
		t.Fatal("personal tags notified the public change stream")
	default:
	}
	view, err = s.ChangeTrackTags(auth, track.ID, "personal", 2, []string{personal.ID}, nil)
	if err != nil || view.PersonalVersion != 2 {
		t.Fatal("no-op changed version", view, err)
	}
	if _, err = s.ChangeTrackTags(auth, track.ID, "personal", 1, nil, []string{personal.ID}); !errors.Is(err, errVersionConflict) {
		t.Fatal("stale mutation accepted", err)
	}
}

func tagLinkFixture(t *testing.T) (*API, authContext, User, authContext, User, authContext) {
	t.Helper()
	api, _ := testAPI(t)
	if err := initializeSQLiteSchemaForTarget(api.store.db, 8); err != nil {
		t.Fatal(err)
	}
	admin := tagTestAuth(t, api.store, "admin")
	alice, _ := readyMember(t, api, "tag-alice")
	bob, _ := readyMember(t, api, "tag-bob")
	return api, admin, alice, tagMemberAuth(t, api.store, alice.Username), bob, tagMemberAuth(t, api.store, bob.Username)
}

func tagMemberAuth(t *testing.T, s *Store, username string) authContext {
	t.Helper()
	u, session, _, err := s.Login(username, "personal password for "+username, "tag-test")
	if err != nil {
		t.Fatal(err)
	}
	return authContext{session: true, userID: u.ID, keyID: session.ID, authVersion: u.AuthVersion, role: u.Role}
}

func mustLinkTag(t *testing.T, s *Store, auth authContext, scope, name string) Tag {
	t.Helper()
	tag, err := s.CreateTag(auth, TagInput{Scope: scope, GroupID: "genre", Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return tag
}

func tagAuditCount(t *testing.T, s *Store) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM audit").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestTrackTagLinksAuthorizeBeforeVersionsAndDoNotLeakPersonal(t *testing.T) {
	api, admin, alice, aliceAuth, _, bobAuth := tagLinkFixture(t)
	s := api.store
	track := s.Tracks()[0]
	public := mustLinkTag(t, s, admin, "public", "public marker")
	private := mustLinkTag(t, s, aliceAuth, "personal", "alice secret tag")
	if _, err := s.ChangeTrackTags(bobAuth, track.ID, "public", 999, []string{public.ID}, nil); !errors.Is(err, errVersionConflict) {
		t.Fatal("public contribution must still check version", err)
	}
	if _, err := s.ChangeTrackTags(bobAuth, track.ID, "personal", 999, []string{private.ID}, nil); !errors.Is(err, errResourceHidden) {
		t.Fatal("personal scope leaked behind version", err)
	}
	before := s.Revision()
	audit := tagAuditCount(t, s)
	if _, err := s.ChangeTrackTags(aliceAuth, track.ID, "personal", 1, []string{private.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if s.Revision() != before {
		t.Fatal("personal mutation bumped global sync")
	}
	view, err := s.GetTrackTags(bobAuth, track.ID)
	if err != nil || len(view.Personal) != 0 {
		t.Fatal("another user's tags leaked", view, err)
	}
	view, err = s.GetTrackTags(admin, track.ID)
	if err != nil || len(view.Personal) != 0 {
		t.Fatal("admin saw user's private tags", view, err)
	}
	var raw string
	if err = s.db.QueryRow("SELECT changes FROM audit WHERE id=(SELECT max(id) FROM audit)").Scan(&raw); err != nil || strings.Contains(raw, "alice secret tag") {
		t.Fatal("private spelling leaked in audit", raw, err)
	}
	if tagAuditCount(t, s) != audit+1 {
		t.Fatal("private mutation audit missing")
	}
	page, err := s.SyncPage(bobAuth, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "alice secret tag") {
		t.Fatal("private tags leaked in shared sync")
	}
	if err = s.transaction(func(tx *sql.Tx) error { track.OwnerID = alice.ID; return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeTrackTags(aliceAuth, track.ID, "public", 1, []string{public.ID}, nil); err != nil {
		t.Fatal("track owner cannot bind public dictionary", err)
	}
	view, err = s.GetTrackTags(bobAuth, track.ID)
	if err != nil || len(view.Public) != 1 || len(view.Personal) != 0 {
		t.Fatal(view, err)
	}
}

func TestTrackTagLinksPublicOverridesNoOpAndValidation(t *testing.T) {
	s, auth := tagTestStore(t)
	track := s.Tracks()[0]
	tag := mustLinkTag(t, s, auth, "public", "hand-confirmed")
	before := s.Revision()
	oldAudit := tagAuditCount(t, s)
	unwatch := s.watchCommunity()
	defer unwatch()
	signal := s.communitySignal()
	view, err := s.ChangeTrackTags(auth, track.ID, "public", 1, []string{tag.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := s.Track(track.ID)
	if current.Version != track.Version+1 || s.Revision() != before+1 || view.PublicVersion != 2 {
		t.Fatal("public versions not advanced")
	}
	select {
	case <-signal:
	default:
		t.Fatal("public tag change did not notify sync readers")
	}
	signal = s.communitySignal()
	var source, action string
	s.db.QueryRow("SELECT source FROM track_tags WHERE track_id=? AND tag_id=?", track.ID, tag.ID).Scan(&source)
	s.db.QueryRow("SELECT action FROM track_tag_overrides WHERE track_id=? AND tag_id=?", track.ID, tag.ID).Scan(&action)
	if source != "manual" || action != "confirmed" {
		t.Fatal(source, action)
	}
	if _, err = s.ChangeTrackTags(auth, track.ID, "public", 2, []string{tag.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if s.Revision() != before+1 || tagAuditCount(t, s) != oldAudit+1 {
		t.Fatal("no-op changed sync or audit")
	}
	select {
	case <-signal:
		t.Fatal("no-op notified sync readers")
	default:
	}
	if _, err = s.ChangeTrackTags(auth, track.ID, "public", 2, []string{tag.ID, tag.ID}, nil); !errors.Is(err, errTagInvalid) {
		t.Fatal("duplicate input accepted", err)
	}
	if _, err = s.ChangeTrackTags(auth, track.ID, "public", 2, []string{tag.ID}, []string{tag.ID}); !errors.Is(err, errTagInvalid) {
		t.Fatal("conflicting input accepted", err)
	}
	view, err = s.ChangeTrackTags(auth, track.ID, "public", 2, nil, []string{tag.ID})
	if err != nil || len(view.Public) != 0 || view.PublicVersion != 3 {
		t.Fatal(view, err)
	}
	s.db.QueryRow("SELECT action FROM track_tag_overrides WHERE track_id=? AND tag_id=?", track.ID, tag.ID).Scan(&action)
	if action != "suppressed" {
		t.Fatal("manual removal lost scanner suppression")
	}
	before = s.Revision()
	oldAudit = tagAuditCount(t, s)
	if _, err = s.ChangeTrackTags(auth, track.ID, "public", 3, nil, []string{tag.ID}); err != nil {
		t.Fatal(err)
	}
	if s.Revision() != before || tagAuditCount(t, s) != oldAudit {
		t.Fatal("repeated suppression was not a no-op")
	}
	track.Missing = true
	if err = s.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeTrackTags(auth, track.ID, "public", 3, []string{tag.ID}, nil); err != nil {
		t.Fatal("missing track cannot be organized", err)
	}
	track.Removed = true
	if err = s.transaction(func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetTrackTags(auth, track.ID); !errors.Is(err, errResourceHidden) {
		t.Fatal("removed track exposed", err)
	}
	if _, err = s.ChangeTrackTags(authContext{}, track.ID, "public", 999, nil, nil); !errors.Is(err, errTagSession) {
		t.Fatal("session validation was not first", err)
	}
}

func TestTrackTagLinksEnforcePerScopeLimitAtomically(t *testing.T) {
	s, auth := tagTestStore(t)
	track := s.Tracks()[0]
	ids := []string{}
	for i := 0; i < 33; i++ {
		ids = append(ids, mustLinkTag(t, s, auth, "personal", fmt.Sprintf("personal-limit-%02d", i)).ID)
	}
	if _, err := s.ChangeTrackTags(auth, track.ID, "personal", 1, ids, nil); !errors.Is(err, errTagLimit) {
		t.Fatal("oversized request accepted", err)
	}
	if _, err := s.ChangeTrackTags(auth, track.ID, "personal", 1, ids[:32], nil); err != nil {
		t.Fatal(err)
	}
	before := tagAuditCount(t, s)
	if _, err := s.ChangeTrackTags(auth, track.ID, "personal", 2, ids[32:], nil); !errors.Is(err, errTagLimit) {
		t.Fatal("track limit exceeded", err)
	}
	view, err := s.GetTrackTags(auth, track.ID)
	if err != nil || len(view.Personal) != 32 || view.PersonalVersion != 2 || tagAuditCount(t, s) != before {
		t.Fatal("failed limit mutation was not atomic", view, err)
	}
}
