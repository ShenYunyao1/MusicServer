package music

import (
	"errors"
	"net/http"
	"strings"
)

func writeTrackFilterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errFilterReference):
		writeError(w, 409, "FILTER_REFERENCE_INVALID", "a tag referenced by this filter is no longer available; review the saved conditions")
	case errors.Is(err, errFilterSnapshot):
		writeError(w, 409, "FILTER_SNAPSHOT_CHANGED", "filter data changed; reload from the first page")
	case errors.Is(err, errSavedFilterNameTaken):
		writeError(w, 409, "FILTER_NAME_EXISTS", "a saved filter with this name already exists")
	default:
		writeTagError(w, err)
	}
}

func (a *API) trackFiltersHTTP(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/api/v2/track-query" && path != "/api/v2/track-facets" && path != "/api/v2/saved-track-filters" && !strings.HasPrefix(path, "/api/v2/saved-track-filters/") {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if !a.requireAccount(w, r) {
		return true
	}
	if err := requireTagsSchema(a.store.db); err != nil {
		writeTrackFilterError(w, err)
		return true
	}
	if !tagNoQuery(w, r) {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	auth := authFromRequest(r)
	if path == "/api/v2/track-query" || path == "/api/v2/track-facets" {
		if r.Method != "POST" {
			tagMethod(w, "POST")
			return true
		}
		if path == "/api/v2/track-query" {
			input, ok := tagJSON[TrackFilterQuery](w, r)
			if !ok {
				return true
			}
			page, err := a.store.QueryTrackFilter(auth, input)
			if err != nil {
				writeTrackFilterError(w, err)
				return true
			}
			writeJSON(w, 200, page)
		} else {
			input, ok := tagJSON[TagFacetQuery](w, r)
			if !ok {
				return true
			}
			page, err := a.store.TrackTagFacets(auth, input)
			if err != nil {
				writeTrackFilterError(w, err)
				return true
			}
			writeJSON(w, 200, page)
		}
		return true
	}
	if path == "/api/v2/saved-track-filters" {
		switch r.Method {
		case "GET":
			items, err := a.store.ListSavedTrackFilters(auth)
			if err != nil {
				writeTrackFilterError(w, err)
				return true
			}
			writeJSON(w, 200, items)
		case "POST":
			input, ok := tagJSON[SavedTrackFilterInput](w, r)
			if !ok {
				return true
			}
			item, err := a.store.CreateSavedTrackFilter(auth, input)
			if err != nil {
				writeTrackFilterError(w, err)
				return true
			}
			tagEntity(w, 201, item.Version, item)
		default:
			tagMethod(w, "GET, POST")
		}
		return true
	}
	id := strings.TrimPrefix(path, "/api/v2/saved-track-filters/")
	if !tagUUID(id) {
		writeTrackFilterError(w, errResourceHidden)
		return true
	}
	if r.Method == "GET" {
		item, err := a.store.GetSavedTrackFilter(auth, id)
		if err != nil {
			writeTrackFilterError(w, err)
			return true
		}
		tagEntity(w, 200, item.Version, item)
		return true
	}
	if r.Method != "PATCH" && r.Method != "DELETE" {
		tagMethod(w, "GET, PATCH, DELETE")
		return true
	}
	version, ok := tagExpectedVersion(w, r)
	if !ok {
		return true
	}
	if r.Method == "DELETE" {
		if err := a.store.DeleteSavedTrackFilter(auth, id, version); err != nil {
			writeTrackFilterError(w, err)
			return true
		}
		w.WriteHeader(204)
		return true
	}
	input, ok := tagJSON[SavedTrackFilterPatch](w, r)
	if !ok {
		return true
	}
	item, err := a.store.UpdateSavedTrackFilter(auth, id, input, version)
	if err != nil {
		writeTrackFilterError(w, err)
		return true
	}
	tagEntity(w, 200, item.Version, item)
	return true
}
