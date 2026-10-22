package music

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const sessionCookie = "music_session"

type AuthConfig struct {
	Origin  string
	Secure  bool
	LANHTTP bool
}

func NewAuthConfig(address, externalURL string) (AuthConfig, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return AuthConfig{}, err
	}
	loopback := host == "localhost"
	lanListener := host == ""
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
		lanListener = ip.IsUnspecified() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	if externalURL == "" {
		if !loopback && lanListener {
			return AuthConfig{LANHTTP: true}, nil
		}
		if !loopback {
			return AuthConfig{}, errors.New("non-loopback listeners require -public-url https://your-server")
		}
		return AuthConfig{}, nil
	}
	parsed, err := url.Parse(externalURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return AuthConfig{}, errors.New("public-url must be an origin without path, query or credentials")
	}
	publicHost := strings.ToLower(parsed.Hostname())
	publicLoopback := publicHost == "localhost"
	if ip := net.ParseIP(publicHost); ip != nil {
		publicLoopback = ip.IsLoopback()
	}
	if parsed.Scheme != "https" {
		if !loopback || !publicLoopback || parsed.Scheme != "http" {
			return AuthConfig{}, errors.New("public-url must use HTTPS outside loopback development")
		}
	}
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		publicHost = net.JoinHostPort(publicHost, port)
	} else if strings.Contains(publicHost, ":") {
		publicHost = "[" + publicHost + "]"
	}
	return AuthConfig{Origin: parsed.Scheme + "://" + publicHost, Secure: parsed.Scheme == "https"}, nil
}

// Direct HTTP is for local/private networks. Public deployments retain a
// pinned HTTPS origin and Secure cookies; forwarded headers do not grant LAN access.
func localHTTPAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = strings.Trim(address, "[]")
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func (a *API) allowLANHTTP(request *http.Request) bool {
	return !a.authConfig.LANHTTP || (localHTTPAddress(request.RemoteAddr) && localHTTPAddress(request.Host))
}

func (a *API) SetAuthConfig(config AuthConfig) { a.authConfig = config }
func (a *API) validOrigin(request *http.Request) bool {
	expected := a.authConfig.Origin
	if expected == "" {
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + request.Host
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		return origin == expected
	}
	if referer := request.Header.Get("Referer"); referer != "" {
		u, err := url.Parse(referer)
		return err == nil && u.Scheme+"://"+u.Host == expected
	}
	return false
}
func csrfForToken(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("musicserver-csrf-v1"))
	return hex.EncodeToString(mac.Sum(nil))
}
func (a *API) setSessionCookie(response http.ResponseWriter, token string, expires time.Time) {
	cookie := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: a.authConfig.Secure, SameSite: http.SameSiteStrictMode, Expires: expires}
	if token == "" {
		cookie.MaxAge = -1
	} else {
		cookie.MaxAge = int(time.Until(expires).Seconds())
	}
	http.SetCookie(response, cookie)
}
func (a *API) accountAuth(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	path := strings.TrimPrefix(request.URL.Path, "/api/v2/auth/")
	if path == "login" {
		if request.Method != "POST" {
			writeError(response, 405, "method not allowed")
			return
		}
		if !a.validOrigin(request) {
			writeError(response, 403, "ORIGIN_DENIED", "same-origin request required")
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		request.Body = http.MaxBytesReader(response, request.Body, 4096)
		if json.NewDecoder(request.Body).Decode(&body) != nil || len(body.Username) > 64 || len(body.Password) > 1024 {
			writeError(response, 400, "INVALID_LOGIN", "invalid login request")
			return
		}
		select {
		case a.hashSlots <- struct{}{}:
			defer func() { <-a.hashSlots }()
		default:
			response.Header().Set("Retry-After", "1")
			writeError(response, 429, "AUTH_BUSY", "login busy; retry later")
			return
		}
		ip, _, err := net.SplitHostPort(request.RemoteAddr)
		if err != nil {
			ip = request.RemoteAddr
		}
		user, session, token, err := a.store.Login(body.Username, body.Password, ip)
		if err != nil {
			if errors.Is(err, errLoginLimited) {
				response.Header().Set("Retry-After", "60")
				writeError(response, 429, "LOGIN_LIMITED", err.Error())
			} else if errors.Is(err, errCredentials) {
				writeError(response, 401, "LOGIN_FAILED", errCredentials.Error())
			} else {
				writeError(response, 500, "LOGIN_FAILED", "login unavailable")
			}
			return
		}
		a.setSessionCookie(response, token, session.ExpiresAt)
		writeJSON(response, 200, map[string]any{"user": user, "csrf_token": csrfForToken(token)})
		return
	}
	auth := authFromRequest(request)
	if !auth.session {
		writeError(response, 403, "SESSION_REQUIRED", "account session required")
		return
	}
	switch {
	case path == "me" && request.Method == "GET":
		user, err := readUser(a.store.db, auth.userID)
		if err != nil {
			writeError(response, 500, "ACCOUNT_FAILED", "account unavailable")
			return
		}
		writeJSON(response, 200, map[string]any{"user": user, "csrf_token": auth.csrf})
	case path == "logout" && request.Method == "POST":
		if err := a.store.RevokeSession(auth.userID, auth.keyID, mutationOptions(request)); err != nil {
			writeError(response, 500, "LOGOUT_FAILED", "could not revoke session")
			return
		}
		a.setSessionCookie(response, "", time.Unix(0, 0))
		response.WriteHeader(204)
	case path == "activity" && request.Method == "POST":
		_, err := a.store.db.Exec("UPDATE sessions SET last_active_at=? WHERE id=? AND revoked_at='' AND last_active_at<?", persistedTime(time.Now()), auth.keyID, persistedTime(time.Now().Add(-time.Minute)))
		if err != nil {
			writeError(response, 500, "ACTIVITY_FAILED", "activity unavailable")
			return
		}
		response.WriteHeader(204)
	case path == "password" && request.Method == "PUT":
		var body struct {
			OldPassword string `json:"old_password"`
			NewPassword string `json:"new_password"`
		}
		request.Body = http.MaxBytesReader(response, request.Body, 4096)
		if json.NewDecoder(request.Body).Decode(&body) != nil {
			writeError(response, 400, "INVALID_PASSWORD", "invalid password request")
			return
		}
		select {
		case a.hashSlots <- struct{}{}:
			defer func() { <-a.hashSlots }()
		default:
			writeError(response, 429, "AUTH_BUSY", "password hashing busy")
			return
		}
		user, err := readUser(a.store.db, auth.userID)
		if err == nil {
			err = a.store.ChangePassword(user, body.OldPassword, body.NewPassword, mutationOptions(request))
		}
		if err != nil {
			writeError(response, 400, "PASSWORD_FAILED", err.Error())
			return
		}
		a.setSessionCookie(response, "", time.Unix(0, 0))
		response.WriteHeader(204)
	case path == "sessions" && request.Method == "GET":
		sessions, err := a.store.Sessions(auth.userID, auth.keyID)
		if err != nil {
			writeError(response, 500, "SESSIONS_FAILED", "sessions unavailable")
			return
		}
		writeJSON(response, 200, sessions)
	case strings.HasPrefix(path, "sessions/") && request.Method == "DELETE":
		id := strings.TrimPrefix(path, "sessions/")
		if err := a.store.RevokeSession(auth.userID, id, mutationOptions(request)); err != nil {
			writeError(response, 404, "NOT_FOUND", "session not found")
			return
		}
		if id == auth.keyID {
			a.setSessionCookie(response, "", time.Unix(0, 0))
		}
		response.WriteHeader(204)
	default:
		writeError(response, 404, "NOT_FOUND", "endpoint not found")
	}
}
