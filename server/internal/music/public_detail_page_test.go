package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func publicDetailFixture(t *testing.T) (*API, User, string, []string) {
	t.Helper()
	a, _ := testAPI(t)
	owner, _ := readyMember(t, a, "public-page-owner")
	_, token := readyMember(t, a, "public-page-reader")
	ids := browseSeedTracks(t, a, owner.ID, owner.ID, 123)
	if err := a.store.PutPlaylist(Playlist{ID: "public-page", Name: "Paged public", TrackIDs: ids, TrackNotes: map[string]string{ids[0]: "author-secret-note"}}, MutationOptions{UserID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	p, _ := a.store.Playlist("public-page")
	if _, err := a.store.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: owner.ID, ExpectedVersion: p.Version}); err != nil {
		t.Fatal(err)
	}
	return a, owner, token, ids
}

func TestPublicDetailIsBoundedSearchableAndRealtimeUsesCurrentPage(t *testing.T) {
	a, _, token, ids := publicDetailFixture(t)
	p, _ := a.store.Playlist("public-page")
	for _, offset := range []int{0, 50, 100} {
		w := sessionRequest(a, token, "GET", fmt.Sprintf("/api/v2/public/playlists/%s?offset=%d&limit=50", p.ID, offset), "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		var page struct {
			Playlist             Playlist         `json:"playlist"`
			Tracks               []map[string]any `json:"tracks"`
			Total, Offset, Limit int
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if page.Total != 123 || page.Offset != offset || page.Limit != 50 || len(page.Tracks) != min(50, 123-offset) || page.Tracks[0]["id"] != ids[offset] {
			t.Fatal("wrong page", page.Total, page.Offset, page.Limit, len(page.Tracks))
		}
		if len(page.Playlist.TrackIDs) != len(page.Tracks) || len(page.Playlist.TrackNotes) != 0 {
			t.Fatal("unbounded or private membership metadata")
		}
		if strings.Contains(w.Body.String(), "full private lyrics") || strings.Contains(w.Body.String(), "author-secret-note") || w.Body.Len() > 100000 {
			t.Fatal("oversized or private public projection", w.Body.Len())
		}
		if page.Tracks[0]["detail_loaded"] != false {
			t.Fatal("summary does not request full detail lazily")
		}
	}
	for search, want := range map[string]int{"Page 080": 1, "author-secret-note": 0} {
		w := sessionRequest(a, token, "GET", "/api/v2/public/playlists/"+p.ID+"?q="+url.QueryEscape(search), "")
		var page struct{ Total int }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != want {
			t.Fatal("search", search, w.Code, w.Body)
		}
	}
	view := map[string]any{"public_detail": p.ID, "public_offset": 50, "public_limit": 50}
	first := realtimeTestSync(t, a, token, "", view)
	if len(first.Sections["detail_tracks"].Changed) != 50 || first.Sections["detail_tracks"].Meta["total"] != float64(123) {
		t.Fatal("unbounded realtime page", len(first.Sections["detail_tracks"].Changed))
	}
	if err := a.store.transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE tracks SET title='Off page',payload=json_set(payload,'$.title','Off page') WHERE id=?", ids[0])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	next := realtimeTestSync(t, a, token, first.Cursor, view)
	if next.Reset || len(next.Sections["detail_tracks"].Changed) != 0 {
		t.Fatal("off-page change downloaded visible tracks")
	}
}

func TestPublicDetailLiveBrowser(t *testing.T) {
	if os.Getenv("MUSIC_SERVER_PUBLIC_DETAIL_UI") != "1" {
		t.Skip("enable isolated public detail browser acceptance")
	}
	a, _, _, _ := publicDetailFixture(t)
	server := httptest.NewServer(a)
	defer server.Close()
	script, err := filepath.Abs("../../tests/public-detail-live-ui.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(), "MUSIC_SERVER_PUBLIC_DETAIL_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("public detail browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}
