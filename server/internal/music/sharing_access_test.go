package music

import (
	"encoding/json"
	"testing"
)

func TestSharingAcceptedInvitationScopesAndRecovery(t *testing.T) {
	api, a, b, p := sharingFixture(t)
	s := api.store
	at, bt := communityLogin(t, s, a.Username), communityLogin(t, s, b.Username)
	p, err := s.SavePlaylistEditor(p.ID, p.Name, "shared metadata", tinyPNG(), false, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if r := sessionRequest(api, bt, "GET", "/api/v2/playlists/"+p.ID, ""); r.Code != 404 {
		t.Fatal("pending invitation exposed playlist")
	}
	i, err = s.InvitationAction(i.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	r := sessionRequest(api, bt, "GET", "/api/v2/playlists/"+p.ID, "")
	if r.Code != 200 {
		t.Fatalf("accepted share denied: %d %s", r.Code, r.Body)
	}
	var detail struct {
		Playlist Playlist `json:"playlist"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &detail)
	if !detail.Playlist.ReadOnly || len(detail.Playlist.TrackNotes) != 0 {
		t.Fatal("share leaked author note or write access")
	}
	key := communityKey(t, api, bt, p.ID)
	check := func(wanted int) {
		t.Helper()
		for _, path := range []string{"playlists/" + p.ID, "media/" + p.TrackIDs[0], "covers/" + p.CoverID} {
			r := communityPlayer(api, key, path)
			if r.Code != wanted {
				t.Fatalf("%s: %d wanted %d", path, r.Code, wanted)
			}
		}
	}
	check(200)
	if r := memberVersionRequest(api, bt, "PATCH", "/api/v2/playlists/"+p.ID, `{"name":"unauthorized"}`, p.Version); r.Code != 404 {
		t.Fatal("recipient edited source")
	}
	var first SyncResponse
	_ = json.Unmarshal(communityPlayer(api, key, "sync?limit=500").Body.Bytes(), &first)
	i, err = s.InvitationAction(i.ID, "revoke", MutationOptions{UserID: a.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	check(404)
	var revoked SyncResponse
	_ = json.Unmarshal(communityPlayer(api, key, "sync?cursor="+first.Cursor).Body.Bytes(), &revoked)
	if !revoked.Reset || len(revoked.Playlists) != 1 || revoked.Playlists[0].SubscriptionReason != "share_revoked" || len(revoked.Playlists[0].TrackIDs) != 0 {
		t.Fatalf("unsafe revoked reset %+v", revoked)
	}
	i, err = s.InvitationAction(i.ID, "resend", MutationOptions{UserID: a.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	check(404)
	i, err = s.InvitationAction(i.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	check(200)
	p, err = s.SetPlaylistVisibility(p.ID, "private", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	check(404)
	p, err = s.SetPlaylistVisibility(p.ID, "shared", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	check(200)
	if r := sessionRequest(api, at, "GET", "/api/v2/public/playlists/"+p.ID, ""); r.Code != 404 {
		t.Fatal("shared source entered public endpoint")
	}
	if err = s.Unsubscribe(p.ID, MutationOptions{UserID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Subscribe(p.ID, MutationOptions{UserID: b.ID}); err != nil {
		t.Fatal("accepted recipient could not resubscribe", err)
	}
	if r := communityPlayer(api, key, "playlists"); r.Code != 200 || r.Body.String() != "[]\n" {
		t.Fatal("resubscribe silently re-granted key", r.Body)
	}
}

func TestSharingPublicSubscribersNeedOwnInvitation(t *testing.T) {
	api, a, b, p := communityFixture(t)
	s := api.store
	p, err := s.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Subscribe(p.ID, MutationOptions{UserID: b.ID}); err != nil {
		t.Fatal(err)
	}
	key := communityKey(t, api, communityLogin(t, s, b.Username), p.ID)
	p, err = s.SetPlaylistVisibility(p.ID, "shared", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if r := communityPlayer(api, key, "playlists/"+p.ID); r.Code != 404 {
		t.Fatal("public subscriber inherited shared access")
	}
	view, err := s.Subscriptions(b.ID)
	if err != nil || len(view) != 1 || view[0].SubscriptionReason != "share_revoked" {
		t.Fatal("missing minimal shared invalid state", err)
	}
}

func TestSharingOwnerDisableTakeoverAndDelete(t *testing.T) {
	api, a, b, p := sharingFixture(t)
	s := api.store
	i, err := s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	i, err = s.InvitationAction(i.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	key := communityKey(t, api, communityLogin(t, s, b.Username), p.ID)
	users, _ := s.Users()
	var admin User
	for _, u := range users {
		if u.Role == "admin" {
			admin = u
		}
	}
	off, on := false, true
	disabled, err := s.UpdateUser(a.ID, AccountPatch{Enabled: &off}, MutationOptions{UserID: admin.ID, ExpectedVersion: a.Version})
	if err != nil {
		t.Fatal(err)
	}
	if r := communityPlayer(api, key, "media/"+p.TrackIDs[0]); r.Code != 404 {
		t.Fatal("disabled publisher still shared")
	}
	views, err := s.Subscriptions(b.ID)
	if err != nil || views[0].SubscriptionReason != "owner_disabled" {
		t.Fatal("missing disabled reason")
	}
	enabled, err := s.UpdateUser(a.ID, AccountPatch{Enabled: &on}, MutationOptions{UserID: admin.ID, ExpectedVersion: disabled.Version})
	if err != nil {
		t.Fatal(err)
	}
	if r := communityPlayer(api, key, "media/"+p.TrackIDs[0]); r.Code != 200 {
		t.Fatal("same key did not resume publisher enable")
	}
	disabled, err = s.UpdateUser(a.ID, AccountPatch{Enabled: &off}, MutationOptions{UserID: admin.ID, ExpectedVersion: enabled.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.TransferResources(a.ID, TransferRequest{TargetID: admin.ID, TargetVersion: admin.Version, Playlists: []ResourceVersion{{ID: p.ID, Version: p.Version}}}, MutationOptions{UserID: admin.ID, ExpectedVersion: disabled.Version}); err != nil {
		t.Fatal(err)
	}
	i, err = readInvitation(s.db, i.ID)
	if err != nil || i.State != "revoked" {
		t.Fatal("takeover did not revoke old invitation")
	}
	p, _ = s.Playlist(p.ID)
	p, err = s.SetPlaylistVisibility(p.ID, "shared", MutationOptions{UserID: admin.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if r := communityPlayer(api, key, "media/"+p.TrackIDs[0]); r.Code != 404 {
		t.Fatal("old invitation survived takeover")
	}
	i, err = s.InvitationAction(i.ID, "resend", MutationOptions{UserID: admin.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	if i.InviterID != admin.ID {
		t.Fatal("resend did not bind new owner")
	}
	i, err = s.InvitationAction(i.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: i.Version})
	if err != nil {
		t.Fatal(err)
	}
	if r := communityPlayer(api, key, "media/"+p.TrackIDs[0]); r.Code != 200 {
		t.Fatal("new owner's explicit invitation did not restore key")
	}
	if err = s.DeletePlaylist(p.ID, MutationOptions{UserID: admin.ID, ExpectedVersion: p.Version}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM playlist_invitations WHERE id=?", i.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("source deletion left invitation")
	}
	views, err = s.Subscriptions(b.ID)
	if err != nil || views[0].SubscriptionReason != "deleted" || len(views[0].TrackIDs) != 0 {
		t.Fatal("source deletion lost minimal invalid subscription")
	}
}
