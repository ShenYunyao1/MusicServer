package music

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

var errSharingMode = errors.New("set playlist visibility to shared before inviting accounts")
var errInvitationState = errors.New("invitation state changed; refresh or explicitly invite again")
var errInvitationTarget = errors.New("this account cannot be invited")

func hasAcceptedInvitation(reader sqlReader, userID string, p Playlist) bool {
	var accepted bool
	return reader.QueryRow("SELECT EXISTS(SELECT 1 FROM playlist_invitations WHERE playlist_id=? AND inviter_id=? AND recipient_id=? AND state='accepted')", p.ID, p.OwnerID, userID).Scan(&accepted) == nil && accepted
}
func externalPlaylistAccess(reader sqlReader, userID string, p Playlist) bool {
	if p.System || (p.Visibility != "public" && p.Visibility != "shared") {
		return false
	}
	var enabled bool
	if reader.QueryRow("SELECT enabled=1 FROM users WHERE id=?", p.OwnerID).Scan(&enabled) != nil || !enabled {
		return false
	}
	return p.Visibility == "public" || hasAcceptedInvitation(reader, userID, p)
}

type PlaylistInvitation struct {
	ID                string `json:"id"`
	PlaylistID        string `json:"playlist_id"`
	InviterID         string `json:"inviter_id"`
	RecipientID       string `json:"recipient_id"`
	RecipientUsername string `json:"recipient_username"`
	InviterName       string `json:"inviter_name"`
	PlaylistName      string `json:"playlist_name"`
	State             string `json:"state"`
	Version           int64  `json:"version"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	Available         bool   `json:"available"`
}
type InvitationPage struct {
	Items  []PlaylistInvitation `json:"items"`
	Total  int                  `json:"total"`
	Offset int                  `json:"offset"`
	Limit  int                  `json:"limit"`
}

const invitationColumns = `i.id,i.playlist_id,i.inviter_id,i.recipient_id,r.username,u.display_name,i.last_name,i.state,i.version,i.created_at,i.updated_at,(p.visibility='shared' AND p.owner_id=i.inviter_id AND u.enabled=1)`
const invitationJoin = ` FROM playlist_invitations i JOIN playlists p ON p.id=i.playlist_id JOIN users u ON u.id=i.inviter_id JOIN users r ON r.id=i.recipient_id`

func scanInvitation(row interface{ Scan(...any) error }) (PlaylistInvitation, error) {
	var i PlaylistInvitation
	err := row.Scan(&i.ID, &i.PlaylistID, &i.InviterID, &i.RecipientID, &i.RecipientUsername, &i.InviterName, &i.PlaylistName, &i.State, &i.Version, &i.CreatedAt, &i.UpdatedAt, &i.Available)
	return i, err
}
func readInvitation(reader sqlReader, id string) (PlaylistInvitation, error) {
	return scanInvitation(reader.QueryRow("SELECT "+invitationColumns+invitationJoin+" WHERE i.id=?", id))
}
func sharingOwner(tx *sql.Tx, id string, options MutationOptions) (Playlist, error) {
	if err := subscriberIdentity(tx, options); err != nil {
		return Playlist{}, err
	}
	p, err := readPlaylistFrom(tx, id)
	if err != nil || p.System || p.OwnerID != options.UserID {
		return Playlist{}, errResourceHidden
	}
	return p, nil
}
func invitationTarget(tx *sql.Tx, id string, owner string) error {
	var enabled bool
	if id == owner || tx.QueryRow("SELECT enabled=1 FROM users WHERE id=?", id).Scan(&enabled) != nil || !enabled {
		return errInvitationTarget
	}
	return nil
}

func (s *Store) CreateInvitation(id, username string, options MutationOptions) (PlaylistInvitation, error) {
	var result PlaylistInvitation
	err := s.transaction(func(tx *sql.Tx) error {
		p, err := sharingOwner(tx, id, options)
		if err != nil {
			return err
		}
		if err = checkVersion(p.Version, []MutationOptions{options}); err != nil {
			return err
		}
		if p.Visibility != "shared" {
			return errSharingMode
		}
		var recipient string
		if tx.QueryRow("SELECT id FROM users WHERE username=?", normalizeUsername(username)).Scan(&recipient) != nil {
			return errInvitationTarget
		}
		if err = invitationTarget(tx, recipient, p.OwnerID); err != nil {
			return err
		}
		var existing string
		err = tx.QueryRow("SELECT id FROM playlist_invitations WHERE playlist_id=? AND recipient_id=?", id, recipient).Scan(&existing)
		if err == nil {
			result, err = readInvitation(tx, existing)
			if err != nil {
				return err
			}
			if result.InviterID != p.OwnerID || result.State == "revoked" || result.State == "declined" {
				return errInvitationState
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var count int
		if err = tx.QueryRow("SELECT count(*) FROM playlist_invitations WHERE playlist_id=?", id).Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return errors.New("playlist invitation limit is 100")
		}
		inviteID, now := uuid.NewString(), persistedTime(time.Now())
		if _, err = tx.Exec("INSERT INTO playlist_invitations VALUES(?,?,?,?,?,1,?,?,?)", inviteID, id, p.OwnerID, recipient, "pending", p.Name, now, now); err != nil {
			return err
		}
		result, err = readInvitation(tx, inviteID)
		if err != nil {
			return err
		}
		return auditMutation(tx, "invitation.create", inviteID, nil, result, []MutationOptions{options})
	})
	return result, err
}

func (s *Store) InvitationAction(id, action string, options MutationOptions) (PlaylistInvitation, error) {
	var result PlaylistInvitation
	err := s.transaction(func(tx *sql.Tx) error {
		if err := subscriberIdentity(tx, options); err != nil {
			return err
		}
		before, err := readInvitation(tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return errResourceHidden
		}
		if err != nil {
			return err
		}
		p, err := readPlaylistFrom(tx, before.PlaylistID)
		if err != nil {
			return err
		}
		ownerAction := action == "revoke" || action == "resend"
		if ownerAction {
			if p.OwnerID != options.UserID {
				return errResourceHidden
			}
		} else if before.RecipientID != options.UserID {
			return errResourceHidden
		}
		if err = checkVersion(before.Version, []MutationOptions{options}); err != nil {
			return err
		}
		state, inviter, name := before.State, before.InviterID, before.PlaylistName
		switch action {
		case "accept", "decline":
			if !before.Available {
				return errInvitationState
			}
			wanted := "accepted"
			if action == "decline" {
				wanted = "declined"
			}
			if state == wanted {
				result = before
				return nil
			}
			if state != "pending" {
				return errInvitationState
			}
			state = wanted
			if action == "accept" {
				if _, err = tx.Exec("INSERT INTO subscriptions(user_id,playlist_id,last_name,version,created_at) VALUES(?,?,?,1,?) ON CONFLICT(user_id,playlist_id) DO NOTHING", options.UserID, p.ID, p.Name, persistedTime(time.Now())); err != nil {
					return err
				}
			}
		case "revoke":
			if state == "revoked" {
				result = before
				return nil
			}
			state = "revoked"
		case "resend":
			if p.Visibility != "shared" {
				return errSharingMode
			}
			if err = invitationTarget(tx, before.RecipientID, p.OwnerID); err != nil {
				return err
			}
			if state != "revoked" && state != "declined" {
				return errInvitationState
			}
			state, inviter, name = "pending", p.OwnerID, p.Name
		default:
			return errors.New("unknown invitation action")
		}
		if _, err = tx.Exec("UPDATE playlist_invitations SET state=?,inviter_id=?,last_name=?,version=version+1,updated_at=? WHERE id=?", state, inviter, name, persistedTime(time.Now()), id); err != nil {
			return err
		}
		if err = recordChange(tx, "playlist", p.ID, false); err != nil {
			return err
		}
		result, err = readInvitation(tx, id)
		if err != nil {
			return err
		}
		return auditMutation(tx, "invitation."+action, id, before, result, []MutationOptions{options})
	})
	return result, err
}

func (s *Store) Invitations(userID, playlistID string, offset, limit int) (InvitationPage, error) {
	page := InvitationPage{Items: []PlaylistInvitation{}, Offset: offset, Limit: limit}
	if offset < 0 || limit < 1 || limit > 100 {
		return page, errors.New("invalid invitation page")
	}
	err := s.transaction(func(tx *sql.Tx) error {
		if err := subscriberIdentity(tx, MutationOptions{UserID: userID}); err != nil {
			return err
		}
		filter, args := " WHERE i.recipient_id=?", []any{userID}
		if playlistID != "" {
			if _, err := sharingOwner(tx, playlistID, MutationOptions{UserID: userID}); err != nil {
				return err
			}
			filter, args = " WHERE i.playlist_id=?", []any{playlistID}
		}
		if err := tx.QueryRow("SELECT count(*)"+invitationJoin+filter, args...).Scan(&page.Total); err != nil {
			return err
		}
		args = append(args, limit, offset)
		rows, err := tx.Query("SELECT "+invitationColumns+invitationJoin+filter+" ORDER BY i.updated_at DESC,i.id LIMIT ? OFFSET ?", args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			i, err := scanInvitation(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, i)
		}
		return rows.Err()
	})
	return page, err
}
