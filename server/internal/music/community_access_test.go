package music

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func communityLogin(t *testing.T, s *Store, name string) string {
	t.Helper()
	_, _, token, err := s.Login(name, "personal password for "+name, "community-test")
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func communityKey(t *testing.T, api *API, token, id string) string {
	t.Helper()
	r := sessionRequest(api, token, "POST", "/api/v2/auth/keys", `{"name":"Subscribed player","allowed_playlist_ids":["`+id+`"]}`)
	if r.Code != 201 {
		t.Fatalf("key: %d %s", r.Code, r.Body)
	}
	var v APIKeyView
	_ = json.Unmarshal(r.Body.Bytes(), &v)
	return v.Secret
}
func communityPlayer(api *API, key, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://example.com/api/v2/"+path, nil)
	r.Header.Set("X-API-Key", key)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}
func TestCommunityPlayerAccessTracksVisibilityAndNotes(t *testing.T) {
	api, a, b, p := communityFixture(t)
	s := api.store
	at := communityLogin(t, s, a.Username)
	bt := communityLogin(t, s, b.Username)
	p, err := s.SavePlaylistEditor(p.ID, p.Name, "Description", tinyPNG(), false, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	public := sessionRequest(api, bt, "GET", "/api/v2/playlists/"+p.ID, "")
	if public.Code != 200 {
		t.Fatalf("public read: %d %s", public.Code, public.Body)
	}
	var detail struct {
		Playlist Playlist `json:"playlist"`
	}
	_ = json.Unmarshal(public.Body.Bytes(), &detail)
	if len(detail.Playlist.TrackNotes) != 0 || !detail.Playlist.ReadOnly {
		t.Fatal("public projection exposed author notes or write access")
	}
	if denied := sessionRequest(api, bt, "POST", "/api/v2/auth/keys", `{"name":"not subscribed","allowed_playlist_ids":["`+p.ID+`"]}`); denied.Code != 400 {
		t.Fatal("key granted unsubscribed public playlist")
	}
	own := sessionRequest(api, at, "GET", "/api/v2/playlists/"+p.ID, "")
	_ = json.Unmarshal(own.Body.Bytes(), &detail)
	if detail.Playlist.TrackNotes[p.TrackIDs[0]] != "author private note" {
		t.Fatal("author lost private notes")
	}
	sub, err := s.Subscribe(p.ID, MutationOptions{UserID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateSubscriptionNote(p.ID, p.TrackIDs[0], "reader private note", MutationOptions{UserID: b.ID, ExpectedVersion: sub.SubscriptionVersion})
	if err != nil {
		t.Fatal(err)
	}
	key := communityKey(t, api, bt, p.ID)
	for _, path := range []string{"playlists/" + p.ID, "media/" + p.TrackIDs[0], "covers/" + p.CoverID} {
		r := communityPlayer(api, key, path)
		if r.Code != 200 {
			t.Fatalf("active %s: %d %s", path, r.Code, r.Body)
		}
	}
	first := communityPlayer(api, key, "sync?limit=500")
	var snapshot SyncResponse
	_ = json.Unmarshal(first.Body.Bytes(), &snapshot)
	if len(snapshot.Playlists) != 1 || snapshot.Playlists[0].TrackNotes[p.TrackIDs[0]] != "reader private note" {
		t.Fatalf("subscriber sync: %s", first.Body)
	}
	p, err = s.SetPlaylistVisibility(p.ID, "private", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"playlists/" + p.ID, "media/" + p.TrackIDs[0], "covers/" + p.CoverID} {
		r := communityPlayer(api, key, path)
		if r.Code != 404 {
			t.Fatalf("inactive %s: %d %s", path, r.Code, r.Body)
		}
	}
	name := "Private unpublished name"
	p, err = s.UpdatePlaylistMetadata(p.ID, &name, nil, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	next := communityPlayer(api, key, "sync?limit=500&cursor="+snapshot.Cursor)
	var invalid SyncResponse
	_ = json.Unmarshal(next.Body.Bytes(), &invalid)
	if next.Code != 200 || !invalid.Reset || len(invalid.Playlists) != 1 || invalid.Playlists[0].SubscriptionReason != "private" || invalid.Playlists[0].Name == name || len(invalid.Changed) != 0 {
		t.Fatalf("lost revocation/reset: %d %s", next.Code, next.Body)
	}
	keys := s.APIKeys()
	if len(keys) != 1 || len(keys[0].AllowedPlaylistIDs) != 1 {
		t.Fatal("private transition removed configured grant")
	}
	p, err = s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if r := communityPlayer(api, key, "media/"+p.TrackIDs[0]); r.Code != 200 {
		t.Fatal("same key did not resume")
	}
	if err := s.DeletePlaylist(p.ID, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version}); err != nil {
		t.Fatal(err)
	}
	deleted := communityPlayer(api, key, "sync?limit=500")
	var gone SyncResponse
	_ = json.Unmarshal(deleted.Body.Bytes(), &gone)
	if len(gone.Playlists) != 1 || gone.Playlists[0].SubscriptionReason != "deleted" {
		t.Fatalf("deleted subscription disappeared: %s", deleted.Body)
	}
	if err := s.Unsubscribe(p.ID, MutationOptions{UserID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if len(s.APIKeys()[0].AllowedPlaylistIDs) != 0 {
		t.Fatal("unsubscribe retained key grant")
	}
}
func TestCommunitySubscriberReadOnlyAndOwnerDisable(t *testing.T) {
	api, a, b, p := communityFixture(t)
	s := api.store
	bt := communityLogin(t, s, b.Username)
	p, _ = s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	_, _ = s.Subscribe(p.ID, MutationOptions{UserID: b.ID})
	key := communityKey(t, api, bt, p.ID)
	for _, request := range []struct{ method, path, body string }{{"PATCH", "/api/v2/playlists/" + p.ID, `{"name":"hijack"}`}, {"DELETE", "/api/v2/playlists/" + p.ID, ""}, {"POST", "/api/v2/playlists/" + p.ID + "/tracks", `{"track_ids":[]}`}, {"PATCH", "/api/v2/playlists/" + p.ID + "/tracks/" + p.TrackIDs[0], `{"note":"overwrite"}`}} {
		r := memberVersionRequest(api, bt, request.method, request.path, request.body, p.Version)
		if r.Code != 404 {
			t.Fatalf("read-only write: %d %s", r.Code, r.Body)
		}
	}
	users, _ := s.Users()
	admin := ""
	for _, u := range users {
		if u.Role == "admin" {
			admin = u.ID
		}
	}
	off := false
	disabled, err := s.UpdateUser(a.ID, AccountPatch{Enabled: &off}, MutationOptions{UserID: admin, ExpectedVersion: a.Version})
	if err != nil {
		t.Fatal(err)
	}
	views, _ := s.Subscriptions(b.ID)
	if views[0].SubscriptionReason != "owner_disabled" || communityPlayer(api, key, "media/"+p.TrackIDs[0]).Code != 404 {
		t.Fatal("disabled author still serves subscription")
	}
	on := true
	if _, err := s.UpdateUser(a.ID, AccountPatch{Enabled: &on}, MutationOptions{UserID: admin, ExpectedVersion: disabled.Version}); err != nil {
		t.Fatal(err)
	}
	if communityPlayer(api, key, "media/"+p.TrackIDs[0]).Code != 200 {
		t.Fatal("enabled author did not restore subscription")
	}
	current, _ := readUser(s.db, a.ID)
	if _, err := s.ResetMemberPassword(a.ID, false, MutationOptions{UserID: admin, ExpectedVersion: current.Version}); err != nil {
		t.Fatal(err)
	}
	if communityPlayer(api, key, "media/"+p.TrackIDs[0]).Code != 200 {
		t.Fatal("author password reset unexpectedly withdrew publication")
	}
}

func TestCommunityTransferAndIndependentGrant(t *testing.T) {
	api, a, b, p := communityFixture(t)
	s := api.store
	bt := communityLogin(t, s, b.Username)
	p, _ = s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	_, _ = s.Subscribe(p.ID, MutationOptions{UserID: b.ID})
	key := communityKey(t, api, bt, p.ID)
	extra := sessionRequest(api, bt, "POST", "/api/v2/auth/keys", `{"name":"Independent global access","allowed_playlist_ids":["library","community-list"]}`)
	if extra.Code != 201 {
		t.Fatal(extra.Body.String())
	}
	var global APIKeyView
	_ = json.Unmarshal(extra.Body.Bytes(), &global)
	users, _ := s.Users()
	var admin User
	for _, u := range users {
		if u.Role == "admin" {
			admin = u
		}
	}
	off := false
	disabled, err := s.UpdateUser(a.ID, AccountPatch{Enabled: &off}, MutationOptions{UserID: admin.ID, ExpectedVersion: a.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TransferResources(a.ID, TransferRequest{TargetID: admin.ID, TargetVersion: admin.Version, Playlists: []ResourceVersion{{ID: p.ID, Version: p.Version}}}, MutationOptions{UserID: admin.ID, ExpectedVersion: disabled.Version}); err != nil {
		t.Fatal(err)
	}
	taken, _ := s.Playlist(p.ID)
	if taken.OwnerID != admin.ID || taken.Visibility != "private" {
		t.Fatal("takeover left playlist public")
	}
	if communityPlayer(api, key, "media/"+p.TrackIDs[0]).Code != 404 {
		t.Fatal("subscribed key followed private takeover")
	}
	if communityPlayer(api, global.Secret, "media/"+p.TrackIDs[0]).Code != 200 {
		t.Fatal("revocation removed independently granted global access")
	}
	if _, err := s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: admin.ID, ExpectedVersion: taken.Version}); err != nil {
		t.Fatal(err)
	}
	if communityPlayer(api, key, "media/"+p.TrackIDs[0]).Code != 200 {
		t.Fatal("takeover removed subscriber's suspended grant")
	}
}
