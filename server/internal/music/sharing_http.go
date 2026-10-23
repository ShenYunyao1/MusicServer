package music

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func sharingEndpoint(path string) bool {
	return path == "/api/v2/invitations" || strings.HasPrefix(path, "/api/v2/invitations/") || strings.HasPrefix(path, "/api/v2/playlists/") && strings.Contains(path, "/invitations")
}
func writeSharingError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errResourceHidden), errors.Is(err, sql.ErrNoRows):
		writeError(response, 404, "NOT_FOUND", "invitation or playlist not found")
	case errors.Is(err, errVersionConflict):
		writeError(response, 409, "VERSION_CONFLICT", err.Error())
	case errors.Is(err, errInvitationState):
		writeError(response, 409, "INVITATION_CONFLICT", err.Error())
	case errors.Is(err, errSharingMode):
		writeError(response, 400, "SHARING_MODE_REQUIRED", err.Error())
	case errors.Is(err, errInvitationTarget):
		writeError(response, 400, "INVALID_INVITEE", err.Error())
	case errors.Is(err, errInvitationLimit):
		writeError(response, 400, "INVITATION_LIMIT", err.Error())
	default:
		writeError(response, 500, "SHARING_FAILED", "unable to process invitation")
	}
}
func invitationPagination(request *http.Request) (int, int, error) {
	offset, limit := 0, 30
	var err error
	if text := request.URL.Query().Get("offset"); text != "" {
		offset, err = strconv.Atoi(text)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be non-negative")
		}
	}
	if text := request.URL.Query().Get("limit"); text != "" {
		limit, err = strconv.Atoi(text)
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, errors.New("limit must be 1..100")
		}
	}
	return offset, limit, nil
}
func (a *API) sharing(response http.ResponseWriter, request *http.Request) {
	if !a.requireAccount(response, request) {
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	path := strings.TrimPrefix(request.URL.Path, "/api/v2/")
	parts := strings.Split(path, "/")
	ownerRoute := len(parts) >= 3 && parts[0] == "playlists" && parts[2] == "invitations" && validID.MatchString(parts[1])
	recipientRoute := parts[0] == "invitations"
	if !ownerRoute && !recipientRoute {
		writeSharingError(response, errResourceHidden)
		return
	}
	playlistID := ""
	baseLen := 1
	if ownerRoute {
		playlistID = parts[1]
		baseLen = 3
	}
	if len(parts) == baseLen && request.Method == "GET" {
		offset, limit, err := invitationPagination(request)
		if err != nil {
			writeError(response, 400, "INVALID_PAGE", err.Error())
			return
		}
		page, err := a.store.Invitations(authFromRequest(request).userID, playlistID, offset, limit)
		if err != nil {
			writeSharingError(response, err)
			return
		}
		writeJSON(response, 200, page)
		return
	}
	if recipientRoute && len(parts) == 2 && validID.MatchString(parts[1]) && request.Method == "GET" {
		invite, err := a.store.ReceivedInvitation(parts[1], authFromRequest(request).userID)
		if err != nil {
			writeSharingError(response, err)
			return
		}
		writeJSON(response, 200, invite)
		return
	}
	create := ownerRoute && len(parts) == baseLen && request.Method == "POST"
	inviteID, action := "", ""
	if len(parts) > baseLen && validID.MatchString(parts[baseLen]) {
		inviteID = parts[baseLen]
		if ownerRoute && len(parts) == baseLen+1 && request.Method == "DELETE" {
			action = "revoke"
		}
		if len(parts) == baseLen+2 && request.Method == "POST" {
			if ownerRoute && parts[baseLen+1] == "resend" {
				action = "resend"
			}
			if recipientRoute && (parts[baseLen+1] == "accept" || parts[baseLen+1] == "decline") {
				action = parts[baseLen+1]
			}
		}
	}
	if !create && action == "" {
		writeError(response, 405, "METHOD_NOT_ALLOWED", "unsupported invitation endpoint or method")
		return
	}
	options := mutationOptions(request)
	if options.ExpectedVersion < 1 {
		writeError(response, 428, "VERSION_REQUIRED", "If-Match with current version is required")
		return
	}
	if create {
		var body struct {
			Username string `json:"username"`
		}
		if decodeStrict(request, &body) != nil {
			writeError(response, 400, "INVALID_INVITATION", "exact username is required")
			return
		}
		i, err := a.store.CreateInvitation(playlistID, body.Username, options)
		if err != nil {
			writeSharingError(response, err)
			return
		}
		writeJSON(response, 201, i)
		return
	}
	if ownerRoute {
		i, err := readInvitation(a.store.db, inviteID)
		if err != nil || i.PlaylistID != playlistID {
			writeSharingError(response, errResourceHidden)
			return
		}
	}
	i, err := a.store.InvitationAction(inviteID, action, options)
	if err != nil {
		writeSharingError(response, err)
		return
	}
	writeJSON(response, 200, i)
}
