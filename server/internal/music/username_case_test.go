package music

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestUsernameCasePreservedAcrossAccountOperations(t *testing.T) {
	api, _ := testAPI(t)
	response := callAPI(api, "POST", "/api/v2/admin/users", `{"username":"  AbC.Def_12  "}`)
	if response.Code != 201 {
		t.Fatalf("create: %d", response.Code)
	}
	var created memberFixture
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.User.Username != "AbC.Def_12" || created.User.DisplayName != "AbC.Def_12" {
		t.Fatalf("creation changed username case: %q / %q", created.User.Username, created.User.DisplayName)
	}
	for _, name := range []string{"abc.def_12", "ABC.DEF_12", " AbC.Def_12 "} {
		user, _, token, err := api.store.Login(name, created.Password, "case-test")
		if err != nil || user.ID != created.User.ID || user.Username != created.User.Username {
			t.Fatalf("login changed identity for %q: %v", name, err)
		}
		me := sessionRequest(api, token, "GET", "/api/v2/auth/me", "")
		var identity struct {
			User User `json:"user"`
		}
		if me.Code != 200 || json.Unmarshal(me.Body.Bytes(), &identity) != nil || identity.User.Username != "AbC.Def_12" {
			t.Fatalf("session identity lost username case: %d", me.Code)
		}
		duplicate := callAPI(api, "POST", "/api/v2/admin/users", `{"username":"`+name+`"}`)
		if duplicate.Code != 409 {
			t.Fatalf("duplicate %q: %d", name, duplicate.Code)
		}
	}
	password := "changed mixed case password"
	if err := api.store.ChangePassword(created.User, created.Password, password); err != nil {
		t.Fatal(err)
	}
	if err := api.store.ResetPassword("aBC.dEF_12", "reset mixed case password", false); err != nil {
		t.Fatalf("case-insensitive offline reset: %v", err)
	}
	user, _, _, err := api.store.Login("ABC.DEF_12", "reset mixed case password", "reset-case")
	if err != nil || user.ID != created.User.ID || user.Username != "AbC.Def_12" {
		t.Fatalf("login after reset: %v", err)
	}
	stored, err := readUser(api.store.db, user.ID)
	if err != nil || stored.Username != "AbC.Def_12" {
		t.Fatalf("persisted username case: %v", err)
	}
}

func TestUsernameCaseInvitationLookup(t *testing.T) {
	api, author, _, playlist := communityFixture(t)
	recipient, _ := readyMember(t, api, "Invite.Target")
	playlist, err := api.store.SetPlaylistVisibility(playlist.ID, "shared", MutationOptions{UserID: author.ID, ExpectedVersion: playlist.Version})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"invite.target", "INVITE.TARGET", " Invite.Target "} {
		invitation, err := api.store.CreateInvitation(playlist.ID, name, MutationOptions{UserID: author.ID, ExpectedVersion: playlist.Version})
		if err != nil || invitation.RecipientID != recipient.ID || invitation.RecipientUsername != "Invite.Target" {
			t.Fatalf("invitation lookup for %q: %v", name, err)
		}
	}
}

func TestUsernameCaseLoginLimitSharedByAllSpellings(t *testing.T) {
	api, _ := testAPI(t)
	created := createMemberFixture(t, api, "Rate.Limited")
	for i, name := range []string{"Rate.Limited", "rate.limited", "RATE.LIMITED", "rATE.lIMITED", " Rate.Limited "} {
		if _, _, _, err := api.store.Login(name, "wrong password", "case-rate"); !errors.Is(err, errCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, _, _, err := api.store.Login("rate.limited", created.Password, "different-ip"); !errors.Is(err, errLoginLimited) {
		t.Fatalf("changing case bypassed account rate limit: %v", err)
	}
}
