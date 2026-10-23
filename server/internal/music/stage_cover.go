package music

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Updating a discovered sidecar never requires sending the audio again.
func (s *Store) updateStageCover(id string, data []byte, source string, options ...MutationOptions) (UploadPreview, error) {
	if source != "manual" && source != "external" {
		return UploadPreview{}, errors.New("invalid cover source")
	}
	cover, mime, ok := validateCover(data, "")
	if !ok {
		return UploadPreview{}, errors.New("invalid cover (maximum 8 MiB and 20 MP)")
	}
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	preview, record, _, err := s.uploadStageLocked(id)
	if err != nil {
		return UploadPreview{}, err
	}
	var owner string
	if err := s.db.QueryRow("SELECT user_id FROM upload_staging WHERE id=?", id).Scan(&owner); err != nil {
		return UploadPreview{}, err
	}
	if len(options) > 0 && options[0].UserID != "" && options[0].UserID != owner {
		return UploadPreview{}, errResourceHidden
	}
	if err := checkActorOwner(s.db, owner, options); err != nil {
		return UploadPreview{}, err
	}
	if source == "external" && record.Track.CoverSource == "embedded" {
		return preview, nil
	}
	record.Cover = cover
	record.Track.CoverID = hashBytes(cover)
	record.Track.CoverMIME = mime
	record.Track.CoverSource = source
	encoded, err := json.Marshal(record)
	if err != nil {
		return UploadPreview{}, err
	}
	if _, err := s.db.Exec("UPDATE upload_staging SET track_json=? WHERE id=?", string(encoded), id); err != nil {
		return UploadPreview{}, err
	}
	return s.previewLocked(id, preview.OriginalName, record, preview.CreatedAt, preview.ExpiresAt), nil
}
func (a *API) stageCoverUpload(w http.ResponseWriter, r *http.Request, id string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxEmbeddedCoverBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, 400, "INVALID_COVER", "invalid cover upload")
		return
	}
	defer r.MultipartForm.RemoveAll()
	data, err := uploadedResource(r, "cover", maxEmbeddedCoverBytes)
	if err != nil || len(data) == 0 {
		writeError(w, 400, "INVALID_COVER", "cover is required and must not exceed 8 MiB")
		return
	}
	source := r.FormValue("cover_source")
	if source == "" {
		source = "manual"
	}
	preview, err := a.store.updateStageCover(id, data, source, mutationOptions(r))
	if err != nil {
		writeError(w, 400, "STAGE_COVER_FAILED", err.Error())
		return
	}
	preview.Track = publicTrack(preview.Track, authFromRequest(r))
	writeJSON(w, 200, preview)
}
