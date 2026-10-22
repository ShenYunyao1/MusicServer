package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func decodeStrict(request *http.Request, value any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("exactly one JSON object is required")
	}
	return nil
}
func (a *API) userHashSlot(response http.ResponseWriter) bool {
	select {
	case a.hashSlots <- struct{}{}:
		return true
	default:
		response.Header().Set("Retry-After", "1")
		writeError(response, 429, "AUTH_BUSY", "password hashing busy")
		return false
	}
}
func writeUserError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errUsernameTaken):
		writeError(response, 409, "USERNAME_EXISTS", err.Error())
	case errors.Is(err, errVersionConflict):
		writeError(response, 409, "VERSION_CONFLICT", err.Error())
	case errors.Is(err, sql.ErrNoRows):
		writeError(response, 404, "NOT_FOUND", "account not found")
	default:
		writeError(response, 400, "ACCOUNT_FAILED", err.Error())
	}
}
func (a *API) adminUsers(response http.ResponseWriter, request *http.Request) {
	if !a.requireAdmin(response, request) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v2/admin/users"), "/")
	if path == "" {
		switch request.Method {
		case "GET":
			users, err := a.store.Users()
			if err != nil {
				writeError(response, 500, "USERS_FAILED", "accounts unavailable")
				return
			}
			writeJSON(response, 200, users)
		case "POST":
			var body struct {
				Username    string `json:"username"`
				DisplayName string `json:"display_name"`
			}
			if decodeStrict(request, &body) != nil {
				writeError(response, 400, "INVALID_USER", "invalid account request")
				return
			}
			if !a.userHashSlot(response) {
				return
			}
			defer func() { <-a.hashSlots }()
			created, err := a.store.CreateMember(body.Username, body.DisplayName, mutationOptions(request))
			if err != nil {
				writeUserError(response, err)
				return
			}
			writeJSON(response, 201, created)
		default:
			writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		}
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "reset-password" && request.Method == "POST" {
		options := mutationOptions(request)
		if options.ExpectedVersion < 1 {
			writeError(response, 428, "VERSION_REQUIRED", "If-Match is required")
			return
		}
		var body struct {
			RevokeKeys bool `json:"revoke_keys"`
		}
		if decodeStrict(request, &body) != nil {
			writeError(response, 400, "INVALID_RESET", "invalid reset request")
			return
		}
		if !a.userHashSlot(response) {
			return
		}
		defer func() { <-a.hashSlots }()
		result, err := a.store.ResetMemberPassword(parts[0], body.RevokeKeys, options)
		if err != nil {
			writeUserError(response, err)
			return
		}
		writeJSON(response, 200, result)
		return
	}
	writeError(response, 404, "NOT_FOUND", "account endpoint not found")
}
