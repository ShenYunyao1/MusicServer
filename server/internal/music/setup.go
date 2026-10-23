package music

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ConfigureWebSetup must run before serving requests. The capability is stored
// beside the database, never returned by the public status endpoint.
func (a *API) ConfigureWebSetup() error {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	initialized, err := a.store.HasAdministrator()
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(a.store.DataPath()), "setup-token")
	if initialized {
		a.setupToken = ""
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return a.store.RequireAdministrator()
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		token, e := randomToken(32)
		if e != nil {
			return e
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.WriteString(token + "\n")
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		raw = []byte(token)
	} else if err != nil {
		return err
	}
	token := strings.TrimSpace(string(raw))
	if len(token) != 64 {
		return errors.New("invalid setup-token file; remove it offline to regenerate authorization")
	}
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	a.setupToken = token
	return nil
}

func (a *API) SetupPending() bool {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	return a.setupToken != ""
}

func (a *API) webSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]bool{"required": a.SetupPending()})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !a.validOrigin(r) {
		writeError(w, 403, "ORIGIN_DENIED", "same-origin request required")
		return
	}
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
		Confirm  string `json:"confirm_password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeError(w, 400, "SETUP_INVALID", "invalid initialization request")
		return
	}
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	if a.setupToken == "" {
		writeError(w, 409, "SETUP_COMPLETE", "administrator already initialized")
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(a.setupToken)) != 1 {
		writeError(w, 403, "SETUP_DENIED", "invalid initialization authorization")
		return
	}
	if body.Password != body.Confirm {
		writeError(w, 400, "PASSWORD_MISMATCH", "password confirmation does not match")
		return
	}
	select {
	case a.hashSlots <- struct{}{}:
		defer func() { <-a.hashSlots }()
	default:
		writeError(w, 429, "AUTH_BUSY", "password hashing busy")
		return
	}
	user, err := a.store.createUserRecord(body.Username, body.Password, "admin", "", false, true, []MutationOptions{{Actor: "web-setup", ActorType: "setup"}})
	if err != nil {
		writeError(w, 400, "SETUP_FAILED", err.Error())
		return
	}
	a.setupToken = ""
	if err := os.Remove(filepath.Join(filepath.Dir(a.store.DataPath()), "setup-token")); err != nil && !os.IsNotExist(err) {
		// The database is authoritative: a leftover file can never reopen setup.
		writeJSON(w, 201, map[string]any{"username": user.Username, "initialized": true})
		return
	}
	writeJSON(w, 201, map[string]any{"username": user.Username, "initialized": true})
}
