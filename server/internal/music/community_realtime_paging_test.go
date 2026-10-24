package music

import (
	"fmt"
	"slices"
	"testing"
)

func TestRealtimePaginationBoundaryAndSearchMembership(t *testing.T) {
	api, author, reader, p := communityFixture(t)
	token := communityLogin(t, api.store, reader.Username)
	var err error
	p, err = api.store.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: author.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("realtime-page-%02d", i)
		if err := api.store.PutPlaylist(Playlist{ID: id, Name: id}, MutationOptions{UserID: author.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := api.store.SetPlaylistVisibility(id, "public", MutationOptions{UserID: author.ID, ExpectedVersion: 1}); err != nil {
			t.Fatal(err)
		}
	}
	view := map[string]any{"catalog": true}
	before := realtimeTestSync(t, api, token, "", view)
	if len(before.Sections["catalog"].Order) != 30 || slices.Contains(before.Sections["catalog"].Order, p.ID) {
		t.Fatal("unexpected initial page")
	}
	p, err = api.store.SetPlaylistVisibility(p.ID, "private", MutationOptions{UserID: author.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	p, err = api.store.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: author.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	delta := realtimeTestSync(t, api, token, before.Cursor, view)
	section := delta.Sections["catalog"]
	if delta.Reset || len(section.Changed) != 1 || len(section.Removed) != 1 || len(section.Order) != 30 || section.Order[0] != p.ID || section.Meta["total"] != float64(31) {
		t.Fatalf("incorrect page boundary delta: %+v", section)
	}
	search := map[string]any{"catalog": true, "query": "Needle title"}
	empty := realtimeTestSync(t, api, token, "", search)
	name := "Needle title"
	p, err = api.store.UpdatePlaylistMetadata(p.ID, &name, nil, MutationOptions{UserID: author.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	matched := realtimeTestSync(t, api, token, empty.Cursor, search)
	if len(matched.Sections["catalog"].Changed) != 1 {
		t.Fatal("search match not inserted")
	}
	name = "No match"
	if _, err := api.store.UpdatePlaylistMetadata(p.ID, &name, nil, MutationOptions{UserID: author.ID, ExpectedVersion: p.Version}); err != nil {
		t.Fatal(err)
	}
	removed := realtimeTestSync(t, api, token, matched.Cursor, search)
	if len(removed.Sections["catalog"].Removed) != 1 || len(removed.Sections["catalog"].Order) != 0 {
		t.Fatal("old search match retained")
	}
}

func TestRealtimeCursorCacheBoundedAndEvictedReset(t *testing.T) {
	api, _ := testAPI(t)
	_, _, token := loginTestAccount(t, api.store, "admin")
	first := realtimeTestSync(t, api, token, "", map[string]any{})
	for i := 0; i < realtimeCursorLimit+2; i++ {
		realtimeTestSync(t, api, token, "", map[string]any{"query": fmt.Sprint(i)})
	}
	api.realtimeMu.Lock()
	count := len(api.realtimeCursors)
	api.realtimeMu.Unlock()
	if count > realtimeCursorLimit {
		t.Fatal("unbounded cursor cache")
	}
	if reply := realtimeTestSync(t, api, token, first.Cursor, map[string]any{}); !reply.Reset {
		t.Fatal("evicted cursor did not reset")
	}
}
