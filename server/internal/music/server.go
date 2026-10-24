package music

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
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

//go:embed users.js
var usersPageScript []byte

//go:embed management.css management.js navigation.js feedback.js library.js administration.js playlist-ui.js community.js sharing.js updater-ui.js setup.js upload-progress.js system-logs.js profile.js profile.css community-realtime.js
var workspaceAssets embed.FS

type API struct {
	realtimeMu      sync.Mutex
	realtimeCursors map[string]realtimeBaseline
	realtimeStreams map[string]int
	store           *Store
	updater         *update.Manager
	concurrencyMu   sync.Mutex
	active          map[string]int
	authConfig      AuthConfig
	hashSlots       chan struct{}
	updating        func() bool
	setupMu         sync.Mutex
	setupToken      string
}

type authContext struct {
	userID             string
	authVersion        int64
	session            bool
	mustChangePassword bool
	csrf               string
	username           string
	role               string
	allowedPlaylists   map[string]bool
	scopes             []string
	keyID              string
	maxConcurrency     int
	download           bool
	maxDownloads       int
}

func NewAPI(store *Store) *API {
	api := &API{store: store, active: map[string]int{}, hashSlots: make(chan struct{}, 2)}
	return api
}

func (a *API) SetUpdater(manager *update.Manager) { a.updater = manager }
func (a *API) SetMaintenance(check func() bool)   { a.updating = check }

func (a *API) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if a.updating != nil && a.updating() && request.URL.Path != "/api/v2/health" {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Retry-After", "5")
		writeError(response, 503, "UPDATE_MAINTENANCE", "server update in progress; retry shortly")
		return
	}
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
			if request.URL.Path != "/api/v2/admin/logs" && request.URL.Path != "/api/v2/auth/activity" && request.URL.Path != "/api/v2/community/events" {
				fields := activityFields(request)
				fields.Status = status
				fields.Reason = response.Header().Get("X-Error-Code")
				fields.DurationMS = time.Since(started).Milliseconds()
				fields.Bytes = tracked.bytes
				code := "request.completed"
				if status >= 500 {
					code = "request.failed"
				} else if status >= 400 {
					code = "request.denied"
				} else if time.Since(started) > 2*time.Second {
					code = "request.slow"
				}
				_ = a.store.activity.Emit(code, fields)
			}
		}()
	}
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if isAPIRequest {
		response.Header().Set("Cache-Control", "no-store")
	}
	requestID, err := randomToken(8)
	if err == nil {
		response.Header().Set("X-Request-ID", requestID)
		request.Header.Set("X-Music-Request-ID", requestID)
	}
	if !a.allowLANHTTP(request) {
		writeError(response, http.StatusForbidden, "LAN_HTTP_ONLY", "HTTP access requires a local/private peer and an IP or localhost host; use HTTPS for public access")
		return
	}
	if request.URL.Path == "/" && request.Method == http.MethodGet {
		response.Header().Set("Cache-Control", "no-cache")
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write(managementPage)
		return
	}
	if request.Method == http.MethodGet {
		if request.URL.Path == "/assets/upload-progress.js" || request.URL.Path == "/assets/system-logs.js" {
			data, _ := workspaceAssets.ReadFile(strings.TrimPrefix(request.URL.Path, "/assets/"))
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "no-cache")
			_, _ = response.Write(data)
			return
		}
		if request.URL.Path == "/assets/setup.js" {
			data, _ := workspaceAssets.ReadFile("setup.js")
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "no-cache")
			_, _ = response.Write(data)
			return
		}
		if request.URL.Path == "/assets/updater-ui.js" {
			data, _ := workspaceAssets.ReadFile("updater-ui.js")
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "no-cache")
			_, _ = response.Write(data)
			return
		}
		assets := map[string]string{"/assets/management.css": "text/css; charset=utf-8", "/assets/management.js": "text/javascript; charset=utf-8", "/assets/navigation.js": "text/javascript; charset=utf-8", "/assets/feedback.js": "text/javascript; charset=utf-8", "/assets/library.js": "text/javascript; charset=utf-8", "/assets/administration.js": "text/javascript; charset=utf-8", "/assets/playlist-ui.js": "text/javascript; charset=utf-8", "/assets/community.js": "text/javascript; charset=utf-8", "/assets/sharing.js": "text/javascript; charset=utf-8"}
		assets["/assets/profile.js"] = "text/javascript; charset=utf-8"
		assets["/assets/community-realtime.js"] = "text/javascript; charset=utf-8"
		assets["/assets/profile.css"] = "text/css; charset=utf-8"
		if contentType, ok := assets[request.URL.Path]; ok {
			data, err := workspaceAssets.ReadFile(strings.TrimPrefix(request.URL.Path, "/assets/"))
			if err != nil {
				http.Error(response, "asset unavailable", http.StatusInternalServerError)
				return
			}
			response.Header().Set("Content-Type", contentType)
			response.Header().Set("Cache-Control", "no-cache")
			_, _ = response.Write(data)
			return
		}
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
	if request.URL.Path == "/assets/users.js" && request.Method == http.MethodGet {
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache")
		_, _ = response.Write(usersPageScript)
		return
	}
	resourceUpload := strings.HasPrefix(request.URL.Path, "/api/v2/tracks/") && (strings.HasSuffix(request.URL.Path, "/cover") || strings.HasSuffix(request.URL.Path, "/lyrics"))
	playlistUpload := strings.HasPrefix(request.URL.Path, "/api/v2/playlists/") && strings.HasSuffix(request.URL.Path, "/editor") || request.URL.Path == "/api/v2/playlists" && request.Method == http.MethodPost && strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/form-data")
	if !strings.HasPrefix(request.URL.Path, "/api/v2/uploads") && !resourceUpload && !playlistUpload {
		request.Body = http.MaxBytesReader(response, request.Body, 2<<20)
	}
	var auth authContext
	if request.URL.Path == "/api/v2/setup" {
		a.webSetup(response, request)
		return
	}
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
		if auth.mustChangePassword && !firstPasswordEndpoint(request) {
			writeError(response, 403, "PASSWORD_CHANGE_REQUIRED", "change your temporary password before using the account")
			return
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
	if request.URL.Path == "/api/v2/community/sync" {
		a.realtimeSync(response, request)
		return
	}
	if request.URL.Path == "/api/v2/community/events" {
		a.communityEvents(response, request)
		return
	}
	if sharingEndpoint(request.URL.Path) {
		a.sharing(response, request)
		return
	}
	if request.URL.Path == "/api/v2/admin/logs" {
		a.systemLogs(response, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/api/v2/public/") || request.URL.Path == "/api/v2/subscriptions" || strings.HasPrefix(request.URL.Path, "/api/v2/subscriptions/") {
		a.community(response, request)
		return
	}
	if !a.authorizeResource(response, request) {
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
	if request.URL.Path == "/api/v2/admin/users" || strings.HasPrefix(request.URL.Path, "/api/v2/admin/users/") {
		a.adminUsers(response, request)
		return
	}
	switch {
	case request.URL.Path == "/api/v2/health":
		if request.Method != http.MethodGet {
			writeError(response, 405, "method not allowed")
			return
		}
		commit, modified := version.Source()
		writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "version": version.Version, "schema": version.Schema, "revision": a.store.Revision(), "commit": commit, "modified": modified})
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
		query := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))
		if query != "" {
			filtered := make([]Track, 0, len(tracks))
			for _, track := range tracks {
				if strings.Contains(strings.ToLower(track.Title+" "+track.Artist+" "+track.Album+" "+track.Genre), query) {
					filtered = append(filtered, track)
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
	case request.URL.Path == "/api/v2/admin/update" || request.URL.Path == "/api/v2/admin/update/check" || request.URL.Path == "/api/v2/admin/update/apply":
		a.adminUpdate(response, request)
	default:
		writeErrorWithID(response, http.StatusNotFound, "NOT_FOUND", "endpoint not found", requestID)
	}
}

func (a *API) playlistOrder(response http.ResponseWriter, request *http.Request) {
	if !a.requireAccount(response, request) {
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
	visible := []Playlist{}
	for _, p := range a.store.Playlists() {
		if p.System || p.OwnerID == authFromRequest(request).userID {
			visible = append(visible, p)
		}
	}
	writeJSON(response, http.StatusOK, visible)
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
		emitActivity(a.store.activity, "update.checked", request, time.Now(), 0)
		return
	}
	if request.URL.Path == "/api/v2/admin/update/apply" && request.Method == http.MethodPost {
		var input struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Version == "" {
			writeError(response, 400, "UPDATE_VERSION_REQUIRED", "select a checked release version")
			return
		}
		if err := a.updater.Apply(input.Version); err != nil {
			writeError(response, 409, "UPDATE_NOT_READY", err.Error())
			return
		}
		writeJSON(response, 202, a.updater.Status())
		emitActivity(a.store.activity, "update.applied", request, time.Now(), 0)
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
			for _, playlist := range a.store.Playlists() {
				if playlist.CoverID == id && canReadPlaylist(auth, a.store, playlist) {
					allowed = true
					break
				}
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
		if err != nil || !user.Enabled || user.MustChangePassword {
			return authContext{}, false
		}
		allowed := map[string]bool{}
		for _, id := range key.AllowedPlaylistIDs {
			playlist, exists := a.store.Playlist(id)
			if exists && (playlist.System || playlist.OwnerID == user.ID) {
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
		if playlist.System || playlist.OwnerID == user.ID {
			allowed[playlist.ID] = true
		}
	}
	return authContext{role: user.Role, userID: user.ID, authVersion: user.AuthVersion, username: user.Username, session: true, mustChangePassword: user.MustChangePassword, csrf: csrfForToken(cookie.Value), allowedPlaylists: allowed, keyID: session.ID}, true
}

func firstPasswordEndpoint(request *http.Request) bool {
	switch request.URL.Path {
	case "/api/v2/auth/me":
		return request.Method == "GET"
	case "/api/v2/auth/password":
		return request.Method == "PUT"
	case "/api/v2/auth/logout", "/api/v2/auth/activity":
		return request.Method == "POST"
	}
	return false
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

func canReadPlaylist(auth authContext, store *Store, p Playlist) bool {
	if auth.session {
		return playlistReadAccess(store.db, auth, p)
	}
	current, err := currentKeyAuth(store.db, auth)
	return err == nil && playlistReadAccess(store.db, current, p)
}

func canReadTrack(auth authContext, store *Store, id string) bool {
	if auth.session || auth.role == "admin" {
		return true
	}
	current, err := currentKeyAuth(store.db, auth)
	if err != nil {
		return false
	}
	for playlistID, allowed := range current.allowedPlaylists {
		if !allowed {
			continue
		}
		var exists bool
		if store.db.QueryRow("SELECT EXISTS(SELECT 1 FROM playlist_tracks m JOIN playlists p ON p.id=m.playlist_id LEFT JOIN users u ON u.id=p.owner_id WHERE m.playlist_id=? AND m.track_id=? AND (p.system=1 OR p.owner_id=? OR (u.enabled=1 AND EXISTS(SELECT 1 FROM subscriptions s WHERE s.user_id=? AND s.playlist_id=p.id) AND (p.visibility='public' OR (p.visibility='shared' AND EXISTS(SELECT 1 FROM playlist_invitations i WHERE i.playlist_id=p.id AND i.inviter_id=p.owner_id AND i.recipient_id=? AND i.state='accepted'))))))", playlistID, id, current.userID, current.userID, current.userID).Scan(&exists) == nil && exists {
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
		var playlists []Playlist
		err := a.store.transaction(func(tx *sql.Tx) error {
			auth := authFromRequest(request)
			var err error
			if !auth.session {
				auth, err = currentKeyAuth(tx, auth)
				if err != nil {
					return err
				}
			}
			playlists, err = playlistViews(tx, auth)
			return err
		})
		if err != nil {
			writeError(response, 500, "READ_FAILED", "unable to read playlists")
			return
		}
		writeJSON(response, http.StatusOK, playlists)
	case http.MethodPost:
		if !a.requireAccount(response, request) {
			return
		}
		if strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/form-data") {
			a.playlistEditor(response, request, "")
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
	if strings.HasSuffix(id, "/visibility") {
		if request.Method != "PUT" {
			writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		if !a.requireAccount(response, request) {
			return
		}
		var body struct {
			Visibility string `json:"visibility"`
		}
		if decodeStrict(request, &body) != nil {
			writeError(response, 400, "INVALID_VISIBILITY", "invalid visibility request")
			return
		}
		stored, err := a.store.SetPlaylistVisibility(strings.TrimSuffix(id, "/visibility"), body.Visibility, mutationOptions(request))
		if err != nil {
			writePlaylistMutationError(response, err)
			return
		}
		writeJSON(response, 200, stored)
		return
	}
	if strings.HasSuffix(id, "/editor") {
		a.playlistEditor(response, request, strings.TrimSuffix(id, "/editor"))
		return
	}
	if strings.Contains(id, "/tracks") {
		a.playlistTracks(response, request, id)
		return
	}
	playlist, ok := a.store.Playlist(id)
	if !ok {
		writeError(response, http.StatusNotFound, "playlist not found")
		return
	}
	if !canReadPlaylist(authFromRequest(request), a.store, playlist) {
		writeError(response, http.StatusNotFound, "PLAYLIST_NOT_FOUND", "playlist not found")
		return
	}
	if request.Method == http.MethodPatch {
		if !a.requireAccount(response, request) {
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
		if !a.requireAccount(response, request) {
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
		if !a.requireAccount(response, request) {
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
	if auth := authFromRequest(request); auth.session && auth.role == "admin" && !playlist.System && playlist.OwnerID != auth.userID {
		if err := a.store.transaction(func(tx *sql.Tx) error {
			return auditMutation(tx, "playlist.admin.read", playlist.ID, nil, map[string]string{"owner_id": playlist.OwnerID}, []MutationOptions{mutationOptions(request)})
		}); err != nil {
			writeError(response, 500, "AUDIT_FAILED", "unable to record management access")
			return
		}
	}
	for _, trackID := range playlist.TrackIDs {
		if track, err := readTrackFrom(a.store.db, trackID, true); err == nil {
			if track.Removed || track.Missing {
				track = unavailableTrack(track)
			}
			tracks = append(tracks, publicTrack(track, authFromRequest(request)))
		}
	}
	view, err := readablePlaylistView(a.store.db, authFromRequest(request), playlist)
	if err != nil {
		writeError(response, 500, "READ_FAILED", "unable to read playlist")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"playlist": view, "tracks": tracks})
}

func writePlaylistMutationError(response http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errPlaylistNotFound), errors.Is(err, errMemberNotFound):
		status = http.StatusNotFound
	case errors.Is(err, errResourceHidden):
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
	if !a.requireAccount(response, request) {
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
		if !a.requireAccount(response, request) {
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
		writeJSON(response, http.StatusOK, publicTrack(updated, authFromRequest(request)))
		return
	}
	if request.Method == http.MethodDelete {
		if !a.requireAccount(response, request) {
			return
		}
		if err := a.store.DeleteTrack(id, mutationOptions(request)); err != nil {
			if errors.Is(err, errTrackInUse) {
				writeError(response, 409, "TRACK_IN_USE", err.Error())
			} else {
				writeError(response, 400, err.Error())
			}
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
	if !a.requireAccount(response, request) {
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
	writeJSON(response, http.StatusOK, publicTrack(track, authFromRequest(request)))
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
	if !a.requireAccount(response, request) {
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
	preview, err := a.store.StageAudio(header.Filename, file, metadata, mutationOptions(request))
	if err != nil {
		writeError(response, 400, err.Error())
		return
	}
	track, duplicate, err := a.store.CommitUploadStage(preview.ID, mutationOptions(request))
	if err != nil {
		_ = a.store.DeleteUploadStage(preview.ID)
	}
	if err != nil {
		writeError(response, 400, err.Error())
		return
	}
	status := http.StatusCreated
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(response, status, struct {
		Track
		Duplicate bool `json:"duplicate"`
	}{publicTrack(track, authFromRequest(request)), duplicate})
}

func (a *API) stageUpload(response http.ResponseWriter, request *http.Request) {
	if !a.requireAccount(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	started := time.Now()
	metrics := activityFields(request)
	_ = a.store.activity.Emit("upload.receiving", metrics)
	received := false
	defer func() {
		if !received {
			metrics.DurationMS = time.Since(started).Milliseconds()
			_ = a.store.activity.Emit("upload.failed", metrics)
		}
	}()
	max := a.store.Settings().MaxUploadBytes
	if max <= 0 {
		max = 512 << 20
	}
	request.Body = http.MaxBytesReader(response, request.Body, max+(12<<20))
	body := &countedUploadBody{ReadCloser: request.Body}
	request.Body = body
	if err := request.ParseMultipartForm(8 << 20); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_UPLOAD", "invalid upload or file exceeds configured limit")
		return
	}
	defer request.MultipartForm.RemoveAll()
	receiveMS := time.Since(started).Milliseconds()
	metrics.Bytes = body.bytes
	metrics.Timings = map[string]int64{"receive_ms": receiveMS}
	_ = a.store.activity.Emit("upload.received", metrics)
	received = true
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
	ctx := context.WithValue(request.Context(), uploadActivityKey{}, metrics)
	validationStarted := time.Now()
	preview, err := a.store.stageAudioWithAssetsContext(ctx, header.Filename, file, fields, uploadAssets{Lyrics: lyrics, Cover: cover, CoverSource: coverSource}, mutationOptions(request))
	if err != nil {
		writeError(response, http.StatusBadRequest, "STAGE_FAILED", err.Error())
		return
	}
	preview.Track = publicTrack(preview.Track, authFromRequest(request))
	preview.Timings["validation_ms"] = time.Since(validationStarted).Milliseconds()
	response.Header().Set("Server-Timing", fmt.Sprintf("receive;dur=%d, validate;dur=%d, probe;dur=%d", receiveMS, preview.Timings["validation_ms"], preview.Timings["probe_ms"]))
	writeJSON(response, http.StatusCreated, preview)
}

func (a *API) stagedUpload(response http.ResponseWriter, request *http.Request) {
	if !a.requireAccount(response, request) {
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
		if request.Method == http.MethodPut {
			a.stageCoverUpload(response, request, id)
			return
		}
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
		writeJSON(response, http.StatusOK, map[string]any{"track": publicTrack(track, authFromRequest(request)), "duplicate": duplicate})
		emitActivity(a.store.activity, "upload.committed", request, time.Now(), track.Size)
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
		preview.Track = publicTrack(preview.Track, authFromRequest(request))
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
	if !a.requireAccount(response, request) {
		return
	}
	if request.URL.Path == "/api/v2/auth/keys/stats" {
		if request.Method != "GET" {
			writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		stats := a.readCallStats(authFromRequest(request).userID)
		if stats.err != nil {
			writeError(response, 500, "STATS_READ_FAILED", "statistics unavailable")
			return
		}
		writeJSON(response, 200, stats)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/v2/auth/keys")
	if path == "" || path == "/" {
		switch request.Method {
		case http.MethodGet:
			views := make([]APIKeyView, 0)
			for _, key := range a.store.APIKeys() {
				if key.UserID == authFromRequest(request).userID {
					views = append(views, APIKeyView{APIKey: publicAPIKey(key)})
				}
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
	stats := a.readCallStats(request.URL.Query().Get("user_id"))
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

func (w *trackingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
	if len(args) > 0 && args[len(args)-1] == errResourceHidden.Error() {
		status = 404
		args = []string{"NOT_FOUND", errResourceHidden.Error()}
	}
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
	response.Header().Set("X-Error-Code", code)
	if requestID := response.Header().Get("X-Request-ID"); requestID != "" {
		errorValue["request_id"] = requestID
	}
	writeJSON(response, status, map[string]any{"error": errorValue})
}

func writeErrorWithID(response http.ResponseWriter, status int, code, message, requestID string) {
	response.Header().Set("X-Error-Code", code)
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
