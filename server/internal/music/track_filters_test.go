package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func filterFixture(t *testing.T) (*Store, authContext, []string, map[string]Tag) {
	t.Helper()
	s, auth := tagTestStore(t)
	ids := batchTracks(t, s, 4)
	tags := map[string]Tag{}
	for name, group := range map[string]string{"rock": "genre", "pop": "genre", "ja": "language", "night": "scene"} {
		scope := "public"
		if name == "night" {
			scope = "personal"
		}
		tag, err := s.CreateTag(auth, TagInput{Scope: scope, GroupID: group, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		tags[name] = tag
	}
	for i, id := range ids {
		if err := s.tagTransaction(false, func(tx *sql.Tx) error {
			tr, err := readTrackFrom(tx, id, false)
			if err != nil {
				return err
			}
			tr.Title = "Same"
			tr.Artist = "Singer"
			tr.Album = "Album"
			tr.Format = "flac"
			tr.DurationMS = int64((i + 1) * 1000)
			tr.Lyrics = "words"
			if i == 3 {
				tr.Format = "mp3"
				tr.Lyrics = ""
			}
			return writeTrack(tx, tr)
		}); err != nil {
			t.Fatal(err)
		}
		add := []string{tags["rock"].ID}
		if i == 1 {
			add = append(add, tags["pop"].ID)
		}
		if i == 2 {
			add = []string{tags["pop"].ID}
		}
		if i == 3 {
			add = nil
		}
		if len(add) > 0 {
			if _, err := s.ChangeTrackTags(auth, id, "public", 1, add, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, id := range ids[:2] {
		v, _ := s.GetTrackTags(auth, id)
		if _, err := s.ChangeTrackTags(auth, id, "public", v.PublicVersion, []string{tags["ja"].ID}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ChangeTrackTags(auth, ids[0], "personal", 1, []string{tags["night"].ID}, nil); err != nil {
		t.Fatal(err)
	}
	return s, auth, ids, tags
}

func TestTrackFilterAnyAllExcludeAndScalarFields(t *testing.T) {
	s, auth, ids, tags := filterFixture(t)
	base := TrackFilter{Version: 1, Search: "Same", Tags: []TrackTagClause{{Scope: "public", GroupID: "genre", IncludeIDs: []string{tags["rock"].ID, tags["pop"].ID}}, {Scope: "public", GroupID: "language", IncludeIDs: []string{tags["ja"].ID}}}}
	page, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: base, Sort: "duration_ms"})
	if err != nil || page.Total != 2 {
		t.Fatal(page, err)
	}
	base.Tags[0].Match = "all"
	page, err = s.QueryTrackFilter(auth, TrackFilterQuery{Filter: base})
	if err != nil || page.Total != 1 || page.Items[0]["id"] != ids[1] {
		t.Fatal(page, err)
	}
	base.Tags[0].Match = "any"
	base.Tags[0].ExcludeIDs = []string{tags["pop"].ID}
	page, err = s.QueryTrackFilter(auth, TrackFilterQuery{Filter: base})
	if err != nil || page.Total != 1 || page.Items[0]["id"] != ids[0] {
		t.Fatal(page, err)
	}
	has := true
	min, max := int64(2000), int64(3000)
	page, err = s.QueryTrackFilter(auth, TrackFilterQuery{Filter: TrackFilter{Version: 1, Search: "Same", Artists: []string{"Singer"}, Albums: []string{"Album"}, Formats: []string{"flac"}, DurationMinMS: &min, DurationMaxMS: &max, HasLyrics: &has}})
	if err != nil || page.Total != 2 {
		t.Fatal(page, err)
	}
}

func TestTrackFilterPrivateScopeBaselineAndStablePages(t *testing.T) {
	s, auth, _, tags := filterFixture(t)
	f := TrackFilter{Version: 1, Search: "Same"}
	p1, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f, Limit: 2, Offset: 2, Baseline: &p1.Baseline})
	if err != nil || p1.Total != 4 || p2.Total != 4 {
		t.Fatal(p1, p2, err)
	}
	all := append(append([]map[string]any{}, p1.Items...), p2.Items...)
	for i := 1; i < len(all); i++ {
		if all[i-1]["id"].(string) >= all[i]["id"].(string) {
			t.Fatal("unstable UUID order", all)
		}
	}
	if _, err := s.CreateTag(auth, TagInput{Scope: "personal", GroupID: "scene", Name: "new private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f, Baseline: &p1.Baseline}); !errors.Is(err, errFilterSnapshot) {
		t.Fatal("stale private epoch accepted", err)
	}
	api := NewAPI(s)
	_, token := readyMember(t, api, "filter-other")
	u, session, _ := s.sessionIdentity(token)
	other := authContext{session: true, userID: u.ID, keyID: session.ID, authVersion: u.AuthVersion, role: u.Role}
	private := TrackFilter{Version: 1, Tags: []TrackTagClause{{Scope: "personal", GroupID: "scene", IncludeIDs: []string{tags["night"].ID}}}}
	if _, err := s.QueryTrackFilter(other, TrackFilterQuery{Filter: private}); !errors.Is(err, errResourceHidden) {
		t.Fatal("private tag leaked", err)
	}
	if _, err := s.QueryTrackFilter(other, TrackFilterQuery{Filter: f, Baseline: &p1.Baseline}); !errors.Is(err, errFilterSnapshot) {
		t.Fatal("other account epoch accepted", err)
	}
	page, err := s.QueryTrackFilter(other, TrackFilterQuery{Filter: f})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item["path"] != "" {
			t.Fatal("path leaked")
		}
		if _, ok := item["lyrics"]; ok {
			t.Fatal("lyrics returned in list")
		}
	}
}

func TestTrackFilterMergeRedirectAndDeletedReference(t *testing.T) {
	s, auth, _, tags := filterFixture(t)
	f := TrackFilter{Version: 1, Tags: []TrackTagClause{{Scope: "public", GroupID: "genre", IncludeIDs: []string{tags["rock"].ID}}}}
	if _, err := s.MergeTags(auth, tags["rock"].ID, tags["pop"].ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f})
	if err != nil || len(page.Redirects) != 1 || page.Redirects[0].To != tags["pop"].ID || page.Total != 3 {
		t.Fatal(page, err)
	}
	deleted, err := s.CreateTag(auth, TagInput{Scope: "public", GroupID: "genre", Name: "deleted"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTag(auth, deleted.ID, 1); err != nil {
		t.Fatal(err)
	}
	f.Tags[0].IncludeIDs = []string{deleted.ID}
	if _, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f}); !errors.Is(err, errFilterReference) {
		t.Fatal("deleted condition broadened", err)
	}
}

func TestTrackFilterRejectsInvalidInputAndSchemaSeven(t *testing.T) {
	s, auth, _, tags := filterFixture(t)
	for _, f := range []TrackFilter{{Version: 2}, {Version: 1, Range: "injected"}, {Version: 1, Formats: []string{"mp3'); DROP TABLE tracks;--"}}, {Version: 1, Tags: []TrackTagClause{{Scope: "personal", GroupID: "genre", IncludeIDs: []string{tags["rock"].ID}}}}} {
		if _, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f}); err == nil {
			t.Fatal("invalid filter accepted", f)
		}
	}
	for _, q := range []TrackFilterQuery{{Filter: TrackFilter{Version: 1}, Sort: "title DESC;"}, {Filter: TrackFilter{Version: 1}, Direction: "injected"}, {Filter: TrackFilter{Version: 1}, Limit: 101}} {
		if _, err := s.QueryTrackFilter(auth, q); !errors.Is(err, errTagInvalid) {
			t.Fatal(err)
		}
	}
	api, _ := testAPIForSchema(t, 7)
	if _, err := api.store.QueryTrackFilter(tagTestAuth(t, api.store, "admin"), TrackFilterQuery{Filter: TrackFilter{Version: 1}}); !errors.Is(err, errTagsUnavailable) {
		t.Fatal(err)
	}
	before := s.Revision()
	_, _ = s.QueryTrackFilter(auth, TrackFilterQuery{Filter: TrackFilter{Version: 1}})
	if s.Revision() != before {
		t.Fatal("read changed revision")
	}
}

func TestTrackFilterRejectsEmptyTagClauseInsteadOfBroadening(t *testing.T) {
	s, auth, _, _ := filterFixture(t)
	f := TrackFilter{Version: 1, Tags: []TrackTagClause{{Scope: "public", GroupID: "genre"}}}
	if _, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f}); !errors.Is(err, errTagInvalid) {
		t.Fatal("empty explicit tag clause broadened to the full library", err)
	}
}

func TestTagFacetsRemoveOnlyCurrentClauseAndPreserveOtherFilters(t *testing.T) {
	s, auth, _, tags := filterFixture(t)
	f := TrackFilter{Version: 1, Search: "Same", Tags: []TrackTagClause{{Scope: "public", GroupID: "genre", IncludeIDs: []string{tags["rock"].ID}}, {Scope: "public", GroupID: "language", IncludeIDs: []string{tags["ja"].ID}}}}
	page, err := s.TrackTagFacets(auth, TagFacetQuery{Filter: f, Scope: "public", GroupID: "genre"})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, item := range page.Items {
		counts[item.Tag.ID] = item.Count
	}
	if !reflect.DeepEqual(counts, map[string]int{tags["rock"].ID: 2, tags["pop"].ID: 1}) {
		t.Fatal(counts)
	}
	page, err = s.TrackTagFacets(auth, TagFacetQuery{Filter: f, Scope: "personal", GroupID: "scene"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Count != 1 {
		t.Fatal(page, err)
	}
}

func TestTrackFilterUntaggedSuppressionAndScopeRevisionIsolation(t *testing.T) {
	s, admin, ids, tags := filterFixture(t)
	api := NewAPI(s)
	user, _ := readyMember(t, api, "scope-isolated")
	other := tagMemberAuth(t, s, user.Username)
	q := TrackFilterQuery{Filter: TrackFilter{Version: 1, Search: "Same"}}
	before, err := s.QueryTrackFilter(other, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(admin, TagInput{Scope: "personal", GroupID: "scene", Name: "Private change"}); err != nil {
		t.Fatal(err)
	}
	q.Baseline = &before.Baseline
	if _, err := s.QueryTrackFilter(other, q); err != nil {
		t.Fatal("other user's private revision invalidated page", err)
	}
	q.Baseline = nil
	q.Filter.UntaggedPersonal = true
	page, err := s.QueryTrackFilter(admin, q)
	if err != nil || page.Total != 3 {
		t.Fatal(page, err)
	}
	q.Filter.UntaggedPersonal = false
	q.Filter.UntaggedPublic = true
	page, err = s.QueryTrackFilter(admin, q)
	if err != nil || page.Total != 1 || page.Items[0]["id"] != ids[3] {
		t.Fatal(page, err)
	}
	// Simulate a protected imported link left by an importer. Every filter and
	// count must use the effective view rather than raw presence in track_tags.
	if _, err := s.db.Exec("INSERT INTO track_tag_overrides VALUES(?,?,'public','suppressed',?,'updated') ON CONFLICT(track_id,tag_id) DO UPDATE SET action='suppressed'", ids[0], tags["rock"].ID, admin.userID); err != nil {
		t.Fatal(err)
	}
	f := TrackFilter{Version: 1, Search: "Same", Tags: []TrackTagClause{{Scope: "public", GroupID: "genre", IncludeIDs: []string{tags["rock"].ID}}}}
	page, err = s.QueryTrackFilter(admin, TrackFilterQuery{Filter: f})
	if err != nil || page.Total != 1 || page.Items[0]["id"] != ids[1] {
		t.Fatal(page, err)
	}
	facets, err := s.TrackTagFacets(admin, TagFacetQuery{Filter: TrackFilter{Version: 1, Search: "Same"}, Scope: "public", GroupID: "genre"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range facets.Items {
		if item.Tag.ID == tags["rock"].ID && item.Count != 1 {
			t.Fatal("suppressed link counted", facets)
		}
	}
}

func TestTrackFilterOwnershipOrphanProjectionAndLiveRole(t *testing.T) {
	s, admin, ids, _ := filterFixture(t)
	api := NewAPI(s)
	user, _ := readyMember(t, api, "filter-owner")
	member := tagMemberAuth(t, s, user.Username)
	if err := s.transaction(func(tx *sql.Tx) error {
		tr, err := readTrackFrom(tx, ids[0], false)
		if err != nil {
			return err
		}
		tr.OwnerID = user.ID
		tr.CoverID = "cover"
		if err := writeTrack(tx, tr); err != nil {
			return err
		}
		tr, err = readTrackFrom(tx, ids[1], false)
		if err != nil {
			return err
		}
		tr.UploadedBy = user.ID
		tr.Missing = true
		return writeTrack(tx, tr)
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ rangeName, id string }{{"mine", ids[1]}, {"managed", ids[0]}} {
		page, err := s.QueryTrackFilter(member, TrackFilterQuery{Filter: TrackFilter{Version: 1, Search: "Same", Range: test.rangeName}})
		if err != nil || page.Total != 1 || page.Items[0]["id"] != test.id {
			t.Fatal(page, err)
		}
	}
	if err := s.PutPlaylist(Playlist{ID: "filter-members", Name: "Owned", TrackIDs: []string{ids[0]}}, MutationOptions{UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryTrackFilter(member, TrackFilterQuery{Filter: TrackFilter{Version: 1, Search: "Same", Range: "orphan"}})
	if err != nil || page.Total != 3 {
		t.Fatal(page, err)
	}
	if _, err := s.QueryTrackFilter(member, TrackFilterQuery{Filter: TrackFilter{Version: 1, Range: "missing"}}); !errors.Is(err, errResourceHidden) {
		t.Fatal("member missing range accepted", err)
	}
	if _, err := s.QueryTrackFilter(member, TrackFilterQuery{Filter: TrackFilter{Version: 1, UploadedBy: user.ID}}); !errors.Is(err, errTagForbidden) {
		t.Fatal("member uploader filter accepted", err)
	}
	page, err = s.QueryTrackFilter(admin, TrackFilterQuery{Filter: TrackFilter{Version: 1, UploadedBy: user.ID}})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	cover := true
	page, err = s.QueryTrackFilter(member, TrackFilterQuery{Filter: TrackFilter{Version: 1, HasCover: &cover}})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	// Even a deliberately stale admin role snapshot must not grant hidden
	// projection or administrator-only filter authority.
	member.role = "admin"
	page, err = s.QueryTrackFilter(member, TrackFilterQuery{Filter: TrackFilter{Version: 1, Search: "Same"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item["path"] != "" {
			t.Fatal("stale admin role exposed media path")
		}
	}
	if _, err := s.QueryTrackFilter(member, TrackFilterQuery{Filter: TrackFilter{Version: 1, UploadedBy: user.ID}}); !errors.Is(err, errTagForbidden) {
		t.Fatal("stale admin role authorized uploader", err)
	}
}

func TestTrackFilterMergedContradictionAndInvalidPrivateReferences(t *testing.T) {
	s, auth, _, tags := filterFixture(t)
	f := TrackFilter{Version: 1, Search: "Same", Tags: []TrackTagClause{{Scope: "public", GroupID: "genre", IncludeIDs: []string{tags["rock"].ID}, ExcludeIDs: []string{tags["pop"].ID}}}}
	if _, err := s.MergeTags(auth, tags["rock"].ID, tags["pop"].ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f})
	if err != nil || page.Total != 0 || len(page.Redirects) != 1 {
		t.Fatal("merge broadened contradictory condition", page, err)
	}
	private, err := s.CreateTag(auth, TagInput{Scope: "personal", GroupID: "scene", Name: "Gone private"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTag(auth, private.ID, 1); err != nil {
		t.Fatal(err)
	}
	f = TrackFilter{Version: 1, Tags: []TrackTagClause{{Scope: "personal", GroupID: "scene", IncludeIDs: []string{private.ID}}}}
	if _, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f}); !errors.Is(err, errFilterReference) {
		t.Fatal(err)
	}
	f.Tags[0].IncludeIDs = []string{uuid.NewString()}
	if _, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f}); !errors.Is(err, errResourceHidden) {
		t.Fatal(err)
	}
	f.Tags[0].Scope = "public"
	if _, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f}); !errors.Is(err, errResourceHidden) {
		t.Fatal("nonexistent public UUID reveals private existence", err)
	}
	encoded, _ := json.Marshal(page)
	if string(encoded) == "" {
		t.Fatal("invalid JSON page")
	}
}

func TestTrackFilterFacetPagingBoundsAndRevokedSession(t *testing.T) {
	s, auth, _, _ := filterFixture(t)
	q := TagFacetQuery{Filter: TrackFilter{Version: 1, Search: "Same"}, Scope: "public", GroupID: "genre", Limit: 1}
	one, err := s.TrackTagFacets(auth, q)
	if err != nil || one.Total != 2 || len(one.Items) != 1 {
		t.Fatal(one, err)
	}
	q.Offset = 1
	q.Baseline = &one.Baseline
	two, err := s.TrackTagFacets(auth, q)
	if err != nil || len(two.Items) != 1 || one.Items[0].Tag.ID == two.Items[0].Tag.ID {
		t.Fatal(two, err)
	}
	for _, f := range []TrackFilter{{Version: 1, Search: string([]byte{0xff})}, {Version: 1, DurationMinMS: func() *int64 { v := int64(-1); return &v }()}, {Version: 1, Artists: make([]string, 21)}, {Version: 1, Tags: make([]TrackTagClause, 13)}} {
		if _, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: f}); !errors.Is(err, errTagInvalid) {
			t.Fatal(f, err)
		}
	}
	if _, err := s.db.Exec("UPDATE sessions SET expires_at=? WHERE id=?", persistedTime(time.Now().Add(-time.Second)), auth.keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrackTagFacets(auth, q); !errors.Is(err, errTagSession) {
		t.Fatal("expired facet session accepted", err)
	}
	if _, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: TrackFilter{Version: 1}}); !errors.Is(err, errTagSession) {
		t.Fatal("expired query session accepted", err)
	}
}
