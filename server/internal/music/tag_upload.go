package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type UploadTagEdit struct {
	Version   int64    `json:"version"`
	AddIDs    []string `json:"add_ids,omitempty"`
	RemoveIDs []string `json:"remove_ids,omitempty"`
}

type UploadTagEdits struct {
	Public   *UploadTagEdit `json:"public,omitempty"`
	Personal *UploadTagEdit `json:"personal,omitempty"`
}

type UploadTagVersions struct {
	PublicVersion   int64 `json:"public_version"`
	PersonalVersion int64 `json:"personal_version"`
}

func validateUploadTagEdits(edits *UploadTagEdits) error {
	if edits == nil {
		return nil
	}
	if edits.Public == nil && edits.Personal == nil {
		return errTagInvalid
	}
	for _, edit := range []*UploadTagEdit{edits.Public, edits.Personal} {
		if edit == nil {
			continue
		}
		if edit.Version < 1 {
			return errTagInvalid
		}
		if err := validateTrackTagIDs(edit.AddIDs, edit.RemoveIDs); err != nil {
			return err
		}
		for _, ids := range [][]string{edit.AddIDs, edit.RemoveIDs} {
			for _, id := range ids {
				if !tagUUID(id) {
					return errTagInvalid
				}
			}
		}
	}
	return nil
}

// Metadata remains at the top level for existing clients. The only additional
// structured member is tag_edits; ownership never comes from the request body.
func parseUploadCommit(r *http.Request) (map[string]string, *UploadTagEdits, error) {
	fields := map[string]string{}
	if r.Body == nil || r.ContentLength == 0 {
		return fields, nil, nil
	}
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		return nil, nil, errTagInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, nil, errTagInvalid
	}
	var edits *UploadTagEdits
	for key, value := range raw {
		if key == "tag_edits" {
			if string(value) == "null" {
				return nil, nil, errTagInvalid
			}
			d := json.NewDecoder(strings.NewReader(string(value)))
			d.DisallowUnknownFields()
			if err := d.Decode(&edits); err != nil {
				return nil, nil, errTagInvalid
			}
			continue
		}
		var text string
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &text) != nil {
			return nil, nil, errTagInvalid
		}
		fields[key] = text
	}
	if err := validMetadataFields(fields); err != nil {
		return nil, nil, err
	}
	if err := validateUploadTagEdits(edits); err != nil {
		return nil, nil, err
	}
	return fields, edits, nil
}

func (s *Store) uploadPreviewTags(preview *UploadPreview, record stagedRecord) {
	if requireTagsSchema(s.db) != nil {
		return
	}
	preview.GenreValues = record.GenreValues
	versions := &UploadTagVersions{PublicVersion: 1, PersonalVersion: 1}
	if preview.DuplicateOf != "" {
		public, err := trackTagVersion(s.db, preview.DuplicateOf, "public")
		if err != nil {
			return
		}
		personal, err := trackTagVersion(s.db, preview.DuplicateOf, record.Track.OwnerID)
		if err != nil {
			return
		}
		versions.PublicVersion, versions.PersonalVersion = public, personal
	}
	preview.TagVersions = versions
}

// Reconstruct the authenticated identity from the persisted session rather
// than accepting a role, version or user ID from tag JSON.
func uploadTagActor(reader sqlReader, options []MutationOptions) (authContext, error) {
	if len(options) == 0 || options[0].ActorType != "session" {
		return authContext{}, errTagSession
	}
	option := options[0]
	var version int64
	if err := reader.QueryRow("SELECT auth_version FROM sessions WHERE id=? AND user_id=?", option.CredentialID, option.UserID).Scan(&version); err != nil {
		return authContext{}, errTagSession
	}
	auth := authContext{session: true, userID: option.UserID, keyID: option.CredentialID, authVersion: version, username: option.Actor}
	actor, err := tagActor(reader, auth)
	if err != nil {
		return authContext{}, err
	}
	auth.role = actor.Role
	return auth, nil
}

func applyUploadTagsTx(tx *sql.Tx, trackID string, edits *UploadTagEdits, options []MutationOptions) (bool, error) {
	if edits == nil {
		return false, nil
	}
	auth, err := uploadTagActor(tx, options)
	if err != nil {
		return false, err
	}
	publicChanged := false
	for _, item := range []struct {
		scope string
		edit  *UploadTagEdit
	}{{"public", edits.Public}, {"personal", edits.Personal}} {
		if item.edit == nil {
			continue
		}
		_, changed, err := changeTrackTagsTx(tx, auth, trackID, item.scope, item.edit.Version, item.edit.AddIDs, item.edit.RemoveIDs)
		if err != nil {
			return false, err
		}
		if item.scope == "public" {
			publicChanged = changed
		}
	}
	return publicChanged, nil
}

func writeUploadTagError(w http.ResponseWriter, err error) bool {
	for _, tagError := range []error{errTagsUnavailable, errTagSession, errTagInvalid, errTagLimit, errTagNameTaken, errTagInUse, errTagForbidden, errVersionConflict, errResourceHidden} {
		if errors.Is(err, tagError) {
			writeTagError(w, err)
			return true
		}
	}
	return false
}
