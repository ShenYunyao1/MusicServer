package music

import (
	"database/sql"
	"encoding/json"
	"errors"
)

type publicDetailPage struct {
	Playlist map[string]any   `json:"playlist"`
	Tracks   []map[string]any `json:"tracks"`
	Total    int              `json:"total"`
	Offset   int              `json:"offset"`
	Limit    int              `json:"limit"`
}

// Public detail is a summary page. Full lyrics/technical metadata are loaded
// through the authorized track endpoint when opened; playback has its own
// complete ordered catalog. Neither path depends on the visible page.
func readPublicDetailPage(reader sqlReader, auth authContext, id string, q browseQuery) (publicDetailPage, error) {
	return readExternalDetailPage(reader, auth, id, q, false)
}

func readExternalDetailPage(reader sqlReader, auth authContext, id string, q browseQuery, subscription bool) (publicDetailPage, error) {
	page := publicDetailPage{Tracks: []map[string]any{}, Offset: q.Offset, Limit: q.Limit}
	if q.Offset < 0 || q.Offset > 1000000 || q.Limit < 1 || q.Limit > 100 || len(q.Search) > 1024 {
		return page, errTagInvalid
	}
	if err := validateAccountSession(reader, auth); err != nil {
		return page, err
	}
	var raw string
	err := reader.QueryRow("SELECT json_remove(payload,'$.track_ids','$.track_notes','$.unavailable_track_ids') FROM playlists WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return page, errResourceHidden
	}
	if err != nil {
		return page, err
	}
	var p Playlist
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return page, err
	}
	if (!subscription && !publishedPlaylist(reader, p)) || (subscription && (!hasSubscription(reader, auth.userID, id) || !externalPlaylistAccess(reader, auth.userID, p))) {
		return page, errResourceHidden
	}
	p.ReadOnly = true
	p.TrackIDs = []string{}
	p.TrackNotes = map[string]string{}
	if err = reader.QueryRow("SELECT display_name FROM users WHERE id=?", p.OwnerID).Scan(&p.SourceOwnerName); err != nil {
		return page, err
	}
	var subscriptionVersion int64
	err = reader.QueryRow("SELECT version FROM subscriptions WHERE user_id=? AND playlist_id=?", auth.userID, id).Scan(&subscriptionVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return page, err
	}
	if err == nil {
		p.SubscriptionStatus = "active"
		p.SubscriptionVersion = subscriptionVersion
	}
	from := ` FROM playlist_tracks m JOIN tracks t ON t.id=m.track_id LEFT JOIN subscription_notes n ON n.playlist_id=m.playlist_id AND n.track_id=m.track_id AND n.user_id=? WHERE m.playlist_id=?`
	args := []any{auth.userID, id}
	var trackCount int
	if err = reader.QueryRow("SELECT count(*)"+from, args...).Scan(&trackCount); err != nil {
		return page, err
	}
	if q.Search != "" {
		from += " AND ((t.removed=0 AND t.missing=0 AND " + browseTrackSearch + ") OR instr(lower(COALESCE(n.note,'')),lower(?))>0 OR ((t.removed=1 OR t.missing=1) AND instr(lower('Unavailable'),lower(?))>0))"
		args = append(args, q.Search, q.Search, q.Search)
	}
	page.Total = trackCount
	if q.Search != "" {
		if err = reader.QueryRow("SELECT count(*)"+from, args...).Scan(&page.Total); err != nil {
			return page, err
		}
	}
	projection := `CASE WHEN t.removed=1 OR t.missing=1 THEN json_object('id',t.id,'title','Unavailable','removed',json(CASE WHEN t.removed=1 THEN 'true' ELSE 'false' END),'missing',json(CASE WHEN t.missing=1 THEN 'true' ELSE 'false' END),'version',t.version,'detail_loaded',json('false')) ELSE json_object('id',t.id,'title',t.title,'artist',json_extract(t.payload,'$.artist'),'album',json_extract(t.payload,'$.album'),'cover_id',json_extract(t.payload,'$.cover_id'),'duration_ms',json_extract(t.payload,'$.duration_ms'),'version',t.version,'detail_loaded',json('false')) END`
	rows, err := reader.Query("SELECT "+projection+",COALESCE(n.note,'')"+from+" ORDER BY m.position LIMIT ? OFFSET ?", append(args, q.Limit, q.Offset)...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var value, note string
		if err = rows.Scan(&value, &note); err != nil {
			rows.Close()
			return page, err
		}
		var track map[string]any
		if err = json.Unmarshal([]byte(value), &track); err != nil {
			rows.Close()
			return page, err
		}
		trackID := track["id"].(string)
		p.TrackIDs = append(p.TrackIDs, trackID)
		if note != "" {
			p.TrackNotes[trackID] = note
		}
		if track["removed"] == true || track["missing"] == true {
			p.UnavailableTrackIDs = append(p.UnavailableTrackIDs, trackID)
		}
		page.Tracks = append(page.Tracks, track)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return page, err
	}
	if err = json.Unmarshal(encoded, &page.Playlist); err != nil {
		return page, err
	}
	page.Playlist["track_count"], page.Playlist["members_complete"] = trackCount, q.Search == "" && len(page.Tracks) == trackCount
	return page, nil
}
