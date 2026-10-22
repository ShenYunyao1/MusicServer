package music

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestMultiuserOwnKeysAndScopedStats(t *testing.T) {
	api, _ := testAPI(t)
	a, at := readyMember(t, api, "keys-a")
	b, bt := readyMember(t, api, "keys-b")
	api.store.PutPlaylist(Playlist{ID: "keys-private-b", Name: "B secret"}, MutationOptions{UserID: b.ID})
	create := func(token, body string) APIKeyView {
		t.Helper()
		r := memberVersionRequest(api, token, "POST", "/api/v2/auth/keys", body, 0)
		if r.Code != 201 {
			t.Fatal("create key", r.Code, r.Body.String())
		}
		var key APIKeyView
		json.Unmarshal(r.Body.Bytes(), &key)
		return key
	}
	empty := create(at, `{"name":"empty"}`)
	if r := callAPIWithKey(api, "GET", "/api/v2/tracks", "", empty.Secret); r.Code != 200 || r.Body.String() != "[]\n" {
		t.Fatal("implicit library grant", r.Code, r.Body.String())
	}
	key := create(at, `{"name":"all","allowed_playlist_ids":["library"]}`)
	if r := callAPIWithKey(api, "GET", "/api/v2/tracks", "", key.Secret); r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(api.store.Tracks()[0].ID)) {
		t.Fatal("explicit library denied", r.Code, r.Body.String())
	}
	if r := memberVersionRequest(api, at, "POST", "/api/v2/auth/keys", `{"name":"bad","allowed_playlist_ids":["keys-private-b"]}`, 0); r.Code != 400 {
		t.Fatal("foreign grant", r.Code)
	}
	if r := memberVersionRequest(api, bt, "PATCH", "/api/v2/auth/keys/"+key.ID, `{"name":"steal"}`, 0); r.Code != 404 {
		t.Fatal("foreign key", r.Code)
	}
	bk := create(bt, `{"name":"bob"}`)
	for _, entry := range []struct{ token, own, foreign string }{{at, key.ID, bk.ID}, {bt, bk.ID, key.ID}} {
		r := memberVersionRequest(api, entry.token, "GET", "/api/v2/auth/keys/stats", "", 0)
		if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(entry.own)) || bytes.Contains(r.Body.Bytes(), []byte(entry.foreign)) {
			t.Fatal("stats leak", r.Code, r.Body.String())
		}
		r = memberVersionRequest(api, entry.token, "GET", "/api/v2/auth/keys", "", 0)
		if bytes.Contains(r.Body.Bytes(), []byte(entry.foreign)) {
			t.Fatal("key list leak")
		}
	}
	r := callAPI(api, "GET", "/api/v2/admin/stats?user_id="+a.ID, "")
	if r.Code != 200 || bytes.Contains(r.Body.Bytes(), []byte(bk.ID)) {
		t.Fatal("admin filter", r.Body.String())
	}
	// Even the administrator's player key cannot inherit management access to B's playlist.
	r = callAPI(api, "POST", "/api/v2/auth/keys", fmt.Sprintf(`{"name":"admin bad","allowed_playlist_ids":[%q]}`, "keys-private-b"))
	if r.Code != 400 {
		t.Fatal("admin management leaked to player", r.Code)
	}
}
