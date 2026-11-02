package music

import (
	"database/sql"
	"fmt"
	"strings"
)

func coverReadable(reader sqlReader, auth authContext, id string) (bool, error) {
	if auth.session {
		if err := validateAccountSession(reader, auth); err != nil {
			return false, err
		}
		if auth.role == "admin" {
			return true, nil
		}
	} else {
		var err error
		auth, err = currentKeyAuth(reader, auth)
		if err != nil {
			return false, err
		}
		if !hasScope(auth, ScopeMediaRead) {
			return false, errResourceHidden
		}
	}
	args := []any{sql.Named("cover", id), sql.Named("user", auth.userID)}
	external := "(u.enabled=1 AND (p.visibility='public' OR (p.visibility='shared' AND EXISTS(SELECT 1 FROM playlist_invitations i WHERE i.playlist_id=p.id AND i.inviter_id=p.owner_id AND i.recipient_id=@user AND i.state='accepted'))))"
	predicate := "(p.system=1 OR p.owner_id=@user OR " + external + ")"
	trackAccess := "1"
	if !auth.session {
		keys := []string{}
		for id, allowed := range auth.allowedPlaylists {
			if allowed {
				key := fmt.Sprintf("grant%d", len(keys))
				keys = append(keys, "@"+key)
				args = append(args, sql.Named(key, id))
			}
		}
		if len(keys) == 0 {
			return false, nil
		}
		predicate = "(p.id IN (" + strings.Join(keys, ",") + ") AND (p.system=1 OR p.owner_id=@user OR (EXISTS(SELECT 1 FROM subscriptions s WHERE s.user_id=@user AND s.playlist_id=p.id) AND " + external + ")))"
		trackAccess = "EXISTS(SELECT 1 FROM playlist_tracks m JOIN playlists p ON p.id=m.playlist_id LEFT JOIN users u ON u.id=p.owner_id WHERE m.track_id=t.id AND " + predicate + ")"
	}
	var allowed bool
	err := reader.QueryRow("SELECT EXISTS(SELECT 1 FROM tracks t WHERE t.removed=0 AND json_extract(t.payload,'$.cover_id')=@cover AND "+trackAccess+") OR EXISTS(SELECT 1 FROM playlists p LEFT JOIN users u ON u.id=p.owner_id WHERE json_extract(p.payload,'$.cover_id')=@cover AND "+predicate+")", args...).Scan(&allowed)
	return allowed, err
}
