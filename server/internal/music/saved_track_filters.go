package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var errSavedFilterNameTaken = errors.New("saved filter name already exists")

const savedTrackFilterLimit = 100
const savedTrackFilterMaxJSON = 16 << 10

type SavedTrackFilterValidation struct {
	Valid          bool          `json:"valid"`
	Code           string        `json:"code,omitempty"`
	Redirects      []TagRedirect `json:"redirects"`
	ResolvedFilter *TrackFilter  `json:"resolved_filter,omitempty"`
}

type SavedTrackFilter struct {
	ID         string                     `json:"id"`
	Name       string                     `json:"name"`
	Filter     TrackFilter                `json:"filter"`
	Version    int64                      `json:"version"`
	CreatedAt  string                     `json:"created_at"`
	UpdatedAt  string                     `json:"updated_at"`
	Validation SavedTrackFilterValidation `json:"validation"`
}

type SavedTrackFilterInput struct {
	Name   string      `json:"name"`
	Filter TrackFilter `json:"filter"`
}

type SavedTrackFilterPatch struct {
	Name   *string      `json:"name"`
	Filter *TrackFilter `json:"filter"`
}

type savedTrackFilterRow struct {
	SavedTrackFilter
	raw string
}

// Keep the original rule, including merged source UUIDs. Resolving never
// rewrites the persisted draft, silently removes conditions, or bumps versions.
func savedTrackFilterValidation(reader sqlReader, auth authContext, input TrackFilter) (SavedTrackFilterValidation, error) {
	status := SavedTrackFilterValidation{Redirects: []TagRedirect{}}
	encoded, err := json.Marshal(input)
	if err != nil || len(encoded) > savedTrackFilterMaxJSON {
		status.Code = "FILTER_INVALID"
		return status, nil
	}
	var clone TrackFilter
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return status, err
	}
	resolved, redirects, err := resolveTrackFilter(reader, auth, clone)
	switch {
	case err == nil:
		status.Valid, status.ResolvedFilter = true, &resolved
		if redirects != nil {
			status.Redirects = redirects
		}
	case errors.Is(err, errFilterReference), errors.Is(err, errResourceHidden), errors.Is(err, sql.ErrNoRows):
		status.Code = "FILTER_REFERENCE_INVALID"
	case errors.Is(err, errTagInvalid):
		status.Code = "FILTER_INVALID"
	case errors.Is(err, errTagForbidden):
		status.Code = "FILTER_FORBIDDEN"
	default:
		return status, err
	}
	return status, nil
}

func encodeSavedTrackFilter(reader sqlReader, auth authContext, input TrackFilter) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil || len(encoded) > savedTrackFilterMaxJSON {
		return "", errTagInvalid
	}
	var clone TrackFilter
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return "", err
	}
	if _, _, err := resolveTrackFilter(reader, auth, clone); err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeSavedTrackFilter(row savedTrackFilterRow) (SavedTrackFilter, error) {
	decoder := json.NewDecoder(strings.NewReader(row.raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&row.Filter); err != nil {
		return SavedTrackFilter{}, err
	}
	return row.SavedTrackFilter, nil
}

func readSavedTrackFilter(reader sqlReader, auth authContext, id string) (SavedTrackFilter, string, error) {
	var row savedTrackFilterRow
	err := reader.QueryRow("SELECT id,name,query_json,version,created_at,updated_at FROM saved_track_filters WHERE id=? AND user_id=?", id, auth.userID).Scan(&row.ID, &row.Name, &row.raw, &row.Version, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SavedTrackFilter{}, "", errResourceHidden
	}
	if err != nil {
		return SavedTrackFilter{}, "", err
	}
	filter, err := decodeSavedTrackFilter(row)
	return filter, row.raw, err
}

func checkSavedTrackFilterName(reader sqlReader, userID, exceptID, normalized string) error {
	rows, err := reader.Query("SELECT id,name FROM saved_track_filters WHERE user_id=?", userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		if id == exceptID {
			continue
		}
		_, key, err := normalizeTagName(name)
		if err != nil {
			return err
		}
		if key == normalized {
			return errSavedFilterNameTaken
		}
	}
	return rows.Err()
}

func (s *Store) ListSavedTrackFilters(auth authContext) ([]SavedTrackFilter, error) {
	items := []SavedTrackFilter{}
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		if _, err := tagActor(tx, auth); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT id,name,query_json,version,created_at,updated_at FROM saved_track_filters WHERE user_id=? ORDER BY updated_at DESC,id", auth.userID)
		if err != nil {
			return err
		}
		stored := []savedTrackFilterRow{}
		for rows.Next() {
			var row savedTrackFilterRow
			if err = rows.Scan(&row.ID, &row.Name, &row.raw, &row.Version, &row.CreatedAt, &row.UpdatedAt); err != nil {
				rows.Close()
				return err
			}
			stored = append(stored, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, row := range stored {
			item, err := decodeSavedTrackFilter(row)
			if err != nil {
				return err
			}
			item.Validation, err = savedTrackFilterValidation(tx, auth, item.Filter)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return nil
	})
	return items, err
}

func (s *Store) GetSavedTrackFilter(auth authContext, id string) (SavedTrackFilter, error) {
	var result SavedTrackFilter
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		if _, err := tagActor(tx, auth); err != nil {
			return err
		}
		var err error
		result, _, err = readSavedTrackFilter(tx, auth, id)
		if err != nil {
			return err
		}
		result.Validation, err = savedTrackFilterValidation(tx, auth, result.Filter)
		return err
	})
	return result, err
}

func (s *Store) CreateSavedTrackFilter(auth authContext, input SavedTrackFilterInput) (SavedTrackFilter, error) {
	var result SavedTrackFilter
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		name, normalized, err := normalizeTagName(input.Name)
		if err != nil {
			return err
		}
		if err := checkSavedTrackFilterName(tx, actor.ID, "", normalized); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM saved_track_filters WHERE user_id=?", actor.ID).Scan(&count); err != nil {
			return err
		}
		if count >= savedTrackFilterLimit {
			return errTagLimit
		}
		raw, err := encodeSavedTrackFilter(tx, auth, input.Filter)
		if err != nil {
			return err
		}
		now := persistedTime(time.Now())
		result = SavedTrackFilter{ID: uuid.NewString(), Name: name, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := json.Unmarshal([]byte(raw), &result.Filter); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO saved_track_filters VALUES(?,?,?,?,1,?,?)", result.ID, actor.ID, name, raw, now, now); err != nil {
			return err
		}
		result.Validation, err = savedTrackFilterValidation(tx, auth, result.Filter)
		if err != nil {
			return err
		}
		return auditTagChange(tx, auth, actor.ID, "filter.create", result.ID, nil, nil)
	})
	return result, err
}

func (s *Store) UpdateSavedTrackFilter(auth authContext, id string, patch SavedTrackFilterPatch, expected int64) (SavedTrackFilter, error) {
	var result SavedTrackFilter
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		current, raw, err := readSavedTrackFilter(tx, auth, id)
		if err != nil {
			return err
		}
		if err := expectedTagVersion(current.Version, expected); err != nil {
			return err
		}
		if patch.Name == nil && patch.Filter == nil {
			return errTagInvalid
		}
		name := current.Name
		if patch.Name != nil {
			var normalized string
			name, normalized, err = normalizeTagName(*patch.Name)
			if err != nil {
				return err
			}
			if err := checkSavedTrackFilterName(tx, actor.ID, id, normalized); err != nil {
				return err
			}
		}
		updatedRaw := raw
		if patch.Filter != nil {
			updatedRaw, err = encodeSavedTrackFilter(tx, auth, *patch.Filter)
			if err != nil {
				return err
			}
		}
		result = current
		if name != current.Name || raw != updatedRaw {
			result.Name, result.Version, result.UpdatedAt = name, current.Version+1, persistedTime(time.Now())
			result.Filter = TrackFilter{}
			if err := json.Unmarshal([]byte(updatedRaw), &result.Filter); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE saved_track_filters SET name=?,query_json=?,version=?,updated_at=? WHERE id=? AND user_id=?", result.Name, updatedRaw, result.Version, result.UpdatedAt, id, actor.ID); err != nil {
				return err
			}
			if err := auditTagChange(tx, auth, actor.ID, "filter.update", id, nil, nil); err != nil {
				return err
			}
		}
		result.Validation, err = savedTrackFilterValidation(tx, auth, result.Filter)
		return err
	})
	return result, err
}

func (s *Store) DeleteSavedTrackFilter(auth authContext, id string, expected int64) error {
	return s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		current, _, err := readSavedTrackFilter(tx, auth, id)
		if err != nil {
			return err
		}
		if err := expectedTagVersion(current.Version, expected); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM saved_track_filters WHERE id=? AND user_id=?", id, actor.ID); err != nil {
			return err
		}
		return auditTagChange(tx, auth, actor.ID, "filter.delete", id, nil, nil)
	})
}
