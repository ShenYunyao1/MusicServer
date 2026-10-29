package music

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func tagTestAuth(t *testing.T, s *Store, username string) authContext {
	t.Helper()
	u, session, _ := loginTestAccount(t, s, username)
	return authContext{session: true, userID: u.ID, keyID: session.ID, authVersion: u.AuthVersion, role: u.Role}
}

func tagTestStore(t *testing.T) (*Store, authContext) {
	t.Helper()
	s, _, _ := seedStore(t)
	t.Cleanup(func() { closeStoreForTest(t, s) })
	assertMigrationSchema(t, s.db, 8)
	return s, tagTestAuth(t, s, "admin")
}

func TestTagDictionaryUnicodeNamespaceAndCRUD(t *testing.T) {
	s, auth := tagTestStore(t)
	groups, err := s.ListTagGroups(auth)
	if err != nil || len(groups) != 6 {
		t.Fatalf("groups=%v error=%v", groups, err)
	}
	beforeRevision := s.Revision()
	tag, err := s.CreateTag(auth, TagInput{GroupID: "genre", Name: "  CAFÉ  Rock ", Aliases: []string{"Alt  Rock", "摇滚"}})
	if err != nil {
		t.Fatal(err)
	}
	if tag.Name != "CAFÉ Rock" || len(tag.Aliases) != 2 || tag.Version != 1 {
		t.Fatalf("tag=%+v", tag)
	}
	if _, err := s.CreateTag(auth, TagInput{GroupID: "genre", Name: "cafe\u0301 rock"}); !errors.Is(err, errTagNameTaken) {
		t.Fatal("NFC collision accepted", err)
	}
	if _, err := s.CreateTag(auth, TagInput{GroupID: "genre", Name: "alt rock"}); !errors.Is(err, errTagNameTaken) {
		t.Fatal("alias collision accepted", err)
	}
	if _, err := s.CreateTag(auth, TagInput{Scope: "personal", GroupID: "genre", Name: "alt rock"}); err != nil {
		t.Fatal("cross scope refused", err)
	}
	if _, err := s.CreateTag(auth, TagInput{GroupID: "mood", Name: "alt rock"}); err != nil {
		t.Fatal("cross group refused", err)
	}
	name := "New Name"
	if _, err := s.UpdateTag(auth, tag.ID, TagPatch{Name: &name}, 9); !errors.Is(err, errVersionConflict) {
		t.Fatal("stale version accepted", err)
	}
	updated, err := s.UpdateTag(auth, tag.ID, TagPatch{Name: &name}, tag.Version)
	if err != nil || updated.Version != 2 {
		t.Fatal(updated, err)
	}
	page, err := s.ListTags(auth, TagQuery{GroupID: "genre", Query: "摇滚"})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != tag.ID {
		t.Fatal(page, err)
	}
	if err := s.DeleteTag(auth, tag.ID, updated.Version); err != nil {
		t.Fatal(err)
	}
	tombstone, err := s.GetTag(auth, tag.ID)
	if err != nil || !tombstone.Deleted {
		t.Fatal(tombstone, err)
	}
	if _, err := s.CreateTag(auth, TagInput{GroupID: "genre", Name: "New Name"}); err != nil {
		t.Fatal("deleted namespace not released", err)
	}
	if s.Revision() != beforeRevision {
		t.Fatal("dictionary modified the global track revision")
	}
}

func TestTagDictionaryRejectsInvalidNamesAndLegacySchema(t *testing.T) {
	for _, name := range []string{"", "   ", "line\nbreak", "tab\tname", string([]byte{0xff}), "zero\u200bwidth"} {
		if _, _, err := normalizeTagName(name); !errors.Is(err, errTagInvalid) {
			t.Fatalf("accepted %q: %v", name, err)
		}
	}
	s, _, _ := seedStoreForSchema(t, 7)
	t.Cleanup(func() { closeStoreForTest(t, s) })
	if _, err := s.ListTagGroups(tagTestAuth(t, s, "admin")); !errors.Is(err, errTagsUnavailable) {
		t.Fatal("schema 7 tags available", err)
	}
}

func TestTagDictionaryPrivateIsolationSessionsAndAudit(t *testing.T) {
	s, admin := tagTestStore(t)
	member, err := s.createUser("tag-member", "test administrator password", "member", false)
	if err != nil {
		t.Fatal(err)
	}
	own := tagTestAuth(t, s, member.Username)
	other, err := s.createUser("tag-other", "test administrator password", "member", false)
	if err != nil {
		t.Fatal(err)
	}
	otherAuth := tagTestAuth(t, s, other.Username)
	if member.EmailVerified {
		t.Fatal("fixture unexpectedly verified email")
	}
	stop := s.watchCommunity()
	defer stop()
	signal := s.communitySignal()
	before := s.Revision()
	private, err := s.CreateTag(own, TagInput{Scope: "personal", GroupID: "custom", Name: "Rare private spelling", Aliases: []string{"Secret alias spelling"}})
	if err != nil {
		t.Fatal("email policy blocked dictionary", err)
	}
	select {
	case <-signal:
		t.Fatal("private dictionary notified the community")
	default:
	}
	if s.Revision() != before {
		t.Fatal("private tag changed global revision")
	}
	for _, auth := range []authContext{admin, otherAuth} {
		if tag, err := s.GetTag(auth, private.ID); !errors.Is(err, errResourceHidden) || tag.ID != "" {
			t.Fatal("private tag leaked", tag, err)
		}
		name := "hijacked"
		if _, err := s.UpdateTag(auth, private.ID, TagPatch{Name: &name}, private.Version); !errors.Is(err, errResourceHidden) {
			t.Fatal("private tag writable", err)
		}
		if err := s.DeleteTag(auth, private.ID, private.Version); !errors.Is(err, errResourceHidden) {
			t.Fatal("private tag deletable", err)
		}
		page, err := s.ListTags(auth, TagQuery{Scope: "personal"})
		if err != nil || page.Total != 0 {
			t.Fatal("private dictionary leaked", page, err)
		}
	}
	public, err := s.CreateTag(admin, TagInput{GroupID: "custom", Name: "Public spelling"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(own, TagInput{GroupID: "custom", Name: "Community public term"}); err != nil {
		t.Fatal("member cannot create public dictionary term", err)
	}
	name := "Rename public"
	if _, err := s.UpdateTag(own, public.ID, TagPatch{Name: &name}, public.Version); !errors.Is(err, errTagForbidden) {
		t.Fatal("member changed public dictionary", err)
	}
	var leaked int
	if err := s.db.QueryRow("SELECT count(*) FROM audit WHERE changes LIKE '%Rare private spelling%' OR changes LIKE '%Secret alias spelling%'").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("private spelling entered shared audit", leaked, err)
	}
	for _, test := range []struct {
		name, sql string
		args      []any
		change    func(*authContext)
	}{
		{"revoked", "UPDATE sessions SET revoked_at='revoked' WHERE id=?", []any{own.keyID}, nil},
		{"expired", "UPDATE sessions SET expires_at=? WHERE id=?", []any{persistedTime(time.Now().Add(-time.Minute)), own.keyID}, nil},
		{"idle", "UPDATE sessions SET last_active_at=? WHERE id=?", []any{persistedTime(time.Now().Add(-sessionIdle - time.Minute)), own.keyID}, nil},
		{"disabled", "UPDATE users SET enabled=0 WHERE id=?", []any{member.ID}, nil},
		{"must change password", "UPDATE users SET must_change_password=1 WHERE id=?", []any{member.ID}, nil},
		{"deleted", "INSERT INTO meta VALUES('deleted_user/'||?,'deleted')", []any{member.ID}, nil},
		{"stale auth version", "", nil, func(auth *authContext) { auth.authVersion++ }},
		{"player key", "", nil, func(auth *authContext) { auth.session = false }},
		{"missing session id", "", nil, func(auth *authContext) { auth.keyID = "" }},
		{"other owner session", "", nil, func(auth *authContext) { auth.keyID = otherAuth.keyID }},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := s.tagTransaction(false, func(tx *sql.Tx) error {
				if test.sql != "" {
					if _, err := tx.Exec(test.sql, test.args...); err != nil {
						return err
					}
				}
				auth := own
				if test.change != nil {
					test.change(&auth)
				}
				if _, err := tagActor(tx, auth); !errors.Is(err, errTagSession) {
					t.Errorf("stale session allowed: %v", err)
				}
				return errors.New("rollback fixture")
			})
			if err == nil {
				t.Fatal("fixture did not roll back")
			}
		})
	}
}

func TestTagDictionaryGroupsAndTransactionalNamespace(t *testing.T) {
	s, auth := tagTestStore(t)
	group, err := s.CreateTagGroup(auth, TagGroupInput{Name: " Project  Group ", SortOrder: 20})
	if err != nil {
		t.Fatal(err)
	}
	if group.Name != "Project Group" {
		t.Fatal(group)
	}
	if _, err := s.CreateTagGroup(auth, TagGroupInput{Name: "project group"}); !errors.Is(err, errTagNameTaken) {
		t.Fatal("duplicate group allowed", err)
	}
	for _, builtin := range []string{"genre", "language", "version", "mood", "scene", "custom"} {
		if err := s.DeleteTagGroup(auth, builtin, 1); !errors.Is(err, errTagInUse) {
			t.Fatal("builtin group deleted", builtin, err)
		}
	}
	name := "Renamed Group"
	order := -10
	if _, err := s.UpdateTagGroup(auth, group.ID, TagGroupPatch{Name: &name}, 10); !errors.Is(err, errVersionConflict) {
		t.Fatal("stale group update accepted", err)
	}
	group, err = s.UpdateTagGroup(auth, group.ID, TagGroupPatch{Name: &name, SortOrder: &order}, group.Version)
	if err != nil || group.Version != 2 || group.SortOrder != order {
		t.Fatal(group, err)
	}
	first, err := s.CreateTag(auth, TagInput{GroupID: group.ID, Name: "First", Aliases: []string{"Alias One"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateTag(auth, TagInput{GroupID: group.ID, Name: "Second", Aliases: []string{"Alias Two"}})
	if err != nil {
		t.Fatal(err)
	}
	pageBefore, err := s.ListTags(auth, TagQuery{GroupID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTag(auth, first.ID, TagPatch{Name: &second.Name}, first.Version); !errors.Is(err, errTagNameTaken) {
		t.Fatal("name collision accepted", err)
	}
	aliases := []string{"Alias Two"}
	if _, err := s.UpdateTag(auth, first.ID, TagPatch{Aliases: &aliases}, first.Version); !errors.Is(err, errTagNameTaken) {
		t.Fatal("alias collision accepted", err)
	}
	if after, err := s.GetTag(auth, first.ID); err != nil || !reflect.DeepEqual(after.Aliases, first.Aliases) || after.Name != first.Name || after.Version != first.Version {
		t.Fatal("failed update changed tag", after, err)
	}
	pageAfter, err := s.ListTags(auth, TagQuery{GroupID: group.ID})
	if err != nil || pageAfter.Revision != pageBefore.Revision {
		t.Fatal("failed update changed dictionary revision", pageAfter, err)
	}
	if err := s.DeleteTagGroup(auth, group.ID, group.Version); !errors.Is(err, errTagInUse) {
		t.Fatal("nonempty group deleted", err)
	}
	if err := s.DeleteTag(auth, first.ID, first.Version); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTag(auth, second.ID, second.Version); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTagGroup(auth, group.ID, group.Version); !errors.Is(err, errTagInUse) {
		t.Fatal("tombstone group deleted", err)
	}
	empty, err := s.CreateTagGroup(auth, TagGroupInput{Name: "Empty group"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTagGroup(auth, empty.ID, empty.Version); err != nil {
		t.Fatal(err)
	}
}

func TestTagDictionaryBoundsUnicodeAndPagination(t *testing.T) {
	s, auth := tagTestStore(t)
	if _, err := s.CreateTag(auth, TagInput{GroupID: "genre", Name: strings.Repeat("语", 64)}); err != nil {
		t.Fatal("64-rune name rejected", err)
	}
	if _, err := s.CreateTag(auth, TagInput{GroupID: "genre", Name: strings.Repeat("语", 65)}); !errors.Is(err, errTagInvalid) {
		t.Fatal("65-rune name accepted", err)
	}
	if _, err := s.CreateTag(auth, TagInput{GroupID: "genre", Name: "Straße"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(auth, TagInput{GroupID: "genre", Name: "STRASSE"}); !errors.Is(err, errTagNameTaken) {
		t.Fatal("Unicode casefold collision accepted", err)
	}
	for _, input := range []TagInput{
		{GroupID: "missing", Name: "Name"}, {GroupID: "genre", Scope: "other", Name: "Name"},
		{GroupID: "genre", Name: "Name", Aliases: []string{"NAME"}},
		{GroupID: "genre", Name: "Name", Aliases: make([]string, 33)},
	} {
		if _, err := s.CreateTag(auth, input); !errors.Is(err, errTagInvalid) {
			t.Fatal("invalid input accepted", input, err)
		}
	}
	for _, query := range []TagQuery{{Offset: -1}, {Offset: 1000001}, {Limit: -1}, {Limit: 101}, {GroupID: "missing"}, {Query: "bad\nquery"}, {Query: strings.Repeat("q", 65)}, {Scope: "other"}} {
		if _, err := s.ListTags(auth, query); !errors.Is(err, errTagInvalid) {
			t.Fatal("invalid query accepted", query, err)
		}
	}
	first, err := s.ListTags(auth, TagQuery{GroupID: "genre", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ListTags(auth, TagQuery{GroupID: "genre", Offset: 1, Limit: 1})
	if err != nil || first.Total != 2 || second.Total != 2 || len(first.Items) != 1 || len(second.Items) != 1 || first.Items[0].ID == second.Items[0].ID {
		t.Fatal("incorrect bounded pages", first, second, err)
	}
	for _, scope := range []string{"public", "personal"} {
		t.Run(scope, func(t *testing.T) {
			scopeID, _ := tagScopeID(auth.userID, scope)
			limit := publicTagLimit
			if scope == "personal" {
				limit = personalTagLimit
				if err := s.tagTransaction(false, func(tx *sql.Tx) error { return ensureTagScope(tx, auth.userID) }); err != nil {
					t.Fatal(err)
				}
			}
			var current int
			if err := s.db.QueryRow("SELECT count(*) FROM tags WHERE scope_id=? AND deleted=0", scopeID).Scan(&current); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO tags(id,scope_id,group_id,name,normalized_name,version,deleted,created_at,updated_at) SELECT printf('00000000-0000-4000-8000-%012d',x),?,'scene','limit-'||x,'limit-'||x,1,0,'created','updated' FROM n`, limit-current, scopeID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateTag(auth, TagInput{Scope: scope, GroupID: "scene", Name: "Over quota"}); !errors.Is(err, errTagLimit) {
				t.Fatal("dictionary cap bypassed", err)
			}
			if _, err := s.db.Exec("DELETE FROM tags WHERE scope_id=? AND group_id='scene'", scopeID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTagDictionaryDeletionRejectsEveryReference(t *testing.T) {
	s, auth := tagTestStore(t)
	trackID := s.Tracks()[0].ID
	for _, kind := range []string{"track", "override", "filter", "merged"} {
		t.Run(kind, func(t *testing.T) {
			tag, err := s.CreateTag(auth, TagInput{GroupID: "custom", Name: "In use " + kind})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "track":
				_, err = s.db.Exec("INSERT INTO track_tags VALUES(?,?,?,'manual',?,'created')", trackID, tag.ID, tag.scopeID, auth.userID)
			case "override":
				_, err = s.db.Exec("INSERT INTO track_tag_overrides VALUES(?,?,?,'suppressed',?,'updated')", trackID, tag.ID, tag.scopeID, auth.userID)
			case "filter":
				_, err = s.db.Exec("INSERT INTO saved_track_filters VALUES(?,?,'Saved',?,1,'created','updated')", uuid.NewString(), auth.userID, fmt.Sprintf(`{"include":["%s"]}`, tag.ID))
			case "merged":
				_, err = s.db.Exec("INSERT INTO tags VALUES(?,'public','custom','Tombstone','tombstone',2,1,?,'created','updated')", uuid.NewString(), tag.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteTag(auth, tag.ID, tag.Version); !errors.Is(err, errTagInUse) {
				t.Fatal("referenced tag deleted", kind, err)
			}
			actual, err := s.GetTag(auth, tag.ID)
			if err != nil || actual.Deleted || actual.Version != tag.Version {
				t.Fatal("rejected delete changed tag", actual, err)
			}
		})
	}
}
