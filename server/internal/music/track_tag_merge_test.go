package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func mergeTestTrack(t *testing.T, s *Store, auth authContext) Track {
	t.Helper()
	track := s.Tracks()[0]
	track.ID = uuid.NewString()
	track.Path = track.ID + ".mp3"
	track.OwnerID = auth.userID
	track.UploadedBy = auth.userID
	track.Version = 1
	if err := s.tagTransaction(false, func(tx *sql.Tx) error { return writeTrack(tx, track) }); err != nil {
		t.Fatal(err)
	}
	return track
}

func setMergeTestRelation(t *testing.T, s *Store, auth authContext, trackID string, tag Tag, source, action string) {
	t.Helper()
	if source != "" {
		if _, err := s.db.Exec("INSERT INTO track_tags(track_id,tag_id,scope_id,source,created_by,created_at) VALUES(?,?,?,?,?,'created')", trackID, tag.ID, tag.scopeID, source, auth.userID); err != nil {
			t.Fatal(err)
		}
	}
	if action != "" {
		if _, err := s.db.Exec("INSERT INTO track_tag_overrides(track_id,tag_id,scope_id,action,updated_by,updated_at) VALUES(?,?,?,?,?,'updated')", trackID, tag.ID, tag.scopeID, action, auth.userID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTrackTagMergePreservesNamespaceAndSourcePrecedence(t *testing.T) {
	s, auth := tagTestStore(t)
	for i, tc := range []struct{ name, left, right, lo, ro, wantLink, wantOverride string }{
		{"manual wins suppression", "manual", "imported", "confirmed", "suppressed", "manual", "confirmed"},
		{"target manual wins source suppression", "imported", "manual", "suppressed", "", "manual", "confirmed"},
		{"suppression removes imported", "imported", "imported", "suppressed", "", "", "suppressed"},
		{"confirmed imported wins suppression", "imported", "imported", "confirmed", "suppressed", "imported", "confirmed"},
		{"imported union", "imported", "imported", "", "", "imported", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := mustLinkTag(t, s, auth, "public", fmt.Sprintf("merge-source-%d", i))
			target := mustLinkTag(t, s, auth, "public", fmt.Sprintf("merge-target-%d", i))
			track := mergeTestTrack(t, s, auth)
			unchanged := mergeTestTrack(t, s, auth)
			setMergeTestRelation(t, s, auth, track.ID, source, tc.left, tc.lo)
			setMergeTestRelation(t, s, auth, track.ID, target, tc.right, tc.ro)
			setMergeTestRelation(t, s, auth, unchanged.ID, target, "imported", "")
			before := s.Revision()
			result, err := s.MergeTags(auth, source.ID, target.ID, source.Version, target.Version)
			if err != nil {
				t.Fatal(err)
			}
			if result.AffectedTracks != 1 || !result.Source.Deleted || result.Source.MergedInto != target.ID || result.Target.Version != 2 || !slices.Contains(result.Target.Aliases, source.Name) {
				t.Fatalf("merge=%+v", result)
			}
			if s.Revision() != before+1 {
				t.Fatal("merge did not publish exactly one changed track")
			}
			var link, override string
			e := s.db.QueryRow("SELECT source FROM track_tags WHERE track_id=? AND tag_id=?", track.ID, target.ID).Scan(&link)
			if tc.wantLink == "" && !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("suppressed imported survived", e)
			}
			if link != tc.wantLink {
				t.Fatal(link, tc.wantLink)
			}
			_ = s.db.QueryRow("SELECT action FROM track_tag_overrides WHERE track_id=? AND tag_id=?", track.ID, target.ID).Scan(&override)
			if override != tc.wantOverride {
				t.Fatal(override, tc.wantOverride)
			}
			version, _ := trackTagVersion(s.db, track.ID, "public")
			if version != 2 {
				t.Fatal("merged state version", version)
			}
			version, _ = trackTagVersion(s.db, unchanged.ID, "public")
			if version != 1 {
				t.Fatal("unaffected target-only track bumped", version)
			}
			current, _ := s.Track(unchanged.ID)
			if current.Version != 1 {
				t.Fatal("unaffected target-only track updated")
			}
			if _, err = s.MergeTags(auth, source.ID, target.ID, source.Version, target.Version); !errors.Is(err, errVersionConflict) {
				t.Fatal("repeated stale merge accepted", err)
			}
		})
	}
}

func TestTrackTagMergePrivateFlatteningAndConflictAtomicity(t *testing.T) {
	s, auth := tagTestStore(t)
	a := mustLinkTag(t, s, auth, "personal", "private merge A")
	b := mustLinkTag(t, s, auth, "personal", "private merge B")
	c := mustLinkTag(t, s, auth, "personal", "private merge C")
	track := s.Tracks()[0]
	if _, err := s.ChangeTrackTags(auth, track.ID, "personal", 1, []string{a.ID}, nil); err != nil {
		t.Fatal(err)
	}
	before := s.Revision()
	oldTrack, _ := s.Track(track.ID)
	ab, err := s.MergeTags(auth, a.ID, b.ID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeTags(auth, b.ID, c.ID, 1, 1); !errors.Is(err, errVersionConflict) {
		t.Fatal("stale target merge accepted", err)
	}
	bc, err := s.MergeTags(auth, b.ID, c.ID, ab.Target.Version, 1)
	if err != nil {
		t.Fatal(err)
	}
	afterA, err := s.GetTag(auth, a.ID)
	if err != nil || afterA.MergedInto != c.ID || !afterA.Deleted {
		t.Fatal("merge chain not flattened", afterA, err)
	}
	if len(bc.Target.Aliases) != 2 {
		t.Fatal("source terms lost", bc.Target)
	}
	view, err := s.GetTrackTags(auth, track.ID)
	if err != nil || len(view.Personal) != 1 || view.Personal[0].ID != c.ID || view.PersonalVersion != 4 {
		t.Fatal(view, err)
	}
	current, _ := s.Track(track.ID)
	if current.Version != oldTrack.Version || s.Revision() != before {
		t.Fatal("private merge changed shared revision")
	}
	if _, err = s.MergeTags(auth, c.ID, a.ID, bc.Target.Version, afterA.Version); err == nil {
		t.Fatal("merge cycle accepted")
	}
	var audit string
	s.db.QueryRow("SELECT changes FROM audit ORDER BY id DESC LIMIT 1").Scan(&audit)
	if strings.Contains(audit, "private merge") {
		t.Fatal("private merge leaked")
	}
}

func TestTrackTagMergeRejectsCrossScopeGroupAndAliasOverflow(t *testing.T) {
	api, admin, _, aliceAuth, _, bobAuth := tagLinkFixture(t)
	s := api.store
	source := mustLinkTag(t, s, aliceAuth, "personal", "private merge secret")
	target := mustLinkTag(t, s, aliceAuth, "personal", "private merge target")
	if _, err := s.MergeTags(bobAuth, source.ID, target.ID, 999, 999); !errors.Is(err, errResourceHidden) {
		t.Fatal("foreign private merge leaked versions", err)
	}
	public := mustLinkTag(t, s, admin, "public", "public merge")
	if _, err := s.MergeTags(aliceAuth, public.ID, public.ID, 999, 999); !errors.Is(err, errTagForbidden) {
		t.Fatal("nonadmin public merge exposed versions", err)
	}
	if _, err := s.MergeTags(admin, public.ID, source.ID, 1, 1); !errors.Is(err, errResourceHidden) {
		t.Fatal("admin accessed foreign private scope", err)
	}
	personal := mustLinkTag(t, s, admin, "personal", "admin merge")
	if _, err := s.MergeTags(admin, public.ID, personal.ID, 1, 1); !errors.Is(err, errTagInvalid) {
		t.Fatal("cross scope merge accepted", err)
	}
	mood, err := s.CreateTag(admin, TagInput{GroupID: "mood", Name: "wrong group"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeTags(admin, public.ID, mood.ID, 1, 1); !errors.Is(err, errTagInvalid) {
		t.Fatal("cross group merge accepted", err)
	}
	aliases := []string{}
	for i := 0; i < 32; i++ {
		aliases = append(aliases, fmt.Sprintf("merge alias %d", i))
	}
	large, err := s.CreateTag(admin, TagInput{GroupID: "genre", Name: "large alias set", Aliases: aliases})
	if err != nil {
		t.Fatal(err)
	}
	audit := tagAuditCount(t, s)
	if _, err = s.MergeTags(admin, large.ID, public.ID, 1, 1); !errors.Is(err, errTagLimit) {
		t.Fatal("alias overflow silently dropped terms", err)
	}
	unchanged, err := s.GetTag(admin, large.ID)
	if err != nil || unchanged.Version != 1 || unchanged.Deleted || len(unchanged.Aliases) != 32 || tagAuditCount(t, s) != audit {
		t.Fatal("failed merge mutated namespace", unchanged, err)
	}
}

func TestTrackTagMergeRejectsMoreThanFiveThousandTracks(t *testing.T) {
	s, auth := tagTestStore(t)
	source := mustLinkTag(t, s, auth, "public", "bulk source")
	target := mustLinkTag(t, s, auth, "public", "bulk target")
	payload, _ := json.Marshal(s.Tracks()[0])
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		_, err := tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<5001) INSERT INTO tracks(id,path,content_hash,title,payload,version,removed,missing,owner_id,uploaded_by) SELECT printf('%08d-0000-4000-8000-000000000000',i),'bulk-'||i||'.mp3','','Bulk',?,1,0,0,?,? FROM n`, string(payload), auth.userID, auth.userID)
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO track_tags(track_id,tag_id,scope_id,source,created_by,created_at) SELECT id,?,'public','imported',?,'created' FROM tracks WHERE path LIKE 'bulk-%'", source.ID, auth.userID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeTags(auth, source.ID, target.ID, 1, 1); !errors.Is(err, errTagLimit) {
		t.Fatal("unbounded merge accepted", err)
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM track_tags WHERE tag_id=?", source.ID).Scan(&count)
	if count != 5001 {
		t.Fatal("failed merge changed links", count)
	}
}
