package music

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestTrackTagSummaryBoundedAccountProjection(t *testing.T) {
	s, auth, ids, tags := filterFixture(t)
	readyMember(t, &API{store: s}, "summary-member")
	other := tagMemberAuth(t, s, "summary-member")
	secret := mustLinkTag(t, s, other, "personal", "Never disclose")
	if _, err := s.ChangeTrackTags(other, ids[0], "personal", 1, []string{secret.ID}, nil); err != nil {
		t.Fatal(err)
	}
	additional := mustLinkTag(t, s, auth, "personal", "Reviewed")
	view, _ := s.GetTrackTags(auth, ids[0])
	if _, err := s.ChangeTrackTags(auth, ids[0], "personal", view.PersonalVersion, []string{additional.ID}, nil); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: TrackFilter{Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		summary, ok := item["tag_summary"].(TrackTagSummary)
		if !ok {
			t.Fatalf("missing typed summary: %#v", item)
		}
		if item["id"] == ids[0] && (summary.Total != 4 || len(summary.Items) != 3) {
			t.Fatalf("unbounded or incorrect summary: %+v", summary)
		}
		if item["id"] == ids[3] && (summary.Total != 0 || len(summary.Items) != 0) {
			t.Fatal(summary)
		}
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), secret.ID) || strings.Contains(string(encoded), secret.Name) {
		t.Fatal("another account's tags leaked")
	}
	page, err = s.QueryTrackFilter(other, TrackFilterQuery{Filter: TrackFilter{Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(page)
	if strings.Contains(string(encoded), tags["night"].ID) || strings.Contains(string(encoded), additional.ID) {
		t.Fatal("administrator's personal tags leaked to member")
	}
	// Rename is projected from the dictionary, without relying on track payload caches.
	name := "Renamed public"
	if _, err := s.UpdateTag(auth, tags["rock"].ID, TagPatch{Name: &name}, tags["rock"].Version); err != nil {
		t.Fatal(err)
	}
	page, err = s.QueryTrackFilter(auth, TrackFilterQuery{Filter: TrackFilter{Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(page)
	if !strings.Contains(string(encoded), name) {
		t.Fatal("stale dictionary label")
	}
}

func TestTrackTagSummaryIgnoresSuppressedSource(t *testing.T) {
	s, auth, ids, tags := filterFixture(t)
	// Imported relations can coexist with an explicit suppression override.
	// Such a relation must not appear in either the visible list or its count.
	if err := s.transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE track_tag_overrides SET action='suppressed' WHERE track_id=? AND tag_id=? AND scope_id='public'`, ids[0], tags["rock"].ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: TrackFilter{Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item["id"] != ids[0] {
			continue
		}
		summary := item["tag_summary"].(TrackTagSummary)
		if summary.Total != 2 {
			t.Fatal(summary)
		}
		for _, label := range summary.Items {
			if label.ID == tags["rock"].ID {
				t.Fatal("suppressed label projected")
			}
		}
	}
}
