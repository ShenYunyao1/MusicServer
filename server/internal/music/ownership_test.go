package music

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestMultiuserResourceTransferAtomicAndAudited(t *testing.T) {
	api, _ := testAPI(t)
	a, at := readyMember(t, api, "transfer-a")
	b, _ := readyMember(t, api, "transfer-b")
	tr, err := api.store.ImportAudioWithMetadata("transfer.wav", bytes.NewReader(validTestAudio(t)), nil, MutationOptions{UserID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	api.store.PutPlaylist(Playlist{ID: "transfer-list", Name: "Private", TrackIDs: []string{tr.ID}, TrackNotes: map[string]string{tr.ID: "personal"}}, MutationOptions{UserID: a.ID})
	p, _ := api.store.Playlist("transfer-list")
	key, err := api.store.CreateAPIKey(APIKeyCreateRequest{Name: "old key", AllowedPlaylistIDs: []string{p.ID}}, MutationOptions{UserID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := callAPI(api, "GET", "/api/v2/admin/users/"+a.ID+"/tracks?limit=1", "")
	if r.Code != 200 {
		t.Fatal("resource list", r.Code, r.Body.String())
	}
	if r := memberVersionRequest(api, at, "GET", "/api/v2/admin/users/"+b.ID+"/playlists", "", 0); r.Code != 403 {
		t.Fatal("member admin list", r.Code)
	}
	enabled := false
	a, err = api.store.UpdateUser(a.ID, AccountPatch{Enabled: &enabled}, MutationOptions{ExpectedVersion: a.Version})
	if err != nil {
		t.Fatal(err)
	}
	request := TransferRequest{TargetID: b.ID, TargetVersion: b.Version, All: true, Tracks: []ResourceVersion{{tr.ID, tr.Version}}, Playlists: []ResourceVersion{{p.ID, p.Version + 1}}}
	body, _ := json.Marshal(request)
	r = adminVersionRequest(api, "POST", "/api/v2/admin/users/"+a.ID+"/transfer", string(body), a.Version)
	if r.Code != 409 {
		t.Fatal("stale selection", r.Code, r.Body.String())
	}
	unchanged, _ := api.store.Track(tr.ID)
	if unchanged.OwnerID != a.ID {
		t.Fatal("partial transfer")
	}
	request.Playlists[0].Version = p.Version
	body, _ = json.Marshal(request)
	r = adminVersionRequest(api, "POST", "/api/v2/admin/users/"+a.ID+"/transfer", string(body), a.Version)
	if r.Code != 200 {
		t.Fatal("transfer", r.Code, r.Body.String())
	}
	after, _ := api.store.Track(tr.ID)
	moved, _ := api.store.Playlist(p.ID)
	if after.OwnerID != b.ID || after.UploadedBy != a.ID || moved.OwnerID != b.ID || moved.TrackNotes[tr.ID] != "personal" {
		t.Fatal("provenance/notes changed")
	}
	for _, k := range api.store.APIKeys() {
		if k.ID == key.ID && (k.UserID != a.ID || len(k.AllowedPlaylistIDs) != 0) {
			t.Fatal("key moved or grant retained")
		}
	}
	enabled = true
	a, _ = api.store.UpdateUser(a.ID, AccountPatch{Enabled: &enabled}, MutationOptions{ExpectedVersion: a.Version})
	after, _ = api.store.Track(tr.ID)
	if after.OwnerID != b.ID {
		t.Fatal("reenable reversed ownership")
	}
	r = callAPI(api, "GET", "/api/v2/admin/users/"+a.ID+"/tracks?relation=uploaded", "")
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(tr.ID)) {
		t.Fatal("uploader relation", r.Body.String())
	}
	var n int
	api.store.db.QueryRow("SELECT count(*) FROM audit WHERE action IN ('user.resources.read','user.resources.transfer')").Scan(&n)
	if n < 3 {
		t.Fatal("missing admin audit", fmt.Sprint(n))
	}
}
