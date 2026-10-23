package music

import (
	"encoding/json"
	"testing"
)

func communityFixture(t *testing.T) (*API, User, User, Playlist) {
	t.Helper()
	api, _ := testAPI(t)
	a, _ := readyMember(t, api, "public-author")
	b, _ := readyMember(t, api, "public-reader")
	track := api.store.Tracks()[0]
	if err := api.store.PutPlaylist(Playlist{ID: "community-list", Name: "Published mix", TrackIDs: []string{track.ID}, TrackNotes: map[string]string{track.ID: "author private note"}}, MutationOptions{UserID: a.ID}); err != nil {
		t.Fatal(err)
	}
	p, _ := api.store.Playlist("community-list")
	return api, a, b, p
}
func communityRevision(t *testing.T, s *Store) string {
	t.Helper()
	var value string
	if err := s.db.QueryRow("SELECT value FROM meta WHERE key='public_revision'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestCommunitySubscriptionLifecycle(t *testing.T) {
	api, a, b, p := communityFixture(t)
	s := api.store
	if p.Visibility != "private" {
		t.Fatal("new playlist must be private")
	}
	if _, err := s.Subscribe(p.ID, MutationOptions{UserID: b.ID}); err == nil {
		t.Fatal("subscribed to private playlist")
	}
	p, err := s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	revision := communityRevision(t, s)
	if _, err := s.Subscribe(p.ID, MutationOptions{UserID: a.ID}); err == nil {
		t.Fatal("self-subscription allowed")
	}
	subscribed, err := s.Subscribe(p.ID, MutationOptions{UserID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !subscribed.ReadOnly || subscribed.SubscriptionStatus != "active" || len(subscribed.TrackNotes) != 0 {
		t.Fatalf("unsafe subscription: %+v", subscribed)
	}
	duplicate, err := s.Subscribe(p.ID, MutationOptions{UserID: b.ID})
	if err != nil || duplicate.SubscriptionVersion != subscribed.SubscriptionVersion {
		t.Fatal("duplicate subscription changed state")
	}
	note, err := s.UpdateSubscriptionNote(p.ID, p.TrackIDs[0], "reader private note", MutationOptions{UserID: b.ID, ExpectedVersion: subscribed.SubscriptionVersion})
	if err != nil {
		t.Fatal(err)
	}
	if note.TrackNotes[p.TrackIDs[0]] != "reader private note" {
		t.Fatal("reader note missing")
	}
	original, _ := s.Playlist(p.ID)
	if original.TrackNotes[p.TrackIDs[0]] != "author private note" {
		t.Fatal("reader overwrote author note")
	}
	if communityRevision(t, s) != revision {
		t.Fatal("personal actions refreshed public directory")
	}
	p, err = s.SetPlaylistVisibility(p.ID, "private", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	views, err := s.Subscriptions(b.ID)
	if err != nil || len(views) != 1 || views[0].SubscriptionReason != "private" || len(views[0].TrackIDs) != 0 || len(views[0].TrackNotes) != 0 {
		t.Fatalf("private projection: %+v %v", views, err)
	}
	if _, err := s.UpdateSubscriptionNote(p.ID, p.TrackIDs[0], "forbidden", MutationOptions{UserID: b.ID, ExpectedVersion: note.SubscriptionVersion}); err == nil {
		t.Fatal("edited inactive subscription")
	}
	p, err = s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	views, _ = s.Subscriptions(b.ID)
	if views[0].SubscriptionStatus != "active" || views[0].TrackNotes[p.TrackIDs[0]] != "reader private note" {
		t.Fatal("subscription did not resume")
	}
	if err := s.DeletePlaylist(p.ID, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version}); err != nil {
		t.Fatal(err)
	}
	views, _ = s.Subscriptions(b.ID)
	if len(views) != 1 || views[0].SubscriptionReason != "deleted" {
		t.Fatalf("lost deleted subscription: %+v", views)
	}
	if err := s.PutPlaylist(Playlist{ID: p.ID, Name: "Reused ID"}, MutationOptions{UserID: a.ID}); err == nil {
		t.Fatal("deleted playlist identity reused")
	}
	if err := s.Unsubscribe(p.ID, MutationOptions{UserID: b.ID}); err != nil {
		t.Fatal(err)
	}
	views, _ = s.Subscriptions(b.ID)
	if len(views) != 0 {
		t.Fatal("unsubscribe left a record")
	}
}
func TestCommunityTransactionAndVisibilityProtection(t *testing.T) {
	api, a, b, p := communityFixture(t)
	s := api.store
	if _, err := s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: b.ID, ExpectedVersion: p.Version}); err == nil {
		t.Fatal("reader published another user's playlist")
	}
	if _, err := s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version + 1}); err == nil {
		t.Fatal("stale visibility applied")
	}
	p, err := s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	before := communityRevision(t, s)
	_, _ = s.db.Exec("CREATE TRIGGER reject_public BEFORE INSERT ON audit WHEN NEW.action='playlist.visibility' BEGIN SELECT RAISE(ABORT,'audit fixture'); END")
	if _, err := s.SetPlaylistVisibility(p.ID, "private", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version}); err == nil {
		t.Fatal("audit failure ignored")
	}
	after, _ := s.Playlist(p.ID)
	if after.Visibility != "public" || communityRevision(t, s) != before {
		t.Fatal("visibility rollback incomplete")
	}
	_, _ = s.db.Exec("DROP TRIGGER reject_public")
	_, _ = s.db.Exec("CREATE TRIGGER reject_subscription BEFORE INSERT ON audit WHEN NEW.action='subscription.create' BEGIN SELECT RAISE(ABORT,'audit fixture'); END")
	if _, err := s.Subscribe(p.ID, MutationOptions{UserID: b.ID}); err == nil {
		t.Fatal("subscription ignored audit failure")
	}
	views, _ := s.Subscriptions(b.ID)
	if len(views) != 0 {
		raw, _ := json.Marshal(views)
		t.Fatalf("partial subscription: %s", raw)
	}
}

func TestCommunityEditorVisibilityIsAtomic(t *testing.T) {
	api, a, _, p := communityFixture(t)
	public := "public"
	saved, err := api.store.SavePlaylistEditor(p.ID, "Published title", "Public description", tinyPNG(), false, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version, PlaylistVisibility: &public})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Visibility != "public" || saved.Name != "Published title" || saved.CoverID == "" || saved.Version != p.Version+1 {
		t.Fatal("editor did not publish atomically")
	}
	invalid := "unlisted"
	if _, err := api.store.SavePlaylistEditor(p.ID, "Must not apply", "", nil, false, MutationOptions{UserID: a.ID, ExpectedVersion: saved.Version, PlaylistVisibility: &invalid}); err == nil {
		t.Fatal("unimplemented visibility accepted")
	}
	after, _ := api.store.Playlist(p.ID)
	if after.Name != saved.Name || after.Version != saved.Version {
		t.Fatal("invalid visibility partially applied")
	}
}
