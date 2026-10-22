package music

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"musicserver/internal/update"
	"musicserver/internal/version"
)

//go:embed index.html
var managementPage []byte

//go:embed vendor/jsmediatags-3.9.7.min.js
var browserTagReader []byte

//go:embed account.js
var accountPageScript []byte

type API struct {
	store         *Store
	updater       *update.Manager
	concurrencyMu sync.Mutex
	active        map[string]int
	authConfig    AuthConfig
	hashSlots     chan struct{}
}

type authContext struct {
	userID           string
	authVersion      int64
	session          bool
	csrf             string
	username         string
	role             string
	allowedPlaylists map[string]bool
	scopes           []string
	keyID            string
	maxConcurrency   int
	download         bool
	maxDownloads     int
}

func NewAPI(store *Store) *API {
	api := &API{store: store, active: map[string]int{}, hashSlots: make(chan struct{}, 2)}
	return api
}

func (a *API) SetUpdater(manager *update.Manager) { a.updater = manager }

func (a *API) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	isAPIRequest := strings.HasPrefix(request.URL.Path, "/api/v2/")
	trackCall := isAPIRequest && request.URL.Path != "/api/v2/health"
	started := time.Now()
	callKeyID := ""
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	var tracked *trackingResponseWriter
	if trackCall {
		tracked = &trackingResponseWriter{ResponseWriter: response}
		response = tracked
		defer func() {
			status := tracked.status
			if status == 0 {
				status = http.StatusOK
			}
			a.recordCall(callKeyID, status, tracked.bytes, time.Since(started))
		}()
	}
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if isAPIRequest {
		response.Header().Set("Cache-Control", "no-store")
	}
	requestID, err := randomToken(8)
	if err == nil {
		response.Header().Set("X-Request-ID", requestID)
	}
	if request.URL.Path == "/" && request.Method == http.MethodGet {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write(managementPage)
		return
	}
	if request.URL.Path == "/assets/jsmediatags-3.9.7.min.js" && request.Method == http.MethodGet {
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = response.Write(browserTagReader)
		return
	}
	if request.URL.Path == "/assets/account.js" && request.Method == http.MethodGet {
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache")
		_, _ = response.Write(accountPageScript)
		return
	}
	resourceUpload := strings.HasPrefix(request.URL.Path, "/api/v2/tracks/") && (strings.HasSuffix(request.URL.Path, "/cover") || strings.HasSuffix(request.URL.Path, "/lyrics"))
	if !strings.HasPrefix(request.URL.Path, "/api/v2/uploads") && !resourceUpload {
		request.Body = http.MaxBytesReader(response, request.Body, 2<<20)
	}
	var auth authContext
	if request.URL.Path == "/api/v2/auth/login" {
		a.accountAuth(response, request)
		return
	}
	if request.URL.Path != "/api/v2/health" {
		var ok bool
		auth, ok = a.authenticate(request)
		if !ok {
			writeErrorWithID(response, http.StatusUnauthorized, "AUTH_INVALID", "authentication required", requestID)
			return
		}
		request = request.WithContext(withAuth(request.Context(), auth))
		if !auth.session {
			callKeyID = auth.keyID
		}
		if auth.session && request.Method != "GET" && request.Method != "HEAD" {
			if !a.validOrigin(request) || subtle.ConstantTimeCompare([]byte(auth.csrf), []byte(request.Header.Get("X-CSRF-Token"))) != 1 {
				writeError(response, 403, "CSRF_DENIED", "same-origin CSRF token required")
				return
			}
		}
		if !auth.session {
			if !playerEndpoint(request) {
				writeError(response, 403, "SESSION_REQUIRED", "management requires an account session")
				return
			}
			auth.download = strings.HasPrefix(request.URL.Path, "/api/v2/media/") || strings.HasPrefix(request.URL.Path, "/api/v2/covers/")
			if !a.acquire(auth) {
				response.Header().Set("Retry-After", "1")
				writeError(response, http.StatusTooManyRequests, "KEY_CONCURRENCY_LIMIT", "key concurrency limit reached")
				return
			}
			release = func() { a.release(auth) }
			required := endpointScope(request.URL.Path, request.Method)
			if required != "" && !hasScope(auth, required) {
				writeError(response, http.StatusForbidden, "SCOPE_DENIED", "API key does not grant the required scope")
				return
			}
		}
	}
	if strings.HasPrefix(request.URL.Path, "/api/v2/auth/") && !strings.HasPrefix(request.URL.Path, "/api/v2/auth/keys") {
		a.accountAuth(response, request)
		return
	}
	if needsVersion(request) {
		expected, err := strconv.ParseInt(strings.Trim(request.Header.Get("If-Match"), string(rune(34))), 10, 64)
		if err != nil || expected < 1 {
			writeError(response, 428, "VERSION_REQUIRED", "If-Match with current version is required")
			return
		}
	}
	if request.URL.Path == "/api/v2/auth/keys" || strings.HasPrefix(request.URL.Path, "/api/v2/auth/keys/") {
		a.keys(response, request)
		return
	}
	switch {
	case request.URL.Path == "/api/v2/health":
		if request.Method != http.MethodGet {
			writeError(response, 405, "method not allowed")
			return
		}
		commit, modified := version.Source()
		writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "version": version.Version, "revision": a.store.Revision(), "commit": commit, "modified": modified})
	case request.URL.Path == "/api/v2/sync":
		a.sync(response, request)
	case request.URL.Path == "/api/v2/playlists":
		a.playlists(response, request)
	case request.URL.Path == "/api/v2/playlists/order":
		a.playlistOrder(response, request)
	case request.URL.Path == "/api/v2/admin/removed" || strings.HasPrefix(request.URL.Path, "/api/v2/admin/removed/") || request.URL.Path == "/api/v2/admin/audit" || request.URL.Path == "/api/v2/admin/backups" || request.URL.Path == "/api/v2/admin/scan/status" || request.URL.Path == "/api/v2/admin/scan/cancel":
		a.maintenance(response, request)
	case request.URL.Path == "/api/v2/tracks":
		if request.Method != http.MethodGet {
			writeError(response, 405, "method not allowed")
			return
		}
		tracks := a.store.Tracks()
		if authFromRequest(request).role != "admin" {
			filtered := make([]Track, 0, len(tracks))
			for _, track := range tracks {
				if canReadTrack(authFromRequest(request), a.store, track.ID) {
					filtered = append(filtered, publicTrack(track, authFromRequest(request)))
				}
			}
			tracks = filtered
		}
		writeJSON(response, http.StatusOK, tracks)
	case request.URL.Path == "/api/v2/uploads":
		a.upload(response, request)
	case request.URL.Path == "/api/v2/uploads/stage":
		a.stageUpload(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v2/uploads/stage/"):
		a.stagedUpload(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v2/playlists/"):
		a.playlist(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v2/tracks/"):
		a.track(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v2/media/"):
		a.media(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v2/covers/"):
		a.cover(response, request)
	case request.URL.Path == "/api/v2/admin/scan":
		a.scan(response, request)
	case request.URL.Path == "/api/v2/admin/stats":
		a.adminStats(response, request)
	case request.URL.Path == "/api/v2/admin/settings":
		a.adminSettings(response, request)
	case request.URL.Path == "/api/v2/admin/update" || request.URL.Path == "/api/v2/admin/update/check":
		a.adminUpdate(response, request)
	default:
		writeErrorWithID(response, http.StatusNotFound, "NOT_FOUND", "endpoint not found", requestID)
	}
}

func (a *API) playlistOrder(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	if request.Method != http.MethodPut {
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var body struct {
		Order         []string `json:"order"`
		ExpectedOrder []string `json:"expected_order"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_ORDER", "invalid playlist order JSON")
		return
	}
	if body.ExpectedOrder == nil {
		writeError(response, 428, "ORDER_REQUIRED", "expected_order is required")
		return
	}
	options := mutationOptions(request)
	options.ExpectedOrder = body.ExpectedOrder
	if err := a.store.ReorderPlaylists(body.Order, options); err != nil {
		writePlaylistMutationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, a.store.Playlists())
}

func (a *API) adminSettings(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(response, http.StatusOK, a.store.Settings())
	case http.MethodPatch, http.MethodPut:
		settings := a.store.Settings()
		if err := json.NewDecoder(request.Body).Decode(&settings); err != nil {
			writeError(response, http.StatusBadRequest, "INVALID_SETTINGS", "invalid settings JSON")
			return
		}
		if err := a.store.UpdateSettings(settings, mutationOptions(request)); err != nil {
			writeError(response, http.StatusBadRequest, "INVALID_SETTINGS", err.Error())
			return
		}
		if a.updater != nil {
			a.updater.Trigger()
		}
		writeJSON(response, http.StatusOK, a.store.Settings())
	default:
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (a *API) adminUpdate(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	if a.updater == nil {
		writeError(response, http.StatusServiceUnavailable, "UPDATE_UNAVAILABLE", "update manager is unavailable")
		return
	}
	if request.URL.Path == "/api/v2/admin/update" && request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, a.updater.Status())
		return
	}
	if request.URL.Path == "/api/v2/admin/update/check" && request.Method == http.MethodPost {
		if !a.updater.Trigger() {
			writeError(response, http.StatusConflict, "UPDATE_BUSY", "update check is already running")
			return
		}
		writeJSON(response, http.StatusAccepted, a.updater.Status())
		return
	}
	writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
}

func (a *API) cover(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	id := strings.TrimPrefix(request.URL.Path, "/api/v2/covers/")
	if id == "" || strings.ContainsAny(id, "/\\") {
		writeError(response, http.StatusBadRequest, "INVALID_COVER", "invalid cover id")
		return
	}
	auth := authFromRequest(request)
	if auth.role != "admin" {
		allowed := false
		for _, track := range a.store.Tracks() {
			if track.CoverID == id && canReadTrack(auth, a.store, track.ID) {
				allowed = true
				break
			}
		}
		if !allowed {
			writeError(response, http.StatusNotFound, "COVER_NOT_FOUND", "cover not found")
			return
		}
	}
	asset, ok := a.store.Cover(id)
	if !ok {
		writeError(response, http.StatusNotFound, "COVER_NOT_FOUND", "cover not found")
		return
	}
	response.Header().Set("Content-Type", asset.MIME)
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Vary", "X-API-Key, Authorization, Cookie")
	response.Header().Set("ETag", fmt.Sprintf("\"%s\"", id))
	if request.Method == http.MethodHead {
		response.WriteHeader(http.StatusOK)
		return
	}
	_, _ = response.Write(asset.Data)
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
	return authContext{}
}

func (a *API) authenticate(request *http.Request) (authContext, bool) {
	provided := request.Header.Get("X-API-Key")
	_, keyHeader := request.Header["X-Api-Key"]
	if provided != "" || keyHeader || request.Header.Get("Authorization") != "" {
		if provided == "" {
			provided = strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		}
		key, ok := a.store.AuthenticateSecret(provided)
		if !ok {
			return authContext{}, false
		}
		user, err := readUser(a.store.db, key.UserID)
		if err != nil || !user.Enabled {
			return authContext{}, false
		}
		allowed := map[string]bool{}
		for _, id := range key.AllowedPlaylistIDs {
			playlist, exists := a.store.Playlist(id)
			if exists && (user.Role == "admin" || !playlist.System && playlist.OwnerID == user.ID) {
				allowed[id] = true
			}
		}
		return authContext{role: "player", userID: user.ID, authVersion: user.AuthVersion, username: user.Username, allowedPlaylists: allowed, scopes: cloneStrings(key.Scopes), keyID: key.ID, maxConcurrency: key.MaxConcurrency, maxDownloads: key.MaxDownloads}, true
	}
	cookie, err := request.Cookie(sessionCookie)
	if err != nil {
		return authContext{}, false
	}
	user, session, ok := a.store.sessionIdentity(cookie.Value)
	if !ok {
		return authContext{}, false
	}
	allowed := map[string]bool{}
	for _, playlist := range a.store.Playlists() {
		if !playlist.System && playlist.OwnerID == user.ID {
			allowed[playlist.ID] = true
		}
	}
	return authContext{role: user.Role, userID: user.ID, authVersion: user.AuthVersion, username: user.Username, session: true, csrf: csrfForToken(cookie.Value), allowedPlaylists: allowed, keyID: session.ID}, true
}

func playerEndpoint(request *http.Request) bool {
	if request.Method != "GET" && request.Method != "HEAD" {
		return false
	}
	path := request.URL.Path
	return path == "/api/v2/sync" || path == "/api/v2/playlists" || strings.HasPrefix(path, "/api/v2/playlists/") || path == "/api/v2/tracks" || strings.HasPrefix(path, "/api/v2/tracks/") || strings.HasPrefix(path, "/api/v2/media/") || strings.HasPrefix(path, "/api/v2/covers/")
}

func (a *API) requireAdmin(response http.ResponseWriter, request *http.Request) bool {
	auth := authFromRequest(request)
	if auth.session && auth.role == "admin" {
		return true
	}
	writeError(response, http.StatusForbidden, "ADMIN_REQUIRED", "administrator access required")
	return false
}

func (a *API) acquire(auth authContext) bool {
	limit := auth.maxConcurrency
	if auth.download {
		limit = auth.maxDownloads
		auth.keyID += "/downloads"
	}
	if limit < 1 {
		limit = 1
	}
	a.concurrencyMu.Lock()
	defer a.concurrencyMu.Unlock()
	if a.active == nil {
		a.active = map[string]int{}
	}
	if a.active[auth.keyID] >= limit {
		return false
	}
	a.active[auth.keyID]++
	return true
}

func (a *API) release(auth authContext) {
	if auth.download {
		auth.keyID += "/downloads"
	}
	a.concurrencyMu.Lock()
	defer a.concurrencyMu.Unlock()
	if count := a.active[auth.keyID]; count <= 1 {
		delete(a.active, auth.keyID)
	} else {
		a.active[auth.keyID] = count - 1
	}
}

func (a *API) activeRequests(keyID string) int {
	a.concurrencyMu.Lock()
	defer a.concurrencyMu.Unlock()
	return a.active[keyID] + a.active[keyID+"/downloads"]
}

func canReadPlaylist(auth authContext, id string) bool {
	return auth.role == "admin" || auth.allowedPlaylists[id]
}

func canReadTrack(auth authContext, store *Store, id string) bool {
	if auth.role == "admin" {
		return true
	}
	for playlistID, allowed := range auth.allowedPlaylists {
		if !allowed {
			continue
		}
		var exists bool
		if store.db.QueryRow("SELECT EXISTS(SELECT 1 FROM playlist_tracks WHERE playlist_id=? AND track_id=?)", playlistID, id).Scan(&exists) == nil && exists {
			return true
		}
	}
	return false
}

func (a *API) sync(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(response, 405, "method not allowed")
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	result, err := a.store.SyncPage(authFromRequest(request), request.URL.Query().Get("cursor"), limit)
	if err != nil {
		if errors.Is(err, errSyncRestart) {
			writeError(response, 409, "SYNC_RESTART", err.Error())
		} else {
			writeError(response, 400, "INVALID_CURSOR", err.Error())
		}
		return
	}
	writeJSON(response, http.StatusOK, result)
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
		options := mutationOptions(request)
		options.CreateOnly = true
		if err := a.store.PutPlaylist(playlist, options); err != nil {
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
	id := strings.TrimPrefix(request.URL.Path, "/api/v2/playlists/")
	if strings.Contains(id, "/tracks") {
		a.playlistTracks(response, request, id)
		return
	}
	playlist, ok := a.store.Playlist(id)
	if !ok {
		writeError(response, http.StatusNotFound, "playlist not found")
		return
	}
	if !canReadPlaylist(authFromRequest(request), id) {
		writeError(response, http.StatusNotFound, "PLAYLIST_NOT_FOUND", "playlist not found")
		return
	}
	if request.Method == http.MethodPatch {
		if !a.requireAdmin(response, request) {
			return
		}
		var patch struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
			writeError(response, http.StatusBadRequest, "invalid playlist JSON")
			return
		}
		stored, err := a.store.UpdatePlaylistMetadata(id, patch.Name, patch.Description, mutationOptions(request))
		if err != nil {
			writePlaylistMutationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, stored)
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
		if err := a.store.PutPlaylist(replacement, mutationOptions(request)); err != nil {
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
		if err := a.store.DeletePlaylist(id, mutationOptions(request)); err != nil {
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
			tracks = append(tracks, publicTrack(track, authFromRequest(request)))
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"playlist": playlist, "tracks": tracks})
}

func writePlaylistMutationError(response http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errPlaylistNotFound), errors.Is(err, errMemberNotFound):
		status = http.StatusNotFound
	case errors.Is(err, errLibraryMembers), errors.Is(err, errPlaylistConflict), errors.Is(err, errVersionConflict):
		status = http.StatusConflict
	}
	writeError(response, status, err.Error())
}

func (a *API) playlistTracks(response http.ResponseWriter, request *http.Request, path string) {
	parts := strings.Split(path, "/")
	if (len(parts) != 2 && len(parts) != 3) || parts[1] != "tracks" || !validID.MatchString(parts[0]) {
		writeError(response, http.StatusNotFound, "playlist endpoint not found")
		return
	}
	if !a.requireAdmin(response, request) {
		return
	}
	id := parts[0]
	if len(parts) == 2 && request.Method == http.MethodPost {
		var body struct {
			TrackIDs []string `json:"track_ids"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.TrackIDs == nil {
			writeError(response, http.StatusBadRequest, "track_ids are required")
			return
		}
		playlist, err := a.store.AddPlaylistTracks(id, body.TrackIDs, mutationOptions(request))
		if err != nil {
			writePlaylistMutationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, playlist)
		return
	}
	if len(parts) == 3 && parts[2] == "order" && request.Method == http.MethodPut {
		var body struct {
			TrackIDs         []string `json:"track_ids"`
			ExpectedTrackIDs []string `json:"expected_track_ids"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.TrackIDs == nil || body.ExpectedTrackIDs == nil {
			writeError(response, http.StatusBadRequest, "track_ids and expected_track_ids are required")
			return
		}
		playlist, err := a.store.ReorderPlaylistTracks(id, body.ExpectedTrackIDs, body.TrackIDs, mutationOptions(request))
		if err != nil {
			writePlaylistMutationError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, playlist)
		return
	}
	if len(parts) == 3 && validID.MatchString(parts[2]) {
		trackID := parts[2]
		switch request.Method {
		case http.MethodPatch:
			var body struct {
				Note *string `json:"note"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Note == nil {
				writeError(response, http.StatusBadRequest, "note is required")
				return
			}
			playlist, err := a.store.UpdatePlaylistTrackNote(id, trackID, *body.Note, mutationOptions(request))
			if err != nil {
				writePlaylistMutationError(response, err)
				return
			}
			writeJSON(response, http.StatusOK, playlist)
			return
		case http.MethodDelete:
			if err := a.store.RemovePlaylistTrack(id, trackID, mutationOptions(request)); err != nil {
				writePlaylistMutationError(response, err)
				return
			}
			response.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(response, http.StatusMethodNotAllowed, "method not allowed")
}

func (a *API) track(response http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/api/v2/tracks/")
	if strings.HasSuffix(id, "/cover") || strings.HasSuffix(id, "/lyrics") {
		a.trackResource(response, request, id)
		return
	}
	track, ok := a.store.Track(id)
	if !ok {
		writeError(response, http.StatusNotFound, "TRACK_NOT_FOUND", "track not found")
		return
	}
	if request.Method == http.MethodGet && !canReadTrack(authFromRequest(request), a.store, id) {
		writeError(response, http.StatusNotFound, "TRACK_NOT_FOUND", "track not found")
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
		if err := a.store.UpdateTrack(id, fields, mutationOptions(request)); err != nil {
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
		if err := a.store.DeleteTrack(id, mutationOptions(request)); err != nil {
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
	writeJSON(response, http.StatusOK, publicTrack(track, authFromRequest(request)))
}

func (a *API) trackResource(response http.ResponseWriter, request *http.Request, path string) {
	if !a.requireAdmin(response, request) {
		return
	}
	if request.Method != http.MethodPut {
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		writeError(response, http.StatusBadRequest, "INVALID_TRACK", "invalid track id")
		return
	}
	id, kind := parts[0], parts[1]
	if _, ok := a.store.Track(id); !ok {
		writeError(response, http.StatusNotFound, "TRACK_NOT_FOUND", "track not found")
		return
	}
	limit, field := int64(maxEmbeddedCoverBytes), "cover"
	if kind == "lyrics" {
		limit, field = maxEmbeddedLyricsBytes, "lyrics_file"
	}
	request.Body = http.MaxBytesReader(response, request.Body, limit+(1<<20))
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_RESOURCE", "invalid resource upload")
		return
	}
	defer request.MultipartForm.RemoveAll()
	data, err := uploadedResource(request, field, limit)
	if err != nil || data == nil {
		writeError(response, http.StatusBadRequest, "INVALID_RESOURCE", "resource file is required or exceeds the limit")
		return
	}
	if kind == "cover" {
		err = a.store.SetTrackCover(id, data, mutationOptions(request))
	} else {
		var text string
		text, err = decodeLyricsFile(data)
		if err == nil {
			err = a.store.UpdateTrack(id, map[string]string{"lyrics": text}, mutationOptions(request))
		}
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_RESOURCE", err.Error())
		return
	}
	track, _ := a.store.Track(id)
	writeJSON(response, http.StatusOK, track)
}

func uploadedResource(request *http.Request, field string, limit int64) ([]byte, error) {
	file, _, err := request.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("resource exceeds the size limit")
	}
	return data, nil
}

func (a *API) media(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeError(response, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(request.URL.Path, "/api/v2/media/")
	track, ok := a.store.Track(id)
	if !ok {
		writeError(response, http.StatusNotFound, "TRACK_NOT_FOUND", "track not found")
		return
	}
	if !canReadTrack(authFromRequest(request), a.store, id) {
		writeError(response, http.StatusNotFound, "TRACK_NOT_FOUND", "track not found")
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
	status, err := a.store.StartScan(request.URL.Query().Get("full") == "true", mutationOptions(request))
	if err != nil {
		writeError(response, 409, "SCAN_BUSY", err.Error())
		return
	}
	writeJSON(response, http.StatusAccepted, status)
}

func (a *API) upload(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeError(response, 405, "method not allowed")
		return
	}
	maxUpload := a.store.Settings().MaxUploadBytes
	if maxUpload <= 0 {
		maxUpload = 512 << 20
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxUpload)
	if err := request.ParseMultipartForm(8 << 20); err != nil {
		writeError(response, 400, "invalid upload or file exceeds configured limit")
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
	track, err := a.store.ImportAudioWithMetadata(header.Filename, file, metadata, mutationOptions(request))
	if err != nil {
		writeError(response, 400, err.Error())
		return
	}
	writeJSON(response, http.StatusCreated, track)
}

func (a *API) stageUpload(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	max := a.store.Settings().MaxUploadBytes
	if max <= 0 {
		max = 512 << 20
	}
	request.Body = http.MaxBytesReader(response, request.Body, max+(12<<20))
	if err := request.ParseMultipartForm(8 << 20); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_UPLOAD", "invalid upload or file exceeds configured limit")
		return
	}
	defer request.MultipartForm.RemoveAll()
	file, header, err := request.FormFile("file")
	if err != nil {
		writeError(response, http.StatusBadRequest, "FILE_REQUIRED", "file is required")
		return
	}
	defer file.Close()
	fields := map[string]string{}
	for _, field := range []string{"title", "artist", "album", "genre", "lyrics", "comment"} {
		if value := strings.TrimSpace(request.FormValue(field)); value != "" {
			fields[field] = value
		}
	}
	lyrics, err := uploadedResource(request, "lyrics_file", maxEmbeddedLyricsBytes)
	if err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_LYRICS", err.Error())
		return
	}
	cover, err := uploadedResource(request, "cover", maxEmbeddedCoverBytes)
	if err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_COVER", err.Error())
		return
	}
	coverSource := request.FormValue("cover_source")
	if coverSource != "" && coverSource != "manual" && coverSource != "external" {
		writeError(response, http.StatusBadRequest, "INVALID_COVER_SOURCE", "cover_source must be manual or external")
		return
	}
	preview, err := a.store.stageAudioWithAssets(header.Filename, file, fields, uploadAssets{Lyrics: lyrics, Cover: cover, CoverSource: coverSource}, mutationOptions(request))
	if err != nil {
		writeError(response, http.StatusBadRequest, "STAGE_FAILED", err.Error())
		return
	}
	writeJSON(response, http.StatusCreated, preview)
}

func (a *API) stagedUpload(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/v2/uploads/stage/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || !validID.MatchString(parts[0]) || len(parts) > 2 || (len(parts) == 2 && parts[1] != "commit" && parts[1] != "cover") {
		writeError(response, http.StatusBadRequest, "INVALID_STAGE", "invalid upload stage id")
		return
	}
	id := parts[0]
	var stageOwner string
	if a.store.db.QueryRow("SELECT user_id FROM upload_staging WHERE id=?", id).Scan(&stageOwner) != nil || stageOwner != authFromRequest(request).userID {
		writeError(response, 404, "STAGE_NOT_FOUND", "upload stage not found")
		return
	}
	if len(parts) == 2 && parts[1] == "cover" {
		if request.Method != http.MethodGet {
			writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		asset, err := a.store.UploadStageCover(id)
		if err != nil {
			writeError(response, http.StatusNotFound, "COVER_NOT_FOUND", "staged cover not found")
			return
		}
		response.Header().Set("Content-Type", asset.MIME)
		response.Header().Set("Cache-Control", "no-store")
		_, _ = response.Write(asset.Data)
		return
	}
	if len(parts) == 2 {
		if request.Method != http.MethodPost {
			writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		fields := map[string]string{}
		if request.Body != nil && request.ContentLength != 0 {
			request.Body = http.MaxBytesReader(response, request.Body, 2<<20)
			if err := json.NewDecoder(request.Body).Decode(&fields); err != nil {
				writeError(response, http.StatusBadRequest, "INVALID_METADATA", "invalid metadata JSON")
				return
			}
		}
		track, duplicate, err := a.store.commitUploadStageWithMetadata(id, fields, mutationOptions(request))
		if errors.Is(err, os.ErrNotExist) {
			writeError(response, http.StatusNotFound, "STAGE_NOT_FOUND", "upload stage not found or expired")
			return
		}
		if err != nil {
			writeError(response, http.StatusBadRequest, "COMMIT_FAILED", err.Error())
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"track": track, "duplicate": duplicate})
		return
	}
	switch request.Method {
	case http.MethodGet:
		preview, ok, err := a.store.UploadStage(id)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "STAGE_READ_FAILED", err.Error())
			return
		}
		if !ok {
			writeError(response, http.StatusNotFound, "STAGE_NOT_FOUND", "upload stage not found or expired")
			return
		}
		writeJSON(response, http.StatusOK, preview)
	case http.MethodDelete:
		if err := a.store.DeleteUploadStage(id); errors.Is(err, os.ErrNotExist) {
			writeError(response, http.StatusNotFound, "STAGE_NOT_FOUND", "upload stage not found")
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "STAGE_DELETE_FAILED", err.Error())
		} else {
			response.WriteHeader(http.StatusNoContent)
		}
	default:
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (a *API) keys(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/v2/auth/keys")
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
			decoder := json.NewDecoder(request.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				writeError(response, http.StatusBadRequest, "INVALID_KEY", "invalid key JSON")
				return
			}
			created, err := a.store.CreateAPIKey(input, mutationOptions(request))
			if err != nil {
				writeError(response, http.StatusBadRequest, "INVALID_KEY", err.Error())
				return
			}
			created.APIKey = publicAPIKey(created.APIKey)
			writeJSON(response, http.StatusCreated, created)
		case http.MethodDelete:
			if err := a.store.RevokeAccountKeys(authFromRequest(request).userID, mutationOptions(request)); err != nil {
				writeError(response, 500, "KEY_REVOKE_FAILED", "could not revoke keys")
				return
			}
			response.WriteHeader(204)
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
		rotated, err := a.store.RotateAPIKey(id, mutationOptions(request))
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
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(response, http.StatusBadRequest, "INVALID_KEY", "invalid key JSON")
			return
		}
		updated, err := a.store.UpdateAPIKey(id, input, mutationOptions(request))
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
	if err := a.store.RevokeAPIKey(id, mutationOptions(request)); errors.Is(err, os.ErrNotExist) {
		writeError(response, http.StatusNotFound, "KEY_NOT_FOUND", "key not found")
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "KEY_REVOKE_FAILED", err.Error())
	} else {
		response.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) adminStats(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	if request.Method != http.MethodGet {
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	stats := a.readCallStats()
	if stats.err != nil {
		writeError(response, http.StatusInternalServerError, "STATS_READ_FAILED", "failed to read API call statistics")
		return
	}
	writeJSON(response, http.StatusOK, stats)
}

// trackingResponseWriter records the wire-level status and bytes without
// changing handler signatures.  It intentionally implements only the
// ResponseWriter contract; handlers in this package do not require hijacking
// or server-sent events.
type trackingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *trackingResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingResponseWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(value)
	w.bytes += int64(n)
	return n, err
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
	if len(args) > 0 && args[len(args)-1] == errVersionConflict.Error() {
		status = 409
		args = []string{"VERSION_CONFLICT", errVersionConflict.Error()}
	}
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

func mutationOptions(request *http.Request) MutationOptions {
	expected, _ := strconv.ParseInt(strings.Trim(request.Header.Get("If-Match"), string(rune(34))), 10, 64)
	auth := authFromRequest(request)
	kind := "api_key"
	if auth.session {
		kind = "session"
	}
	return MutationOptions{Actor: auth.username, UserID: auth.userID, ActorType: kind, CredentialID: auth.keyID, ExpectedVersion: expected}
}
func needsVersion(request *http.Request) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return false
	}
	path := request.URL.Path
	if strings.HasPrefix(path, "/api/v2/tracks/") {
		return true
	}
	if strings.HasPrefix(path, "/api/v2/playlists/") && path != "/api/v2/playlists/order" {
		return true
	}
	return strings.HasPrefix(path, "/api/v2/admin/removed/")
}
func (a *API) maintenance(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	path := request.URL.Path
	switch {
	case path == "/api/v2/admin/removed" && request.Method == http.MethodGet:
		writeJSON(response, 200, a.store.RemovedTracks())
	case strings.HasPrefix(path, "/api/v2/admin/removed/") && request.Method == http.MethodPost:
		id := strings.TrimPrefix(path, "/api/v2/admin/removed/")
		if err := a.store.RestoreTrack(id, mutationOptions(request)); err != nil {
			writeError(response, 400, err.Error())
			return
		}
		track, _ := a.store.Track(id)
		writeJSON(response, 200, track)
	case path == "/api/v2/admin/audit" && request.Method == http.MethodGet:
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		before, _ := strconv.ParseInt(request.URL.Query().Get("before"), 10, 64)
		result, err := a.store.Audit(limit, before)
		if err != nil {
			writeError(response, 500, err.Error())
			return
		}
		writeJSON(response, 200, result)
	case path == "/api/v2/admin/backups" && request.Method == http.MethodPost:
		path, err := a.store.Backup()
		if err != nil {
			writeError(response, 500, err.Error())
			return
		}
		writeJSON(response, 201, map[string]string{"path": path})
	case path == "/api/v2/admin/scan/status" && request.Method == http.MethodGet:
		writeJSON(response, 200, a.store.ScanStatus())
	case path == "/api/v2/admin/scan/cancel" && request.Method == http.MethodPost:
		a.store.CancelScan()
		writeJSON(response, 202, a.store.ScanStatus())
	default:
		writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}
