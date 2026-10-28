package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

const tagImportSchema = `CREATE TABLE track_tag_imports (
 track_id TEXT PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
 values_json TEXT NOT NULL CHECK(json_valid(values_json)),
 unmapped_json TEXT NOT NULL CHECK(json_valid(unmapped_json)),
 truncated INTEGER NOT NULL CHECK(truncated IN(0,1)));`

type TrackTagImport struct {
	Values    []string `json:"values"`
	Unmapped  []string `json:"unmapped"`
	Truncated bool     `json:"truncated"`
}

func readTrackTagImport(reader sqlReader, id string) (TrackTagImport, error) {
	source := TrackTagImport{Values: []string{}, Unmapped: []string{}}
	var values, unmapped string
	err := reader.QueryRow("SELECT values_json,unmapped_json,truncated FROM track_tag_imports WHERE track_id=?", id).Scan(&values, &unmapped, &source.Truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return source, nil
	}
	if err != nil {
		return source, err
	}
	if err = json.Unmarshal([]byte(values), &source.Values); err != nil {
		return source, err
	}
	err = json.Unmarshal([]byte(unmapped), &source.Unmapped)
	return source, err
}

func (s *Store) GetTrackTagImport(auth authContext, id string) (TrackTagImport, error) {
	var source TrackTagImport
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		if _, err := tagActor(tx, auth); err != nil {
			return err
		}
		if _, err := readTrackFrom(tx, id, false); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errResourceHidden
			}
			return err
		}
		var err error
		source, err = readTrackTagImport(tx, id)
		return err
	})
	return source, err
}

// This helper shares the scan/upload transaction. The caller combines its
// result with track changes, so one import cannot publish a partial track.
func syncImportedGenreTags(tx *sql.Tx, id string, values []string) (bool, error) {
	if values == nil {
		return false, nil
	}
	var schema int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		return false, err
	}
	if schema == 7 {
		return false, nil
	}
	if err := requireTagsSchema(tx); err != nil {
		return false, err
	}
	track, err := readTrackFrom(tx, id, false)
	if err != nil {
		return false, err
	}
	old, err := readTrackTagImport(tx, id)
	if err != nil {
		return false, err
	}
	source := TrackTagImport{Values: []string{}, Unmapped: []string{}, Truncated: len(values) > 64}
	for _, value := range values[:min(len(values), 64)] {
		if !utf8.ValidString(value) || len(value) > 1024 {
			source.Truncated = true
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			source.Values = append(source.Values, value)
		}
	}
	type link struct{ source, override, group string }
	links := map[string]link{}
	rows, err := tx.Query(`SELECT t.id,COALESCE(l.source,''),COALESCE(o.action,''),t.group_id FROM tags t
 LEFT JOIN track_tags l ON l.tag_id=t.id AND l.track_id=?
 LEFT JOIN track_tag_overrides o ON o.tag_id=t.id AND o.track_id=?
 WHERE t.scope_id='public' AND t.deleted=0 AND (l.track_id IS NOT NULL OR o.track_id IS NOT NULL)`, id, id)
	if err != nil {
		return false, err
	}
	protected := 0
	for rows.Next() {
		var tagID string
		var item link
		if err = rows.Scan(&tagID, &item.source, &item.override, &item.group); err != nil {
			rows.Close()
			return false, err
		}
		links[tagID] = item
		if item.source != "" && (item.group != "genre" || item.source == "manual" || item.override == "confirmed") {
			protected++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	desired := map[string]bool{}
	for _, value := range source.Values {
		_, normalized, e := normalizeTagName(value)
		if e != nil {
			source.Unmapped = append(source.Unmapped, value)
			continue
		}
		var tagID string
		e = tx.QueryRow(`SELECT t.id FROM tag_aliases a JOIN tags t ON t.id=a.tag_id
 WHERE a.scope_id='public' AND a.group_id='genre' AND a.normalized_name=? AND t.deleted=0`, normalized).Scan(&tagID)
		if errors.Is(e, sql.ErrNoRows) {
			source.Unmapped = append(source.Unmapped, value)
			continue
		}
		if e != nil {
			return false, e
		}
		item := links[tagID]
		if item.override == "suppressed" || item.source == "manual" || item.override == "confirmed" {
			continue
		}
		if !desired[tagID] && protected+len(desired) >= maxTrackTags {
			source.Unmapped = append(source.Unmapped, value)
			source.Truncated = true
			continue
		}
		desired[tagID] = true
	}
	changed := false
	for tagID, item := range links {
		if item.group == "genre" && item.source == "imported" && item.override != "confirmed" && (!desired[tagID] || item.override == "suppressed") {
			if _, err = tx.Exec("DELETE FROM track_tags WHERE track_id=? AND tag_id=?", id, tagID); err != nil {
				return false, err
			}
			changed = true
		}
	}
	for tagID := range desired {
		if links[tagID].source != "" {
			continue
		}
		if _, err = tx.Exec("INSERT INTO track_tags VALUES(?,?,'public','imported',?,?)", id, tagID, track.OwnerID, persistedTime(time.Now())); err != nil {
			return false, err
		}
		changed = true
	}
	if !reflect.DeepEqual(old, source) {
		encoded, _ := json.Marshal(source.Values)
		unmapped, _ := json.Marshal(source.Unmapped)
		if _, err = tx.Exec("INSERT INTO track_tag_imports VALUES(?,?,?,?) ON CONFLICT(track_id) DO UPDATE SET values_json=excluded.values_json,unmapped_json=excluded.unmapped_json,truncated=excluded.truncated", id, string(encoded), string(unmapped), source.Truncated); err != nil {
			return false, err
		}
		changed = true
	}
	if changed {
		if err = bumpTrackTagVersion(tx, id, "public"); err != nil {
			return false, err
		}
		if err = bumpTagScope(tx, "public"); err != nil {
			return false, err
		}
	}
	return changed, nil
}

func (a *API) tagImportHTTP(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/api/v2/tracks/"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/tag-import-source") {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if !a.requireAccount(w, r) {
		return true
	}
	if err := requireTagsSchema(a.store.db); err != nil {
		writeTagError(w, err)
		return true
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/tag-import-source")
	if !tagUUID(id) {
		writeTagError(w, errResourceHidden)
		return true
	}
	if !tagNoQuery(w, r) {
		return true
	}
	if r.Method != "GET" {
		tagMethod(w, "GET")
		return true
	}
	source, err := a.store.GetTrackTagImport(authFromRequest(r), id)
	if err != nil {
		writeTagError(w, err)
		return true
	}
	writeJSON(w, 200, source)
	return true
}
