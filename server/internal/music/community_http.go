package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type PublicPlaylistSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	CoverID         string `json:"cover_id,omitempty"`
	CoverMIME       string `json:"cover_mime,omitempty"`
	OwnerID         string `json:"owner_id"`
	SourceOwnerName string `json:"source_owner_name"`
	TrackCount      int    `json:"track_count"`
	PublishedAt     string `json:"published_at"`
}
type PublicCatalog struct {
	Items    []PublicPlaylistSummary `json:"items"`
	Total    int                     `json:"total"`
	Offset   int                     `json:"offset"`
	Limit    int                     `json:"limit"`
	Revision string                  `json:"revision"`
}

func matchesETag(header, etag string) bool {
	for _, value := range strings.Split(header, ",") {
		value = strings.TrimSpace(value)
		if value == "*" || strings.TrimPrefix(value, "W/") == etag {
			return true
		}
	}
	return false
}
func (s *Store) publicCatalog(query string, offset, limit int, conditional string) (PublicCatalog, string, bool, error) {
	page := PublicCatalog{Items: []PublicPlaylistSummary{}, Offset: offset, Limit: limit}
	etag := ""
	unchanged := false
	err := s.transaction(func(tx *sql.Tx) error {
		if err := tx.QueryRow("SELECT value FROM meta WHERE key='public_revision'").Scan(&page.Revision); err != nil {
			return err
		}
		binding, _ := json.Marshal([]any{page.Revision, query, offset, limit})
		etag = `"public-` + hashBytes(binding) + `"`
		if matchesETag(conditional, etag) {
			unchanged = true
			return nil
		}
		filter := ` FROM playlists p JOIN users u ON u.id=p.owner_id WHERE p.visibility='public' AND u.enabled=1 AND instr(lower(COALESCE(json_extract(p.payload,'$.name'),'')||' '||COALESCE(json_extract(p.payload,'$.description'),'')||' '||u.display_name),lower(?))>0`
		if err := tx.QueryRow("SELECT count(*)"+filter, query).Scan(&page.Total); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT p.id,json_extract(p.payload,'$.name'),COALESCE(json_extract(p.payload,'$.description'),''),COALESCE(json_extract(p.payload,'$.cover_id'),''),COALESCE(json_extract(p.payload,'$.cover_mime'),''),p.owner_id,u.display_name,(SELECT count(*) FROM playlist_tracks m WHERE m.playlist_id=p.id),p.published_at`+filter+" ORDER BY p.published_at DESC,p.id LIMIT ? OFFSET ?", query, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p PublicPlaylistSummary
			if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CoverID, &p.CoverMIME, &p.OwnerID, &p.SourceOwnerName, &p.TrackCount, &p.PublishedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, p)
		}
		return rows.Err()
	})
	return page, etag, unchanged, err
}
func (a *API) community(response http.ResponseWriter, request *http.Request) {
	if !a.requireAccount(response, request) {
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/v2/")
	if path == "public/playlists" {
		if request.Method != "GET" {
			writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		offset, limit := 0, 30
		var err error
		if value := request.URL.Query().Get("offset"); value != "" {
			offset, err = strconv.Atoi(value)
			if err != nil || offset < 0 {
				writeError(response, 400, "INVALID_PAGE", "offset must be non-negative")
				return
			}
		}
		if value := request.URL.Query().Get("limit"); value != "" {
			limit, err = strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 100 {
				writeError(response, 400, "INVALID_PAGE", "limit must be 1..100")
				return
			}
		}
		query := strings.TrimSpace(request.URL.Query().Get("q"))
		if len([]rune(query)) > 128 {
			writeError(response, 400, "INVALID_QUERY", "query is too long")
			return
		}
		page, etag, unchanged, err := a.store.publicCatalog(query, offset, limit, request.Header.Get("If-None-Match"))
		if err != nil {
			writeError(response, 500, "READ_FAILED", "unable to read public catalog")
			return
		}
		response.Header().Set("ETag", etag)
		response.Header().Set("Cache-Control", "private, no-cache")
		response.Header().Set("Vary", "Cookie")
		if unchanged {
			response.WriteHeader(304)
			return
		}
		writeJSON(response, 200, page)
		return
	}
	if strings.HasPrefix(path, "public/playlists/") {
		if request.Method != "GET" {
			writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		id := strings.TrimPrefix(path, "public/playlists/")
		if !validID.MatchString(id) {
			writeError(response, 404, "NOT_FOUND", "playlist not found")
			return
		}
		var view Playlist
		tracks := []Track{}
		err := a.store.transaction(func(tx *sql.Tx) error {
			p, err := readPlaylistFrom(tx, id)
			if errors.Is(err, sql.ErrNoRows) || err == nil && !publishedPlaylist(tx, p) {
				return errResourceHidden
			}
			if err != nil {
				return err
			}
			view, err = publicPlaylistView(tx, p, authFromRequest(request).userID)
			if err != nil {
				return err
			}
			for _, id := range p.TrackIDs {
				track, err := readTrackFrom(tx, id, true)
				if err != nil {
					return err
				}
				if track.Removed || track.Missing {
					view.UnavailableTrackIDs = append(view.UnavailableTrackIDs, id)
					track = unavailableTrack(track)
				}
				tracks = append(tracks, publicTrack(track, authContext{role: "member"}))
			}
			return nil
		})
		if err != nil {
			writePlaylistMutationError(response, err)
			return
		}
		writeJSON(response, 200, map[string]any{"playlist": view, "tracks": tracks})
		return
	}
	if path == "subscriptions" {
		if request.Method != "GET" {
			writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		views, err := a.store.Subscriptions(authFromRequest(request).userID)
		if err != nil {
			writeError(response, 500, "READ_FAILED", "unable to read subscriptions")
			return
		}
		writeJSON(response, 200, views)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "subscriptions/"), "/")
	if len(parts) < 1 || !validID.MatchString(parts[0]) {
		writeError(response, 404, "NOT_FOUND", "subscription not found")
		return
	}
	options := mutationOptions(request)
	if len(parts) == 1 {
		switch request.Method {
		case "PUT":
			view, err := a.store.Subscribe(parts[0], options)
			if err != nil {
				writePlaylistMutationError(response, err)
				return
			}
			writeJSON(response, 200, view)
		case "DELETE":
			if err := a.store.Unsubscribe(parts[0], options); err != nil {
				writePlaylistMutationError(response, err)
				return
			}
			response.WriteHeader(204)
		default:
			writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		}
		return
	}
	if len(parts) == 3 && parts[1] == "tracks" && validID.MatchString(parts[2]) && request.Method == "PATCH" {
		if options.ExpectedVersion < 1 {
			writeError(response, 428, "VERSION_REQUIRED", "If-Match with subscription version is required")
			return
		}
		var body struct {
			Note string `json:"note"`
		}
		if decodeStrict(request, &body) != nil {
			writeError(response, 400, "INVALID_NOTE", "invalid note")
			return
		}
		view, err := a.store.UpdateSubscriptionNote(parts[0], parts[2], body.Note, options)
		if err != nil {
			writePlaylistMutationError(response, err)
			return
		}
		writeJSON(response, 200, view)
		return
	}
	writeError(response, 404, "NOT_FOUND", "subscription endpoint not found")
}
