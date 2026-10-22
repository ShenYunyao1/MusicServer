package music

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestMultiuserPersonalPlaylistsAndOrder(t *testing.T) {
	api, _ := testAPI(t)
	alice, at := readyMember(t, api, "playlist-alice")
	bob, bt := readyMember(t, api, "playlist-bob")
	create := func(token, id string) Playlist {
		t.Helper()
		response := memberVersionRequest(api, token, http.MethodPost, "/api/v2/playlists", fmt.Sprintf(`{"id":%q,"name":"Same name","owner_id":%q}`, id, bob.ID), 0)
		if response.Code != 201 {
			t.Fatalf("create %s: %d %s", id, response.Code, response.Body.String())
		}
		var p Playlist
		json.Unmarshal(response.Body.Bytes(), &p)
		return p
	}
	a := create(at, "alice-first")
	a2 := create(at, "alice-second")
	b := create(bt, "bob-first")
	spoof := memberVersionRequest(api, at, "PUT", "/api/v2/playlists/"+a.ID, `{"name":"Same name","system":true,"track_ids":[]}`, a.Version)
	if spoof.Code != 200 {
		t.Fatal(spoof.Code, spoof.Body.String())
	}
	a, _ = api.store.Playlist(a.ID)
	if a.System {
		t.Fatal("client promoted private playlist to system")
	}
	if a.OwnerID != alice.ID || a2.SortOrder != 2 || b.OwnerID != bob.ID || b.SortOrder != 1 {
		t.Fatalf("owners/order: %+v %+v %+v", a, a2, b)
	}
	track := api.store.Tracks()[0]
	for _, entry := range []struct {
		token string
		p     Playlist
	}{{at, a}, {bt, b}} {
		r := memberVersionRequest(api, entry.token, "POST", "/api/v2/playlists/"+entry.p.ID+"/tracks", fmt.Sprintf(`{"track_ids":[%q]}`, track.ID), entry.p.Version)
		if r.Code != 200 {
			t.Fatalf("add: %d %s", r.Code, r.Body.String())
		}
	}
	a, _ = api.store.Playlist(a.ID)
	r := memberVersionRequest(api, at, "PATCH", "/api/v2/playlists/"+a.ID+"/tracks/"+track.ID, `{"note":"alice private"}`, a.Version)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	bBefore, _ := api.store.Playlist(b.ID)
	r = memberVersionRequest(api, at, "PUT", "/api/v2/playlists/order", `{"order":["alice-second","alice-first"],"expected_order":["alice-first","alice-second"]}`, 0)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	bAfter, _ := api.store.Playlist(b.ID)
	if bBefore.Version != bAfter.Version || bAfter.SortOrder != 1 || bAfter.TrackNotes[track.ID] != "" {
		t.Fatal("other user's playlist changed")
	}
	r = memberVersionRequest(api, at, "PUT", "/api/v2/playlists/order", `{"order":["alice-first","bob-first"],"expected_order":["alice-second","alice-first"]}`, 0)
	if r.Code != 409 {
		t.Fatal("foreign order", r.Code)
	}
	for _, token := range []string{at, bt} {
		r = memberVersionRequest(api, token, "GET", "/api/v2/playlists", "", 0)
		var list []Playlist
		json.Unmarshal(r.Body.Bytes(), &list)
		for _, p := range list {
			if !p.System && p.OwnerID != map[string]string{at: alice.ID, bt: bob.ID}[token] {
				t.Fatal("private list leak", p)
			}
		}
	}
	a, _ = api.store.Playlist(a.ID)
	r = memberVersionRequest(api, at, "DELETE", "/api/v2/playlists/"+a.ID, "", a.Version)
	if r.Code != 204 {
		t.Fatal("delete", r.Code, r.Body.String())
	}
}
