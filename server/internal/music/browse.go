package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const browseMaxLimit = 100

var errBrowseRestart = errors.New("library changed; restart browsing from the first page")

type browseQuery struct {
	Offset            int
	Limit             int
	Search            string
	Filter            string
	Revision          *int64
	ExcludePlaylistID string
	SummaryOnly       bool
}

type browsePage struct {
	Items    []map[string]any `json:"items"`
	Total    int              `json:"total"`
	Offset   int              `json:"offset"`
	Limit    int              `json:"limit"`
	Revision int64            `json:"revision"`
	Playlist map[string]any   `json:"playlist,omitempty"`
}

func parseBrowseQuery(request *http.Request) (browseQuery, error) {
	values := request.URL.Query()
	q := browseQuery{Limit: 50, Search: strings.TrimSpace(values.Get("q")), Filter: values.Get("filter")}
	q.ExcludePlaylistID = values.Get("exclude_playlist_id")
	q.SummaryOnly = values.Get("summary_only") == "1"
	for name, dest := range map[string]*int{"offset": &q.Offset, "limit": &q.Limit} {
		if raw, present := values[name]; present {
			if len(raw) != 1 {
				return q, errors.New("invalid " + name)
			}
			n, err := strconv.Atoi(raw[0])
			if err != nil || n < 0 || name == "limit" && n == 0 {
				return q, errors.New("invalid " + name)
			}
			*dest = n
		}
	}
	q.Limit = min(q.Limit, browseMaxLimit)
	if raw, present := values["revision"]; present {
		if len(raw) != 1 {
			return q, errors.New("invalid revision")
		}
		revision, err := strconv.ParseInt(raw[0], 10, 64)
		if err != nil || revision < 0 {
			return q, errors.New("invalid revision")
		}
		q.Revision = &revision
	}
	if q.Filter == "" {
		q.Filter = "all"
	}
	switch q.Filter {
	case "all", "mine", "managed", "orphan", "missing":
	default:
		return q, errors.New("invalid filter")
	}
	return q, nil
}

// browse uses separate session-only routes so older complete-object APIs keep
// their contract. Every count, baseline and page is read in one transaction.
func (a *API) browse(response http.ResponseWriter, request *http.Request) {
	if !a.requireAccount(response, request) {
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/v2/browse/")
	parts := strings.Split(path, "/")
	cardMove := path == "playlists/move"
	move := cardMove || len(parts) == 4 && parts[0] == "playlists" && parts[2] == "tracks" && parts[3] == "move"
	if !move && request.Method != http.MethodGet || move && request.Method != http.MethodPost {
		writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	q, err := parseBrowseQuery(request)
	if err != nil {
		writeError(response, 400, "INVALID_BROWSE", err.Error())
		return
	}
	if move {
		if cardMove {
			a.browsePlaylistMove(response, request)
			return
		}
		a.browseMove(response, request, parts[1])
		return
	}
	auth := authFromRequest(request)
	var result any
	err = a.store.transaction(func(tx *sql.Tx) error {
		if err := browseIdentity(tx, auth); err != nil {
			return err
		}
		var revision int64
		if err := tx.QueryRow("SELECT value FROM meta WHERE key='revision'").Scan(&revision); err != nil {
			return err
		}
		if q.Revision != nil && *q.Revision != revision {
			return errBrowseRestart
		}
		page := browsePage{Items: []map[string]any{}, Offset: q.Offset, Limit: q.Limit, Revision: revision}
		switch {
		case path == "summary":
			var total, owned, missing, playlists int
			if err := tx.QueryRow("SELECT count(*),COALESCE(sum(owner_id=?),0),COALESCE(sum(missing=1),0) FROM tracks WHERE removed=0", auth.userID).Scan(&total, &owned, &missing); err != nil {
				return err
			}
			if err := tx.QueryRow("SELECT count(*) FROM playlists WHERE owner_id=? AND system=0", auth.userID).Scan(&playlists); err != nil {
				return err
			}
			result = map[string]any{"total": total, "owned": owned, "missing": missing, "playlist_count": playlists, "revision": revision}
			return nil
		case path == "tracks":
			if err := browseTracks(tx, auth, q, &page); err != nil {
				return err
			}
		case path == "playlists":
			if err := browsePlaylists(tx, auth, q, &page); err != nil {
				return err
			}
		case len(parts) == 3 && parts[0] == "playlists" && parts[2] == "tracks" && validID.MatchString(parts[1]):
			if err := browsePlaylistTracks(tx, auth, parts[1], q, &page); err != nil {
				return err
			}
			if auth.role == "admin" && page.Playlist["owner_id"] != auth.userID && page.Playlist["system"] != true {
				if err := auditMutation(tx, "playlist.admin.read", parts[1], nil, map[string]any{"owner_id": page.Playlist["owner_id"], "offset": q.Offset, "limit": q.Limit}, []MutationOptions{mutationOptions(request)}); err != nil {
					return err
				}
			}
		default:
			return errResourceHidden
		}
		result = page
		return nil
	})
	if err != nil {
		writeBrowseError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (a *API) browsePlaylistMove(response http.ResponseWriter, request *http.Request) {
	var body struct {
		PlaylistID    string   `json:"playlist_id"`
		BeforeID      string   `json:"before_id"`
		ExpectedOrder []string `json:"expected_order"`
		Offset        int      `json:"offset"`
		Limit         int      `json:"limit"`
		Revision      *int64   `json:"revision"`
	}
	if decodeStrict(request, &body) != nil || body.PlaylistID == "" || body.ExpectedOrder == nil || len(body.ExpectedOrder) > browseMaxLimit || body.Offset < 0 || body.Limit < 0 {
		writeError(response, 400, "INVALID_BROWSE_MOVE", "invalid relative playlist move")
		return
	}
	if body.Limit == 0 {
		body.Limit = 50
	}
	body.Limit = min(body.Limit, browseMaxLimit)
	auth := authFromRequest(request)
	err := a.store.transaction(func(tx *sql.Tx) error {
		if err := browseIdentity(tx, auth); err != nil {
			return err
		}
		var revision int64
		if err := tx.QueryRow("SELECT value FROM meta WHERE key='revision'").Scan(&revision); err != nil {
			return err
		}
		if body.Revision != nil && *body.Revision != revision {
			return errBrowseRestart
		}
		rows, err := tx.Query("SELECT id,sort_order,version FROM playlists WHERE owner_id=? AND system=0 ORDER BY sort_order,json_extract(payload,'$.name'),id", auth.userID)
		if err != nil {
			return err
		}
		ids := []string{}
		positions := map[string]int{}
		versions := map[string]int64{}
		for rows.Next() {
			var id string
			var position int
			var version int64
			if err := rows.Scan(&id, &position, &version); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			positions[id] = position
			versions[id] = version
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		start := min(body.Offset, len(ids))
		end := start + min(body.Limit, len(ids)-start)
		if !slices.Equal(ids[start:end], body.ExpectedOrder) {
			return errPlaylistConflict
		}
		index := slices.Index(ids, body.PlaylistID)
		if !slices.Contains(body.ExpectedOrder, body.PlaylistID) || index < 0 || body.BeforeID != "" && !slices.Contains(ids, body.BeforeID) {
			return errResourceHidden
		}
		if body.BeforeID == body.PlaylistID {
			return nil
		}
		ids = slices.Delete(ids, index, index+1)
		before := len(ids)
		if body.BeforeID != "" {
			before = slices.Index(ids, body.BeforeID)
		}
		ids = slices.Insert(ids, before, body.PlaylistID)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for index, id := range ids {
			position := index + 1
			if positions[id] == position {
				continue
			}
			if _, err := tx.Exec("UPDATE playlists SET sort_order=?,version=version+1,payload=json_set(payload,'$.sort_order',?,'$.version',version+1,'$.updated_at',?) WHERE id=?", position, position, now, id); err != nil {
				return err
			}
			if err := recordChange(tx, "playlist", id, false); err != nil {
				return err
			}
			if err := auditMutation(tx, "playlist.order", id, map[string]any{"sort_order": positions[id], "version": versions[id]}, map[string]any{"sort_order": position, "version": versions[id] + 1}, []MutationOptions{mutationOptions(request)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeBrowseError(response, err)
		return
	}
	read := request.Clone(request.Context())
	read.Method = http.MethodGet
	read.URL.Path = "/api/v2/browse/playlists"
	values := read.URL.Query()
	values.Set("offset", strconv.Itoa(body.Offset))
	values.Set("limit", strconv.Itoa(body.Limit))
	values.Del("revision")
	read.URL.RawQuery = values.Encode()
	a.browse(response, read)
}

func browseIdentity(reader sqlReader, auth authContext) error {
	user, err := readUser(reader, auth.userID)
	if err != nil || !auth.session || !user.Enabled || user.MustChangePassword || user.DeletedAt != "" || user.AuthVersion != auth.authVersion || user.Role != auth.role {
		return errResourceHidden
	}
	var valid bool
	now := time.Now()
	if err := reader.QueryRow("SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND user_id=? AND auth_version=? AND revoked_at='' AND expires_at>? AND last_active_at>?)", auth.keyID, auth.userID, auth.authVersion, persistedTime(now), persistedTime(now.Add(-sessionIdle))).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errResourceHidden
	}
	return nil
}

func writeBrowseError(response http.ResponseWriter, err error) {
	if errors.Is(err, errBrowseRestart) {
		writeError(response, 409, "BROWSE_RESTART", err.Error())
		return
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, errResourceHidden) {
		writeError(response, 404, "NOT_FOUND", "resource not found")
		return
	}
	writePlaylistMutationError(response, err)
}

// Search predicates are evaluated by SQLite before LIMIT/OFFSET and decoding.
// instr keeps '%' and '_' literal, matching the existing browser search.
const browseTrackSearch = `instr(lower(COALESCE(json_extract(t.payload,'$.title'),'') || ' ' || COALESCE(json_extract(t.payload,'$.artist'),'') || ' ' || COALESCE(json_extract(t.payload,'$.album'),'') || ' ' || COALESCE(json_extract(t.payload,'$.genre'),'')),lower(?))>0`

func browseTracks(tx *sql.Tx, auth authContext, q browseQuery, page *browsePage) error {
	where := "t.removed=0"
	args := []any{}
	if q.ExcludePlaylistID != "" {
		var allowed bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM playlists WHERE id=? AND system=0 AND (owner_id=? OR ?='admin'))", q.ExcludePlaylistID, auth.userID, auth.role).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return errResourceHidden
		}
		where += " AND NOT EXISTS (SELECT 1 FROM playlist_tracks m WHERE m.playlist_id=? AND m.track_id=t.id)"
		args = append(args, q.ExcludePlaylistID)
	}
	switch q.Filter {
	case "missing":
		if auth.role != "admin" {
			return errResourceHidden
		}
		where += " AND t.missing=1"
	case "mine":
		where += " AND t.uploaded_by=?"
		args = append(args, auth.userID)
	case "managed":
		where += " AND t.owner_id=?"
		args = append(args, auth.userID)
	case "orphan":
		where += ` AND NOT EXISTS (SELECT 1 FROM playlists p, json_each(p.payload,'$.track_ids') m WHERE p.owner_id=? AND p.system=0 AND m.value=t.id)`
		args = append(args, auth.userID)
	}
	if q.Search != "" {
		where += " AND " + browseTrackSearch
		args = append(args, q.Search)
	}
	if err := tx.QueryRow("SELECT count(*) FROM tracks t WHERE "+where, args...).Scan(&page.Total); err != nil {
		return err
	}
	items, err := queryJSON[map[string]any](tx, "SELECT json_remove(t.payload,'$.lyrics') FROM tracks t WHERE "+where+" ORDER BY t.title,t.id LIMIT ? OFFSET ?", append(args, q.Limit, q.Offset)...)
	if err != nil {
		return err
	}
	for _, item := range items {
		item["detail_loaded"] = false
		if auth.role != "admin" {
			item["path"] = ""
		}
	}
	page.Items = items
	return nil
}

const browsePlaylistProjection = `CASE WHEN COALESCE(json_array_length(payload,'$.track_ids'),0)<=100 THEN payload ELSE json_remove(payload,'$.track_ids','$.track_notes','$.unavailable_track_ids') END, COALESCE(json_array_length(payload,'$.track_ids'),0)`

func browsePlaylistSummary(raw string, count int) (map[string]any, error) {
	var item map[string]any
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, err
	}
	item["track_count"] = count
	item["members_complete"] = count <= browseMaxLimit
	return item, nil
}

func browsePlaylists(tx *sql.Tx, auth authContext, q browseQuery, page *browsePage) error {
	where := "owner_id=? AND system=0"
	args := []any{auth.userID}
	if q.Search != "" {
		where += ` AND instr(lower(COALESCE(json_extract(payload,'$.name'),'') || ' ' || COALESCE(json_extract(payload,'$.description'),'')),lower(?))>0`
		args = append(args, q.Search)
	}
	if err := tx.QueryRow("SELECT count(*) FROM playlists WHERE "+where, args...).Scan(&page.Total); err != nil {
		return err
	}
	projection := browsePlaylistProjection
	if q.SummaryOnly {
		projection = "json_remove(payload,'$.track_ids','$.track_notes','$.unavailable_track_ids'),COALESCE(json_array_length(payload,'$.track_ids'),0)"
	}
	rows, err := tx.Query("SELECT "+projection+" FROM playlists WHERE "+where+" ORDER BY sort_order,json_extract(payload,'$.name'),id LIMIT ? OFFSET ?", append(args, q.Limit, q.Offset)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var count int
		if err := rows.Scan(&raw, &count); err != nil {
			return err
		}
		item, err := browsePlaylistSummary(raw, count)
		if err != nil {
			return err
		}
		if q.SummaryOnly {
			item["members_complete"] = false
		}
		page.Items = append(page.Items, item)
	}
	return rows.Err()
}

func browsePlaylistTracks(tx *sql.Tx, auth authContext, id string, q browseQuery, page *browsePage) error {
	var raw string
	var count int
	// Ordinary browse intentionally includes only one's own private collection;
	// administrators can inspect another owner only by explicitly naming it.
	if err := tx.QueryRow("SELECT "+browsePlaylistProjection+" FROM playlists WHERE id=? AND (system=1 OR owner_id=? OR ?='admin')", id, auth.userID, auth.role).Scan(&raw, &count); err != nil {
		return err
	}
	playlist, err := browsePlaylistSummary(raw, count)
	if err != nil {
		return err
	}
	page.Playlist = playlist
	where := "m.playlist_id=?"
	args := []any{id}
	if q.Search != "" {
		where += " AND ((t.removed=0 AND t.missing=0 AND " + browseTrackSearch + ") OR instr(lower(m.note),lower(?))>0 OR ((t.removed=1 OR t.missing=1) AND instr(lower('Unavailable'),lower(?))>0))"
		args = append(args, q.Search, q.Search, q.Search)
	}
	if err := tx.QueryRow("SELECT count(*) FROM playlist_tracks m JOIN tracks t ON t.id=m.track_id WHERE "+where, args...).Scan(&page.Total); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT CASE WHEN t.removed=1 OR t.missing=1 THEN json_object('id',t.id,'title','Unavailable','removed',json(CASE WHEN t.removed=1 THEN 'true' ELSE 'false' END),'missing',json(CASE WHEN t.missing=1 THEN 'true' ELSE 'false' END),'version',t.version) ELSE json_remove(t.payload,'$.lyrics') END,m.note FROM playlist_tracks m JOIN tracks t ON t.id=m.track_id WHERE `+where+" ORDER BY m.position LIMIT ? OFFSET ?", append(args, q.Limit, q.Offset)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw, note string
		if err := rows.Scan(&raw, &note); err != nil {
			return err
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return err
		}
		item["detail_loaded"], item["playlist_note"] = false, note
		if auth.role != "admin" {
			if _, present := item["path"]; present {
				item["path"] = ""
			}
		}
		page.Items = append(page.Items, item)
	}
	return rows.Err()
}

type browseMoveRequest struct {
	TrackID          string   `json:"track_id"`
	BeforeID         string   `json:"before_id"`
	ExpectedTrackIDs []string `json:"expected_track_ids"`
	Offset           int      `json:"offset"`
	Limit            int      `json:"limit"`
}

func (a *API) browseMove(response http.ResponseWriter, request *http.Request, id string) {
	if !validID.MatchString(id) {
		writeBrowseError(response, errResourceHidden)
		return
	}
	expected, err := strconv.ParseInt(strings.Trim(request.Header.Get("If-Match"), `"`), 10, 64)
	if err != nil || expected < 1 {
		writeError(response, 428, "VERSION_REQUIRED", "If-Match with current version is required")
		return
	}
	var body browseMoveRequest
	if decodeStrict(request, &body) != nil || body.TrackID == "" || body.Offset < 0 || body.Limit < 0 || len(body.ExpectedTrackIDs) > browseMaxLimit {
		writeError(response, 400, "INVALID_BROWSE_MOVE", "invalid relative move request")
		return
	}
	if body.Limit == 0 {
		body.Limit = 50
	}
	body.Limit = min(body.Limit, browseMaxLimit)
	options := mutationOptions(request)
	auth := authFromRequest(request)
	_, err = a.store.mutatePlaylistActionGuarded(id, true, "playlist.track.move", func(reader sqlReader) error { return browseIdentity(reader, auth) }, func(playlist *Playlist) error {
		if body.ExpectedTrackIDs != nil {
			start := min(body.Offset, len(playlist.TrackIDs))
			end := start + min(body.Limit, len(playlist.TrackIDs)-start)
			if !slices.Equal(playlist.TrackIDs[start:end], body.ExpectedTrackIDs) {
				return errPlaylistConflict
			}
			if !slices.Contains(body.ExpectedTrackIDs, body.TrackID) {
				return errors.New("moving track must belong to the expected page")
			}
		}
		index := slices.Index(playlist.TrackIDs, body.TrackID)
		if index < 0 || body.BeforeID != "" && !slices.Contains(playlist.TrackIDs, body.BeforeID) {
			return errMemberNotFound
		}
		if body.BeforeID == body.TrackID {
			return nil
		}
		playlist.TrackIDs = slices.Delete(playlist.TrackIDs, index, index+1)
		before := len(playlist.TrackIDs)
		if body.BeforeID != "" {
			before = slices.Index(playlist.TrackIDs, body.BeforeID)
		}
		playlist.TrackIDs = slices.Insert(playlist.TrackIDs, before, body.TrackID)
		return nil
	}, options)
	if err != nil {
		writeBrowseError(response, err)
		return
	}
	// Return the current bounded member page, never the full mutation object.
	values := request.URL.Query()
	values.Set("offset", strconv.Itoa(body.Offset))
	values.Set("limit", strconv.Itoa(body.Limit))
	values.Del("revision")
	read := request.Clone(request.Context())
	read.Method = http.MethodGet
	read.URL.Path = "/api/v2/browse/playlists/" + id + "/tracks"
	read.URL.RawQuery = values.Encode()
	a.browse(response, read)
}
