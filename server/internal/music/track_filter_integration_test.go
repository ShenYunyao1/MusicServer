package music

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func filterIntegrationFixture(t *testing.T) (*API, authContext, []string, Tag, Tag) {
	t.Helper()
	a, _ := tagHTTPAPI(t)
	s := a.store
	auth := tagTestAuth(t, s, "admin")
	base := s.Tracks()[0]
	ids := []string{base.ID, uuid.NewString(), uuid.NewString()}
	if err := s.transaction(func(tx *sql.Tx) error {
		for i, title := range []string{"Alpha", "Beta", "Gamma"} {
			track := base
			track.ID = ids[i]
			track.Title = title
			track.Artist = "Artist " + title
			track.Album = "Album"
			track.DurationMS = int64((i + 1) * 1000)
			track.Path = title + ".mp3"
			track.Version++
			if err := writeTrack(tx, track); err != nil {
				return err
			}
			if err := recordChange(tx, "track", track.ID, false); err != nil {
				return err
			}
		}
		return refreshLibrary(tx)
	}); err != nil {
		t.Fatal(err)
	}
	rock := mustLinkTag(t, s, auth, "public", "Rock")
	jazz := mustLinkTag(t, s, auth, "public", "Jazz")
	personal := mustLinkTag(t, s, auth, "personal", "My music")
	for i, id := range ids {
		public := rock
		if i == 0 {
			public = jazz
		}
		if _, err := s.ChangeTrackTags(auth, id, "public", 1, []string{public.ID}, nil); err != nil {
			t.Fatal(err)
		}
		if i != 1 {
			if _, err := s.ChangeTrackTags(auth, id, "personal", 1, []string{personal.ID}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	return a, auth, ids, rock, personal
}

func TestTrackFilterBatchFreezesCombinedResultAndBaseline(t *testing.T) {
	a, auth, ids, rock, personal := filterIntegrationFixture(t)
	s := a.store
	filter := TrackFilter{Version: 1, Tags: []TrackTagClause{{Scope: "public", GroupID: "genre", IncludeIDs: []string{rock.ID}}, {Scope: "personal", GroupID: "genre", IncludeIDs: []string{personal.ID}}}}
	page, err := s.QueryTrackFilter(auth, TrackFilterQuery{Filter: filter})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	label := mustLinkTag(t, s, auth, "personal", "Reviewed")
	page, err = s.QueryTrackFilter(auth, TrackFilterQuery{Filter: filter})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", Query: &TagBatchQuery{Conditions: &filter, Baseline: &page.Baseline}, AddIDs: []string{label.ID}})
	if err != nil || job.Total != 1 || job.Items[0].TrackID != ids[2] {
		t.Fatal(job, err)
	}
	if _, err := s.ChangeTrackTags(auth, ids[1], "personal", 1, []string{personal.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewTagBatch(auth, TagBatchInput{Scope: "personal", Query: &TagBatchQuery{Conditions: &filter, Baseline: &page.Baseline}, AddIDs: []string{label.ID}}); !errors.Is(err, errFilterSnapshot) {
		t.Fatal("stale filtered snapshot accepted", err)
	}
	job, err = s.StartTagBatch(auth, job.ID, job.Version)
	if err != nil || job.Completed != 1 {
		t.Fatal(job, err)
	}
	view, err := s.GetTrackTags(auth, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range view.Personal {
		if tag.ID == label.ID {
			t.Fatal("later matching track joined frozen task")
		}
	}
}

func TestTrackFilterLiveBrowser(t *testing.T) {
	if os.Getenv("MUSIC_SERVER_FILTER_UI") != "1" {
		t.Skip("live filter browser acceptance was not requested")
	}
	a, _, _, _, _ := filterIntegrationFixture(t)
	server := httptest.NewServer(a)
	defer server.Close()
	script, err := filepath.Abs("../../tests/track-filter-live-ui.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(), "MUSIC_SERVER_FILTER_LIVE_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live filter browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}
