package music

import (
	"io"
	"net/http"
	"strings"
)

func verificationUploadRequest(r *http.Request) bool {
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
		return false
	}
	path := r.URL.Path
	return path == "/api/v2/uploads" || strings.HasPrefix(path, "/api/v2/uploads/") || strings.HasPrefix(path, "/api/v2/tracks/") && (strings.HasSuffix(path, "/cover") || strings.HasSuffix(path, "/lyrics"))
}

func (a *API) requireUploadEmail(w http.ResponseWriter, r *http.Request) bool {
	if !verificationUploadRequest(r) {
		return true
	}
	auth := authFromRequest(r)
	if err := validateAccountSession(a.store.db, auth); err != nil {
		writeError(w, 401, "AUTH_INVALID", "sign in again")
		return false
	}
	if err := checkUploadEmailPolicy(a.store.db, auth.userID); err != nil {
		if !writeStorageError(w, err) {
			writeError(w, 403, "UPLOAD_DENIED", "upload is unavailable")
		}
		return false
	}
	r.Body = &emailUploadBody{ReadCloser: r.Body, store: a.store, auth: auth}
	return true
}

// A request that was authorized before a policy change cannot continue to
// consume its entire multipart body or turn it into a new validated stage.
type emailUploadBody struct {
	io.ReadCloser
	store     *Store
	auth      authContext
	unchecked int
}

func (b *emailUploadBody) Read(data []byte) (int, error) {
	check := b.unchecked <= 0
	if check {
		if err := validateAccountSession(b.store.db, b.auth); err != nil {
			return 0, err
		}
		if err := checkUploadEmailPolicy(b.store.db, b.auth.userID); err != nil {
			return 0, err
		}
		b.unchecked = 1 << 20
	}
	n, err := b.ReadCloser.Read(data)
	b.unchecked -= n
	if check || b.unchecked <= 0 || err != nil {
		if policyErr := checkUploadEmailPolicy(b.store.db, b.auth.userID); policyErr != nil {
			return 0, policyErr
		}
		if sessionErr := validateAccountSession(b.store.db, b.auth); sessionErr != nil {
			return 0, sessionErr
		}
	}
	return n, err
}

func checkStageUploadActor(reader sqlReader, owner string, options []MutationOptions) error {
	if err := checkActorOwner(reader, owner, options); err != nil {
		return err
	}
	if len(options) > 0 && options[0].ActorType == "session" {
		if err := validateAccountMutationSession(reader, options[0]); err != nil {
			return err
		}
	}
	return checkUploadEmailPolicy(reader, owner)
}

func checkResumeUploadActor(reader sqlReader, auth authContext) error {
	if err := resumeActor(reader, auth); err != nil {
		return err
	}
	return checkUploadEmailPolicy(reader, auth.userID)
}
