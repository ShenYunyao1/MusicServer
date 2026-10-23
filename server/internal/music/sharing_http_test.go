package music

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharingCreateNeedsSharedMode(t *testing.T) {
	api, a, b, p := communityFixture(t)
	token := communityLogin(t, api.store, a.Username)
	r := memberVersionRequest(api, token, "POST", "/api/v2/playlists/"+p.ID+"/invitations", `{"username":"`+b.Username+`"}`, p.Version)
	if r.Code != 400 || !strings.Contains(r.Body.String(), "SHARING_MODE_REQUIRED") {
		t.Fatalf("private source must explicitly require shared mode: %d %s", r.Code, r.Body)
	}
}

func TestSharingHTTPAccountBoundLifecycle(t *testing.T) {
	api, a, b, p := sharingFixture(t)
	s := api.store
	c, _ := readyMember(t, api, "uninvited-account")
	at, bt, ct := communityLogin(t, s, a.Username), communityLogin(t, s, b.Username), communityLogin(t, s, c.Username)
	endpoint := "/api/v2/playlists/" + p.ID + "/invitations"
	if r := sessionRequest(api, at, "POST", endpoint, `{"username":"`+b.Username+`"}`); r.Code != 428 {
		t.Fatal("missing version accepted")
	}
	if r := memberVersionRequest(api, at, "POST", endpoint, `{"username":"`+b.Username+`"}`, p.Version+1); r.Code != 409 {
		t.Fatal("stale source accepted")
	}
	r := memberVersionRequest(api, at, "POST", endpoint, `{"username":"`+b.Username+`"}`, p.Version)
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body)
	}
	var i PlaylistInvitation
	_ = json.Unmarshal(r.Body.Bytes(), &i)
	if i.State != "pending" || i.RecipientID != b.ID || !i.Available {
		t.Fatal("wrong target/state")
	}
	link := "/api/v2/invitations/" + i.ID
	if r := sessionRequest(api, ct, "GET", link, ""); r.Code != 404 {
		t.Fatal("forwarded invitation link exposed metadata")
	}
	if r := memberVersionRequest(api, ct, "POST", link+"/accept", "", i.Version); r.Code != 404 {
		t.Fatal("forwarded link granted another account")
	}
	if r := sessionRequest(api, bt, "GET", endpoint, ""); r.Code != 404 {
		t.Fatal("recipient enumerated owner's shares")
	}
	if r := sessionRequest(api, bt, "GET", link, ""); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" || strings.Contains(r.Body.String(), "author private note") {
		t.Fatal("unsafe received invitation", r.Body)
	}
	page := sessionRequest(api, bt, "GET", "/api/v2/invitations?limit=1", "")
	var data InvitationPage
	_ = json.Unmarshal(page.Body.Bytes(), &data)
	if data.Total != 1 || len(data.Items) != 1 {
		t.Fatal("received page")
	}
	if r := sessionRequest(api, bt, "GET", "/api/v2/invitations?limit=101", ""); r.Code != 400 {
		t.Fatal("unbounded list allowed")
	}
	badCSRF := httptest.NewRequest("POST", "http://example.com"+link+"/accept", nil)
	badCSRF.AddCookie(&http.Cookie{Name: sessionCookie, Value: bt})
	badCSRF.Header.Set("Origin", "http://example.com")
	denied := httptest.NewRecorder()
	api.ServeHTTP(denied, badCSRF)
	if denied.Code != 403 {
		t.Fatal("CSRF accepted")
	}
	r = memberVersionRequest(api, bt, "POST", link+"/accept", "", i.Version)
	if r.Code != 200 {
		t.Fatalf("accept: %d %s", r.Code, r.Body)
	}
	_ = json.Unmarshal(r.Body.Bytes(), &i)
	if r := memberVersionRequest(api, bt, "POST", link+"/decline", "", i.Version); r.Code != 409 {
		t.Fatal("accepted invitation became declined")
	}
	if r := sessionRequest(api, bt, "GET", "/api/v2/playlists/"+p.ID, ""); r.Code != 200 {
		t.Fatal("accept did not subscribe")
	}
	if r := memberVersionRequest(api, bt, "DELETE", endpoint+"/"+i.ID, "", i.Version); r.Code != 404 {
		t.Fatal("recipient revoked owner share")
	}
	if r := memberVersionRequest(api, at, "DELETE", endpoint+"/"+i.ID, "", 1); r.Code != 409 {
		t.Fatal("stale revoke accepted")
	}
	r = memberVersionRequest(api, at, "DELETE", endpoint+"/"+i.ID, "", i.Version)
	if r.Code != 200 {
		t.Fatal("revoke", r.Body)
	}
	_ = json.Unmarshal(r.Body.Bytes(), &i)
	r = memberVersionRequest(api, at, "POST", endpoint+"/"+i.ID+"/resend", "", i.Version)
	if r.Code != 200 {
		t.Fatal("resend", r.Body)
	}
	_ = json.Unmarshal(r.Body.Bytes(), &i)
	if r := memberVersionRequest(api, bt, "POST", link+"/decline", "", i.Version); r.Code != 200 {
		t.Fatal("decline", r.Body)
	}
	key := communityKey(t, api, at, p.ID)
	if r := communityPlayer(api, key, "invitations"); r.Code != 403 {
		t.Fatal("player key enumerated invitations")
	}
	if r := communityPlayer(api, key, "playlists/"+p.ID+"/invitations"); r.Code != 403 {
		t.Fatal("player key managed shares")
	}
	if r := sessionRequest(api, bt, "GET", "/api/v2/admin/users", ""); r.Code != 403 {
		t.Fatal("member enumerated accounts")
	}
}

func TestSharingHTTPFirstPasswordAndPrivateSuspension(t *testing.T) {
	api, a, b, p := sharingFixture(t)
	s := api.store
	at, bt := communityLogin(t, s, a.Username), communityLogin(t, s, b.Username)
	i, err := s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.SetPlaylistVisibility(p.ID, "private", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if r := memberVersionRequest(api, bt, "POST", "/api/v2/invitations/"+i.ID+"/accept", "", i.Version); r.Code != 409 {
		t.Fatal("accepted while source private")
	}
	if r := sessionRequest(api, at, "GET", "/api/v2/public/playlists/"+p.ID, ""); r.Code != 404 {
		t.Fatal("shared source publicly discoverable")
	}
	users, _ := s.Users()
	var admin User
	for _, u := range users {
		if u.Role == "admin" {
			admin = u
		}
	}
	created, err := s.CreateMember("uninitialized-share", "Pending", MutationOptions{UserID: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, _, token, err := s.Login(created.User.Username, created.TemporaryPassword, "pending-invite")
	if err != nil {
		t.Fatal(err)
	}
	if r := sessionRequest(api, token, "GET", "/api/v2/invitations", ""); r.Code != 403 || !strings.Contains(r.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
		t.Fatal("first password gate bypassed")
	}
}
