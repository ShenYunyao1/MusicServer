package music

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"musicserver/internal/version"
)

//go:embed index.html
var managementPage []byte

type API struct {
	store  *Store
	apiKey string
}

type authContext struct {
	role             string
	allowedPlaylists map[string]bool
	legacy           bool
}

func NewAPI(store *Store, apiKey string) *API {
	return &API{store: store, apiKey: apiKey}
}

func (a *API) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("X-Content-Type-Options", "nosniff")
	requestID, err := randomToken(8)
	if err == nil {
		response.Header().Set("X-Request-ID", requestID)
	}
	if request.URL.Path == "/" && request.Method == http.MethodGet {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write(managementPage)
		return
	}
	if request.URL.Path != "/api/v1/uploads" {
		request.Body = http.MaxBytesReader(response, request.Body, 2<<20)
	}
	if request.URL.Path != "/api/v1/health" {
		auth, ok := a.authenticate(request)
		if !ok {
			writeErrorWithID(response, http.StatusUnauthorized, "AUTH_INVALID", "authentication required", requestID)
			return
		}
		request = request.WithContext(withAuth(request.Context(), auth))
	}
	if request.URL.Path == "/api/v1/auth/keys" || strings.HasPrefix(request.URL.Path, "/api/v1/auth/keys/") {
		a.keys(response, request)
		return
	}
	switch {
	case request.URL.Path == "/api/v1/health":
		if request.Method != http.MethodGet {
			writeError(response, 405, "method not allowed")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "version": version.Version, "revision": a.store.Revision()})
	case request.URL.Path == "/api/v1/sync":
		a.sync(response, request)
	case request.URL.Path == "/api/v1/playlists":
		a.playlists(response, request)
	case request.URL.Path == "/api/v1/tracks":
		if request.Method != http.MethodGet {
			writeError(response, 405, "method not allowed")
			return
		}
		tracks := a.store.Tracks()
		if authFromRequest(request).role != "admin" {
			filtered := make([]Track, 0, len(tracks))
			for _, track := range tracks {
				if canReadTrack(authFromRequest(request), a.store, track.ID) {
					filtered = append(filtered, track)
				}
			}
			tracks = filtered
		}
		writeJSON(response, http.StatusOK, tracks)
	case request.URL.Path == "/api/v1/uploads":
		a.upload(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/playlists/"):
		a.playlist(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/tracks/"):
		a.track(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/media/"):
		a.media(response, request)
	case request.URL.Path == "/api/v1/admin/scan":
		a.scan(response, request)
	default:
		writeErrorWithID(response, http.StatusNotFound, "NOT_FOUND", "endpoint not found", requestID)
	}
}

type authContextKey struct{}

func withAuth(ctx context.Context, auth authContext) context.Context {
	return context.WithValue(ctx, authContextKey{}, auth)
}

// authFromRequest is kept simple because handlers are all called synchronously
// from ServeHTTP; the context value is attached through the request context.
func authFromRequest(request *http.Request) authContext {
	if value, ok := request.Context().Value(authContextKey{}).(authContext); ok {
		return value
	}
	return authContext{role: "admin", legacy: true}
}

func (a *API) authenticate(request *http.Request) (authContext, bool) {
	provided := request.Header.Get("X-API-Key")
	if provided == "" {
		provided = strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	}
	if a.apiKey == "" && provided == "" && len(a.store.APIKeys()) == 0 {
		return authContext{role: "admin", legacy: true}, true
	}
	if provided != "" && a.apiKey != "" && provided == a.apiKey {
		return authContext{role: "admin", legacy: true}, true
	}
	key, ok := a.store.AuthenticateSecret(provided)
	if !ok {
		return authContext{}, false
	}
	allowed := map[string]bool{}
	for _, id := range key.AllowedPlaylistIDs {
		allowed[id] = true
	}
	return authContext{role: key.Role, allowedPlaylists: allowed}, true
}

func (a *API) requireAdmin(response http.ResponseWriter, request *http.Request) bool {
	if authFromRequest(request).role == "admin" {
		return true
	}
	writeError(response, http.StatusForbidden, "ADMIN_REQUIRED", "administrator access required")
	return false
}

func canReadPlaylist(auth authContext, id string) bool {
	return auth.role == "admin" || auth.allowedPlaylists[id]
}

func canReadTrack(auth authContext, store *Store, id string) bool {
	if auth.role == "admin" {
		return true
	}
	for _, playlist := range store.Playlists() {
		if !canReadPlaylist(auth, playlist.ID) {
			continue
		}
		for _, trackID := range playlist.TrackIDs {
			if trackID == id {
				return true
			}
		}
	}
	return false
}

func (a *API) sync(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(response, 405, "method not allowed")
		return
	}
	since := int64(-1)
	if value := request.URL.Query().Get("since"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			writeError(response, 400, "INVALID_REVISION", "invalid revision")
			return
		}
		since = parsed
	}
	result := a.store.Sync(since)
	auth := authFromRequest(request)
	visiblePlaylists := make([]Playlist, 0, len(result.Playlists))
	visibleTracks := map[string]bool{}
	for _, playlist := range result.Playlists {
		if !canReadPlaylist(auth, playlist.ID) {
			continue
		}
		visiblePlaylists = append(visiblePlaylists, playlist)
		for _, id := range playlist.TrackIDs {
			visibleTracks[id] = true
		}
	}
	filtered := result
	filtered.Playlists = visiblePlaylists
	filtered.Changed = make([]Track, 0, len(result.Changed))
	for _, track := range result.Changed {
		if auth.role == "admin" || visibleTracks[track.ID] {
			filtered.Changed = append(filtered.Changed, track)
		}
	}
	writeJSON(response, http.StatusOK, filtered)
}

func (a *API) playlists(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		playlists := make([]Playlist, 0)
		for _, playlist := range a.store.Playlists() {
			if canReadPlaylist(authFromRequest(request), playlist.ID) {
				playlists = append(playlists, playlist)
			}
		}
		writeJSON(response, http.StatusOK, playlists)
	case http.MethodPost:
		if !a.requireAdmin(response, request) {
			return
		}
		var playlist Playlist
		if err := json.NewDecoder(request.Body).Decode(&playlist); err != nil {
			writeError(response, http.StatusBadRequest, "invalid playlist JSON")
			return
		}
		if err := a.store.PutPlaylist(playlist); err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		stored, _ := a.store.Playlist(playlist.ID)
		writeJSON(response, http.StatusCreated, stored)
	default:
		writeError(response, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *API) playlist(response http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/api/v1/playlists/")
	playlist, ok := a.store.Playlist(id)
	if !ok {
		writeError(response, http.StatusNotFound, "playlist not found")
		return
	}
	if !canReadPlaylist(authFromRequest(request), id) {
		writeError(response, http.StatusForbidden, "PLAYLIST_ACCESS_DENIED", "current key cannot access this playlist")
		return
	}
	if request.Method == http.MethodPut {
		if !a.requireAdmin(response, request) {
			return
		}
		var replacement Playlist
		if err := json.NewDecoder(request.Body).Decode(&replacement); err != nil {
			writeError(response, http.StatusBadRequest, "invalid playlist JSON")
			return
		}
		replacement.ID = id
		if err := a.store.PutPlaylist(replacement); err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		stored, _ := a.store.Playlist(id)
		writeJSON(response, http.StatusOK, stored)
		return
	}
	if request.Method == http.MethodDelete {
		if !a.requireAdmin(response, request) {
			return
		}
		if err := a.store.DeletePlaylist(id); err != nil {
			writeError(response, 400, err.Error())
			return
		}
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodGet {
		writeError(response, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tracks := make([]Track, 0, len(playlist.TrackIDs))
	for _, trackID := range playlist.TrackIDs {
		if track, exists := a.store.Track(trackID); exists {
			tracks = append(tracks, track)
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"playlist": playlist, "tracks": tracks})
}

func (a *API) track(response http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/api/v1/tracks/")
	track, ok := a.store.Track(id)
	if !ok {
		writeError(response, http.StatusNotFound, "TRACK_NOT_FOUND", "track not found")
		return
	}
	if request.Method == http.MethodGet && !canReadTrack(authFromRequest(request), a.store, id) {
		writeError(response, http.StatusForbidden, "TRACK_ACCESS_DENIED", "current key cannot access this track")
		return
	}
	if request.Method == http.MethodPatch {
		if !a.requireAdmin(response, request) {
			return
		}
		fields := map[string]string{}
		if err := json.NewDecoder(request.Body).Decode(&fields); err != nil {
			writeError(response, 400, "invalid metadata JSON")
			return
		}
		if err := a.store.UpdateTrack(id, fields); err != nil {
			writeError(response, 400, err.Error())
			return
		}
		updated, _ := a.store.Track(id)
		writeJSON(response, http.StatusOK, updated)
		return
	}
	if request.Method == http.MethodDelete {
		if !a.requireAdmin(response, request) {
			return
		}
		if err := a.store.DeleteTrack(id); err != nil {
			writeError(response, 500, err.Error())
			return
		}
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodGet {
		writeError(response, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(response, http.StatusOK, track)
}

func (a *API) media(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeError(response, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(request.URL.Path, "/api/v1/media/")
	track, ok := a.store.Track(id)
	if !ok {
		writeError(response, http.StatusNotFound, "TRACK_NOT_FOUND", "track not found")
		return
	}
	if !canReadTrack(authFromRequest(request), a.store, id) {
		writeError(response, http.StatusForbidden, "TRACK_ACCESS_DENIED", "current key cannot access this track")
		return
	}
	_, err := a.store.MediaPath(track)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	root, err := os.OpenRoot(a.store.mediaRoot)
	if err != nil {
		writeError(response, 500, "open media root failed")
		return
	}
	defer root.Close()
	file, err := root.Open(track.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(response, http.StatusNotFound, "media file not found")
		} else {
			writeError(response, http.StatusInternalServerError, "open media file failed")
		}
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "stat media file failed")
		return
	}
	response.Header().Set("Content-Type", contentType(track.Format))
	response.Header().Set("ETag", fmt.Sprintf("\"%s\"", track.ContentHash))
	http.ServeContent(response, request, track.Path, info.ModTime(), file)
}

func (a *API) scan(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeError(response, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	count, err := a.store.Scan()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"tracks": count, "revision": a.store.Revision()})
}

func (a *API) upload(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeError(response, 405, "method not allowed")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 512<<20)
	if err := request.ParseMultipartForm(8 << 20); err != nil {
		writeError(response, 400, "invalid upload or file exceeds 512 MiB")
		return
	}
	defer request.MultipartForm.RemoveAll()
	file, header, err := request.FormFile("file")
	if err != nil {
		writeError(response, 400, "file is required")
		return
	}
	defer file.Close()
	metadata := map[string]string{}
	for _, field := range []string{"title", "artist", "album", "genre", "lyrics", "comment"} {
		if value := strings.TrimSpace(request.FormValue(field)); value != "" {
			metadata[field] = value
		}
	}
	track, err := a.store.ImportAudioWithMetadata(header.Filename, file, metadata)
	if err != nil {
		writeError(response, 400, err.Error())
		return
	}
	writeJSON(response, http.StatusCreated, track)
}

func (a *API) keys(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/v1/auth/keys")
	if path == "" || path == "/" {
		switch request.Method {
		case http.MethodGet:
			views := make([]APIKeyView, 0)
			for _, key := range a.store.APIKeys() {
				views = append(views, APIKeyView{APIKey: publicAPIKey(key)})
			}
			writeJSON(response, http.StatusOK, views)
		case http.MethodPost:
			var input APIKeyCreateRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				writeError(response, http.StatusBadRequest, "INVALID_KEY", "invalid key JSON")
				return
			}
			created, err := a.store.CreateAPIKey(input)
			if err != nil {
				writeError(response, http.StatusBadRequest, "INVALID_KEY", err.Error())
				return
			}
			created.APIKey = publicAPIKey(created.APIKey)
			writeJSON(response, http.StatusCreated, created)
		default:
			writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	id := parts[0]
	if len(parts) == 2 && parts[1] == "rotate" {
		if request.Method != http.MethodPost {
			writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		rotated, err := a.store.RotateAPIKey(id)
		if errors.Is(err, os.ErrNotExist) {
			writeError(response, http.StatusNotFound, "KEY_NOT_FOUND", "key not found")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "KEY_ROTATE_FAILED", err.Error())
			return
		}
		rotated.APIKey = publicAPIKey(rotated.APIKey)
		writeJSON(response, http.StatusOK, rotated)
		return
	}
	if len(parts) == 1 && request.Method == http.MethodPatch {
		var input APIKeyUpdateRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			writeError(response, http.StatusBadRequest, "INVALID_KEY", "invalid key JSON")
			return
		}
		updated, err := a.store.UpdateAPIKey(id, input)
		if errors.Is(err, os.ErrNotExist) {
			writeError(response, http.StatusNotFound, "KEY_NOT_FOUND", "key not found")
			return
		}
		if err != nil {
			writeError(response, http.StatusBadRequest, "INVALID_KEY", err.Error())
			return
		}
		writeJSON(response, http.StatusOK, APIKeyView{APIKey: publicAPIKey(updated)})
		return
	}
	if request.Method != http.MethodDelete {
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if err := a.store.RevokeAPIKey(id); errors.Is(err, os.ErrNotExist) {
		writeError(response, http.StatusNotFound, "KEY_NOT_FOUND", "key not found")
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "KEY_REVOKE_FAILED", err.Error())
	} else {
		response.WriteHeader(http.StatusNoContent)
	}
}

func contentType(format string) string {
	contentTypes := map[string]string{"aac": "audio/aac", "flac": "audio/flac", "m4a": "audio/mp4", "mp3": "audio/mpeg", "ogg": "audio/ogg", "opus": "audio/opus", "wav": "audio/wav", "wma": "audio/x-ms-wma"}
	if value, ok := contentTypes[format]; ok {
		return value
	}
	return "application/octet-stream"
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, args ...string) {
	code, message := "REQUEST_FAILED", "request failed"
	if len(args) == 1 {
		message = args[0]
	} else if len(args) >= 2 {
		code, message = args[0], args[1]
	}
	errorValue := map[string]string{"code": code, "message": message}
	if requestID := response.Header().Get("X-Request-ID"); requestID != "" {
		errorValue["request_id"] = requestID
	}
	writeJSON(response, status, map[string]any{"error": errorValue})
}

func writeErrorWithID(response http.ResponseWriter, status int, code, message, requestID string) {
	writeJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": requestID}})
}
