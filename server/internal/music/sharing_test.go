package music

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func sharingFixture(t *testing.T) (*API, User, User, Playlist) {
	t.Helper()
	api, a, b, p := communityFixture(t)
	p, err := api.store.SetPlaylistVisibility(p.ID, "shared", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	return api, a, b, p
}

func TestSharingInvitationLimitAndDisabledTarget(t *testing.T) {
	api, a, b, p := sharingFixture(t)
	s := api.store
	if _, err := s.db.Exec("UPDATE users SET enabled=0 WHERE id=?", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version}); !errors.Is(err, errInvitationTarget) {
		t.Fatal("disabled target invited", err)
	}
	if _, err := s.db.Exec("UPDATE users SET enabled=1 WHERE id=?", b.ID); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 100; n++ {
		id := uuid.NewString()
		if _, err := s.db.Exec(`INSERT INTO users SELECT ?,?,display_name,password_hash,role,enabled,auth_version,version,created_at,updated_at,must_change_password FROM users WHERE id=?`, id, fmt.Sprintf("invite-limit-%d", n), b.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("INSERT INTO playlist_invitations VALUES(?,?,?,?,?,1,?,?,?)", uuid.NewString(), p.ID, a.ID, id, "pending", p.Name, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version}); err == nil {
		t.Fatal("invitation limit ignored")
	}
	page, err := s.Invitations(a.ID, p.ID, 30, 30)
	if err != nil || page.Total != 100 || len(page.Items) != 30 {
		t.Fatalf("bounded page %+v %v", page, err)
	}
}

func TestSharingInvitationLifecycleAndIdentity(t *testing.T) {
	api, a, b, p := sharingFixture(t)
	s := api.store
	invite, err := s.CreateInvitation(p.ID, "  PUBLIC-READER ", MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil || invite.State != "pending" || invite.RecipientID != b.ID || invite.Version != 1 {
		t.Fatalf("create: %+v %v", invite, err)
	}
	duplicate, err := s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil || duplicate.ID != invite.ID || duplicate.Version != invite.Version {
		t.Fatal("duplicate changed invitation")
	}
	if hasSubscription(s.db, b.ID, p.ID) {
		t.Fatal("invitation auto-subscribed before acceptance")
	}
	if _, err = s.InvitationAction(invite.ID, "accept", MutationOptions{UserID: a.ID, ExpectedVersion: invite.Version}); !errors.Is(err, errResourceHidden) {
		t.Fatal("non-recipient accepted")
	}
	if _, err = s.InvitationAction(invite.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: 99}); !errors.Is(err, errVersionConflict) {
		t.Fatal("stale accept allowed")
	}
	accepted, err := s.InvitationAction(invite.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: invite.Version})
	if err != nil || accepted.State != "accepted" || !hasSubscription(s.db, b.ID, p.ID) {
		t.Fatalf("accept: %+v %v", accepted, err)
	}
	if _, err = s.InvitationAction(invite.ID, "revoke", MutationOptions{UserID: b.ID, ExpectedVersion: accepted.Version}); !errors.Is(err, errResourceHidden) {
		t.Fatal("recipient revoked owner's invitation")
	}
	revoked, err := s.InvitationAction(invite.ID, "revoke", MutationOptions{UserID: a.ID, ExpectedVersion: accepted.Version})
	if err != nil || revoked.State != "revoked" || !hasSubscription(s.db, b.ID, p.ID) {
		t.Fatal("revoke removed subscription or failed", err)
	}
	if _, err = s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version}); !errors.Is(err, errInvitationState) {
		t.Fatal("ordinary create silently reinstated revoked grant")
	}
	pending, err := s.InvitationAction(invite.ID, "resend", MutationOptions{UserID: a.ID, ExpectedVersion: revoked.Version})
	if err != nil || pending.State != "pending" {
		t.Fatal("resend", err)
	}
	declined, err := s.InvitationAction(invite.ID, "decline", MutationOptions{UserID: b.ID, ExpectedVersion: pending.Version})
	if err != nil || declined.State != "declined" {
		t.Fatal("decline", err)
	}
	if _, err = s.InvitationAction(invite.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: declined.Version}); !errors.Is(err, errInvitationState) {
		t.Fatal("accepted a declined invitation")
	}
	for _, name := range []string{a.Username, "does-not-exist", ""} {
		if _, err = s.CreateInvitation(p.ID, name, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version}); err == nil {
			t.Fatalf("invalid target accepted: %s", name)
		}
	}
	if _, err = s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: b.ID, ExpectedVersion: p.Version}); !errors.Is(err, errResourceHidden) {
		t.Fatal("non-owner created invitation")
	}
}

func TestSharingAcceptanceRollbackAndPages(t *testing.T) {
	api, a, b, p := sharingFixture(t)
	s := api.store
	i, err := s.CreateInvitation(p.ID, b.Username, MutationOptions{UserID: a.ID, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_invite_audit BEFORE INSERT ON audit WHEN NEW.action='invitation.accept' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InvitationAction(i.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: i.Version}); err == nil {
		t.Fatal("expected audit failure")
	}
	if hasSubscription(s.db, b.ID, p.ID) {
		t.Fatal("partial subscription survived rollback")
	}
	read, err := readInvitation(s.db, i.ID)
	if err != nil || read.State != "pending" || read.Version != i.Version {
		t.Fatal("partial invitation survived rollback")
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_invite_audit"); err != nil {
		t.Fatal(err)
	}
	received, err := s.Invitations(b.ID, "", 0, 1)
	if err != nil || received.Total != 1 || len(received.Items) != 1 {
		t.Fatalf("received page: %+v %v", received, err)
	}
	page, err := s.Invitations(a.ID, p.ID, 1, 1)
	if err != nil || page.Total != 1 || len(page.Items) != 0 {
		t.Fatal("owner paging", err)
	}
	if _, err = s.Invitations(b.ID, p.ID, 0, 30); !errors.Is(err, errResourceHidden) {
		t.Fatal("non-owner enumerated recipients")
	}
	if _, err = s.InvitationAction(i.ID, "accept", MutationOptions{UserID: b.ID, ExpectedVersion: i.Version}); err != nil {
		t.Fatal(err)
	}
}
