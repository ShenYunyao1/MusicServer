package music

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func savedFilterRule(scope, id string) TrackFilter {
	return TrackFilter{Version: 1, Tags: []TrackTagClause{{Scope: scope, GroupID: "genre", IncludeIDs: []string{id}}}}
}

func TestSavedTrackFilterCRUDNamesVersionsAndPrivateAudit(t *testing.T) {
	s, auth := tagTestStore(t)
	tag := mustLinkTag(t, s, auth, "personal", "Secret filter tag")
	stop := s.watchCommunity()
	defer stop()
	signal := s.communitySignal()
	revision := s.Revision()
	filter, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: "  CAFÉ   favorites ", Filter: savedFilterRule("personal", tag.ID)})
	if err != nil || filter.Name != "CAFÉ favorites" || filter.Version != 1 || !filter.Validation.Valid {
		t.Fatal(filter, err)
	}
	if _, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: "cafe\u0301 FAVORITES", Filter: filter.Filter}); !errors.Is(err, errSavedFilterNameTaken) {
		t.Fatal("duplicate name accepted", err)
	}
	before := tagAuditCount(t, s)
	name := filter.Name
	unchanged, err := s.UpdateSavedTrackFilter(auth, filter.ID, SavedTrackFilterPatch{Name: &name, Filter: &filter.Filter}, 1)
	if err != nil || unchanged.Version != 1 || tagAuditCount(t, s) != before {
		t.Fatal("no-op revised filter", unchanged, err)
	}
	name = "Updated secret name"
	if _, err := s.UpdateSavedTrackFilter(auth, filter.ID, SavedTrackFilterPatch{Name: &name}, 99); !errors.Is(err, errVersionConflict) {
		t.Fatal(err)
	}
	updated, err := s.UpdateSavedTrackFilter(auth, filter.ID, SavedTrackFilterPatch{Name: &name}, 1)
	if err != nil || updated.Version != 2 {
		t.Fatal(updated, err)
	}
	items, err := s.ListSavedTrackFilters(auth)
	if err != nil || len(items) != 1 || items[0].ID != filter.ID {
		t.Fatal(items, err)
	}
	if err := s.DeleteSavedTrackFilter(auth, filter.ID, 1); !errors.Is(err, errVersionConflict) {
		t.Fatal(err)
	}
	if err := s.DeleteSavedTrackFilter(auth, filter.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSavedTrackFilter(auth, filter.ID); !errors.Is(err, errResourceHidden) {
		t.Fatal(err)
	}
	if s.Revision() != revision {
		t.Fatal("saved filters changed public revision")
	}
	select {
	case <-signal:
		t.Fatal("private filter notified community")
	default:
	}
	var leaked int
	if err := s.db.QueryRow("SELECT count(*) FROM audit WHERE action LIKE 'filter.%' AND (entity_id<>? OR changes LIKE '%Secret%' OR changes LIKE '%'||?||'%' OR changes LIKE '%'||?||'%')", auth.userID, filter.ID, tag.ID).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("saved filter leaked audit content", leaked, err)
	}
}

func TestSavedTrackFilterOwnerIsolationAndLiveSession(t *testing.T) {
	a, admin, alice, own, _, other := tagLinkFixture(t)
	s := a.store
	tag := mustLinkTag(t, s, own, "personal", "Private saved rule")
	filter, err := s.CreateSavedTrackFilter(own, SavedTrackFilterInput{Name: "Mine", Filter: savedFilterRule("personal", tag.ID)})
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []authContext{admin, other} {
		if _, err := s.GetSavedTrackFilter(actor, filter.ID); !errors.Is(err, errResourceHidden) {
			t.Fatal("foreign read", err)
		}
		name := "hijack"
		if _, err := s.UpdateSavedTrackFilter(actor, filter.ID, SavedTrackFilterPatch{Name: &name}, 999); !errors.Is(err, errResourceHidden) {
			t.Fatal("foreign update leaked version", err)
		}
		if err := s.DeleteSavedTrackFilter(actor, filter.ID, 999); !errors.Is(err, errResourceHidden) {
			t.Fatal("foreign delete leaked version", err)
		}
		items, err := s.ListSavedTrackFilters(actor)
		if err != nil || len(items) != 0 {
			t.Fatal("foreign list", items, err)
		}
	}
	setEmailTestRequirement(t, s, alice.ID, true)
	if _, err := s.GetSavedTrackFilter(own, filter.ID); err != nil {
		t.Fatal("email policy blocked rule", err)
	}
	if err := s.RevokeSession(own.userID, own.keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSavedTrackFilter(own, filter.ID); !errors.Is(err, errTagSession) {
		t.Fatal("revoked read", err)
	}
	if _, err := s.CreateSavedTrackFilter(own, SavedTrackFilterInput{Name: "revoked", Filter: TrackFilter{Version: 1}}); !errors.Is(err, errTagSession) {
		t.Fatal("revoked write", err)
	}
}

func TestSavedTrackFilterMergedAndBrokenReferencesKeepOriginalDraft(t *testing.T) {
	s, auth := tagTestStore(t)
	source := mustLinkTag(t, s, auth, "personal", "Source")
	target := mustLinkTag(t, s, auth, "personal", "Target")
	filter, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: "Keep original", Filter: savedFilterRule("personal", source.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeTags(auth, source.ID, target.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSavedTrackFilter(auth, filter.ID)
	if err != nil || !got.Validation.Valid || len(got.Validation.Redirects) != 1 || got.Validation.ResolvedFilter == nil || got.Filter.Tags[0].IncludeIDs[0] != source.ID || got.Validation.ResolvedFilter.Tags[0].IncludeIDs[0] != target.ID || got.Version != 1 {
		t.Fatal(got, err)
	}
	// Simulate an out-of-band or future lifecycle invalidation; normal tag deletion
	// protects saved references, but reading must remain safe after corruption.
	if _, err := s.db.Exec("UPDATE tags SET deleted=1 WHERE id=?", target.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSavedTrackFilter(auth, filter.ID)
	if err != nil || got.Validation.Valid || got.Validation.Code != "FILTER_REFERENCE_INVALID" || got.Filter.Tags[0].IncludeIDs[0] != source.ID || got.Validation.ResolvedFilter != nil {
		t.Fatal("invalid rule widened or lost draft", got, err)
	}
	items, err := s.ListSavedTrackFilters(auth)
	if err != nil || len(items) != 1 || items[0].Validation.Valid {
		t.Fatal(items, err)
	}
	name := "Still editable"
	if _, err := s.UpdateSavedTrackFilter(auth, filter.ID, SavedTrackFilterPatch{Name: &name}, 1); err != nil {
		t.Fatal("rename invalid draft blocked", err)
	}
	if _, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: "Invalid new", Filter: filter.Filter}); !errors.Is(err, errFilterReference) {
		t.Fatal("invalid new rule accepted", err)
	}
}

func TestSavedTrackFilterBoundsAndSchemaGate(t *testing.T) {
	s, _, _ := seedStore(t)
	defer closeStoreForTest(t, s)
	if _, err := s.ListSavedTrackFilters(tagTestAuth(t, s, "admin")); !errors.Is(err, errTagsUnavailable) {
		t.Fatal(err)
	}
	if err := initializeSQLiteSchemaForTarget(s.db, 8); err != nil {
		t.Fatal(err)
	}
	auth := tagTestAuth(t, s, "admin")
	oversized := TrackFilter{Version: 1, Artists: make([]string, 20)}
	for i := range oversized.Artists {
		oversized.Artists[i] = strings.Repeat("😀", 256)
	}
	if _, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: "Too large", Filter: oversized}); !errors.Is(err, errTagInvalid) {
		t.Fatal("JSON limit ignored", err)
	}
	for _, name := range []string{"", "\tbad", strings.Repeat("x", 65)} {
		if _, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: name, Filter: TrackFilter{Version: 1}}); !errors.Is(err, errTagInvalid) {
			t.Fatal(name, err)
		}
	}
	for i := 0; i < 100; i++ {
		if _, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: fmt.Sprintf("Rule %03d", i), Filter: TrackFilter{Version: 1}}); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: "Overflow", Filter: TrackFilter{Version: 1}}); !errors.Is(err, errTagLimit) {
		t.Fatal("limit ignored", err)
	}
}

func TestSavedTrackFilterDatabaseFailureIsNotAnInvalidDraft(t *testing.T) {
	s, auth := tagTestStore(t)
	tag := mustLinkTag(t, s, auth, "personal", "SQL failure")
	filter, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: "Still mine", Filter: savedFilterRule("personal", tag.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ALTER TABLE tag_aliases RENAME COLUMN display_name TO unavailable_column"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSavedTrackFilter(auth, filter.ID); err == nil || errors.Is(err, errFilterReference) || errors.Is(err, errResourceHidden) || errors.Is(err, errTagInvalid) {
		t.Fatal("database failure masked as validation", err)
	}
	if _, err := s.ListSavedTrackFilters(auth); err == nil {
		t.Fatal("list masked database failure")
	}
}

func TestSavedTrackFilterReplacingRuleClearsOmittedConditions(t *testing.T) {
	s, auth := tagTestStore(t)
	tag := mustLinkTag(t, s, auth, "personal", "Old condition")
	rule := savedFilterRule("personal", tag.ID)
	hasCover := true
	rule.HasCover = &hasCover
	created, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: "Replace conditions", Filter: rule})
	if err != nil {
		t.Fatal(err)
	}
	replacement := TrackFilter{Version: 1, Search: "new query"}
	updated, err := s.UpdateSavedTrackFilter(auth, created.ID, SavedTrackFilterPatch{Filter: &replacement}, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Filter.Tags) != 0 || updated.Filter.HasCover != nil || updated.Filter.Search != "new query" || updated.Validation.ResolvedFilter == nil || len(updated.Validation.ResolvedFilter.Tags) != 0 || updated.Validation.ResolvedFilter.HasCover != nil {
		t.Fatal("PATCH retained removed conditions", updated)
	}
	stored, err := s.GetSavedTrackFilter(auth, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Filter.Tags) != 0 || stored.Filter.HasCover != nil || stored.Filter.Search != updated.Filter.Search {
		t.Fatal("PATCH disagrees with persisted rule", updated, stored)
	}
}

func TestSavedTrackFilterOldEmptyClauseStaysInvalidAndEditable(t *testing.T) {
	s, auth := tagTestStore(t)
	created, err := s.CreateSavedTrackFilter(auth, SavedTrackFilterInput{Name: "Old draft", Filter: TrackFilter{Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE saved_track_filters SET query_json='{"version":1,"tags":[{"scope":"public","group_id":"genre"}]}' WHERE id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSavedTrackFilter(auth, created.ID)
	if err != nil || got.Validation.Valid || got.Validation.Code != "FILTER_INVALID" || len(got.Filter.Tags) != 1 {
		t.Fatal("empty legacy clause widened or lost", got, err)
	}
	name := "Manage invalid draft"
	got, err = s.UpdateSavedTrackFilter(auth, created.ID, SavedTrackFilterPatch{Name: &name}, created.Version)
	if err != nil || got.Validation.Valid || len(got.Filter.Tags) != 1 {
		t.Fatal(got, err)
	}
	if err := s.DeleteSavedTrackFilter(auth, created.ID, got.Version); err != nil {
		t.Fatal(err)
	}
}
