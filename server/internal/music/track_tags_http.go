package music

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Tag routes precede the generic track-owner guard: personal tags belong to
// the reader, while public tag bindings still require track ownership.
func (a *API) trackTagsHTTP(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	isTrack := strings.HasPrefix(path, "/api/v2/tracks/") && strings.HasSuffix(path, "/tags")
	if path != "/api/v2/tags" && !strings.HasPrefix(path, "/api/v2/tags/") && path != "/api/v2/tag-groups" && !strings.HasPrefix(path, "/api/v2/tag-groups/") && !isTrack {
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
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	parts := strings.Split(strings.TrimPrefix(path, "/api/v2/"), "/")
	if isTrack {
		if len(parts) != 3 || !tagUUID(parts[1]) {
			writeTagError(w, errResourceHidden)
			return true
		}
		a.trackTagBindingsHTTP(w, r, parts[1])
		return true
	}
	if len(parts) > 3 || len(parts) > 1 && !(parts[0] == "tag-groups" && tagGroupID(parts[1]) || parts[0] == "tags" && tagUUID(parts[1])) || len(parts) == 3 && (parts[0] != "tags" || parts[2] != "merge") {
		writeTagError(w, errResourceHidden)
		return true
	}
	if parts[0] == "tag-groups" {
		a.tagGroupsHTTP(w, r, parts)
	} else {
		a.tagDictionaryHTTP(w, r, parts)
	}
	return true
}

func tagUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
func tagGroupID(id string) bool {
	switch id {
	case "genre", "language", "version", "mood", "scene", "custom":
		return true
	}
	return tagUUID(id)
}

func writeTagError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errFilterReference):
		writeError(w, 409, "FILTER_REFERENCE_INVALID", "review unavailable tags referenced by this filter")
	case errors.Is(err, errFilterSnapshot):
		writeError(w, 409, "FILTER_SNAPSHOT_CHANGED", "filter data changed; reload from the first page")
	case errors.Is(err, errTagsUnavailable):
		writeError(w, 503, "TAGS_UNAVAILABLE", "tag features are not enabled for this database schema")
	case errors.Is(err, errTagSession):
		writeError(w, 401, "AUTH_INVALID", "a current account session is required")
	case errors.Is(err, errResourceHidden), errors.Is(err, sql.ErrNoRows):
		writeError(w, 404, "NOT_FOUND", "resource not found")
	case errors.Is(err, errTagForbidden):
		writeError(w, 403, "TAG_FORBIDDEN", "administrator permission required")
	case errors.Is(err, errVersionConflict):
		writeError(w, 409, "VERSION_CONFLICT", "resource changed; reload before retrying")
	case errors.Is(err, errTagNameTaken):
		writeError(w, 409, "TAG_NAME_EXISTS", "name or alias already exists in this group and scope")
	case errors.Is(err, errTagInUse):
		writeError(w, 409, "TAG_IN_USE", "resource is still referenced")
	case errors.Is(err, errTagLimit):
		writeError(w, 400, "TAG_LIMIT", "tag limit exceeded")
	case errors.Is(err, errTagInvalid):
		writeError(w, 400, "TAG_INVALID", "invalid tag request")
	default:
		writeError(w, 500, "TAG_FAILED", "tag operation failed")
	}
}

func tagJSON[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var input *T
	if err := decodeStrict(r, &input); err != nil || input == nil {
		writeTagError(w, errTagInvalid)
		var zero T
		return zero, false
	}
	return *input, true
}

func tagExpectedVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		writeError(w, 428, "VERSION_REQUIRED", "If-Match with current version is required")
		return 0, false
	}
	if len(values) != 1 {
		writeTagError(w, errTagInvalid)
		return 0, false
	}
	value := strings.TrimSpace(values[0])
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			writeTagError(w, errTagInvalid)
			return 0, false
		}
	}
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 1 {
		writeTagError(w, errTagInvalid)
		return 0, false
	}
	return version, true
}

func tagNoQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" {
		writeTagError(w, errTagInvalid)
		return false
	}
	return true
}

func tagMethod(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
}

func tagEntity(w http.ResponseWriter, status int, version int64, entity any) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(version, 10)+`"`)
	writeJSON(w, status, entity)
}

func (a *API) tagGroupsHTTP(w http.ResponseWriter, r *http.Request, parts []string) {
	if !tagNoQuery(w, r) {
		return
	}
	auth := authFromRequest(r)
	if len(parts) == 1 {
		switch r.Method {
		case "GET":
			groups, err := a.store.ListTagGroups(auth)
			if err != nil {
				writeTagError(w, err)
				return
			}
			writeJSON(w, 200, groups)
		case "POST":
			input, ok := tagJSON[TagGroupInput](w, r)
			if !ok {
				return
			}
			group, err := a.store.CreateTagGroup(auth, input)
			if err != nil {
				writeTagError(w, err)
				return
			}
			tagEntity(w, 201, group.Version, group)
		default:
			tagMethod(w, "GET, POST")
		}
		return
	}
	if r.Method != "PATCH" && r.Method != "DELETE" {
		tagMethod(w, "PATCH, DELETE")
		return
	}
	version, ok := tagExpectedVersion(w, r)
	if !ok {
		return
	}
	if r.Method == "DELETE" {
		if err := a.store.DeleteTagGroup(auth, parts[1], version); err != nil {
			writeTagError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	input, ok := tagJSON[TagGroupPatch](w, r)
	if !ok {
		return
	}
	group, err := a.store.UpdateTagGroup(auth, parts[1], input, version)
	if err != nil {
		writeTagError(w, err)
		return
	}
	tagEntity(w, 200, group.Version, group)
}

func tagListQuery(r *http.Request) (TagQuery, error) {
	query := TagQuery{Scope: "public", Limit: 50}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return query, errTagInvalid
	}
	for key, items := range values {
		if len(items) != 1 {
			return query, errTagInvalid
		}
		switch key {
		case "scope":
			query.Scope = items[0]
			if query.Scope != "public" && query.Scope != "personal" {
				return query, errTagInvalid
			}
		case "group_id":
			query.GroupID = items[0]
			if !tagGroupID(query.GroupID) {
				return query, errTagInvalid
			}
		case "q":
			query.Query = items[0]
			if !utf8.ValidString(query.Query) || utf8.RuneCountInString(query.Query) > 64 {
				return query, errTagInvalid
			}
		case "offset":
			query.Offset, err = strconv.Atoi(items[0])
			if err != nil || query.Offset < 0 {
				return query, errTagInvalid
			}
		case "limit":
			query.Limit, err = strconv.Atoi(items[0])
			if err != nil || query.Limit < 1 || query.Limit > 100 {
				return query, errTagInvalid
			}
		default:
			return query, errTagInvalid
		}
	}
	return query, nil
}

func (a *API) tagDictionaryHTTP(w http.ResponseWriter, r *http.Request, parts []string) {
	auth := authFromRequest(r)
	if len(parts) == 1 && r.Method == "GET" {
		query, err := tagListQuery(r)
		if err != nil {
			writeTagError(w, err)
			return
		}
		page, err := a.store.ListTags(auth, query)
		if err != nil {
			writeTagError(w, err)
			return
		}
		writeJSON(w, 200, page)
		return
	}
	if !tagNoQuery(w, r) {
		return
	}
	if len(parts) == 1 {
		if r.Method != "POST" {
			tagMethod(w, "GET, POST")
			return
		}
		input, ok := tagJSON[TagInput](w, r)
		if !ok {
			return
		}
		if input.Scope != "public" && input.Scope != "personal" || !tagGroupID(input.GroupID) {
			writeTagError(w, errTagInvalid)
			return
		}
		tag, err := a.store.CreateTag(auth, input)
		if err != nil {
			writeTagError(w, err)
			return
		}
		tagEntity(w, 201, tag.Version, tag)
		return
	}
	if len(parts) == 2 && r.Method == "GET" {
		tag, err := a.store.GetTag(auth, parts[1])
		if err != nil {
			writeTagError(w, err)
			return
		}
		tagEntity(w, 200, tag.Version, tag)
		return
	}
	if len(parts) == 3 && r.Method != "POST" {
		tagMethod(w, "POST")
		return
	}
	if len(parts) == 2 && r.Method != "PATCH" && r.Method != "DELETE" {
		tagMethod(w, "GET, PATCH, DELETE")
		return
	}
	version, ok := tagExpectedVersion(w, r)
	if !ok {
		return
	}
	if len(parts) == 3 {
		input, ok := tagJSON[struct {
			TargetID      string `json:"target_id"`
			TargetVersion *int64 `json:"target_version"`
		}](w, r)
		if !ok {
			return
		}
		if input.TargetVersion == nil {
			writeError(w, 428, "VERSION_REQUIRED", "target_version is required")
			return
		}
		if !tagUUID(input.TargetID) || *input.TargetVersion < 1 {
			writeTagError(w, errTagInvalid)
			return
		}
		result, err := a.store.MergeTags(auth, parts[1], input.TargetID, version, *input.TargetVersion)
		if err != nil {
			writeTagError(w, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	if r.Method == "DELETE" {
		if err := a.store.DeleteTag(auth, parts[1], version); err != nil {
			writeTagError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	input, ok := tagJSON[TagPatch](w, r)
	if !ok {
		return
	}
	tag, err := a.store.UpdateTag(auth, parts[1], input, version)
	if err != nil {
		writeTagError(w, err)
		return
	}
	tagEntity(w, 200, tag.Version, tag)
}

func (a *API) trackTagBindingsHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if !tagNoQuery(w, r) {
		return
	}
	auth := authFromRequest(r)
	if r.Method == "GET" {
		view, err := a.store.GetTrackTags(auth, id)
		if err != nil {
			writeTagError(w, err)
			return
		}
		writeJSON(w, 200, view)
		return
	}
	if r.Method != "PATCH" {
		tagMethod(w, "GET, PATCH")
		return
	}
	version, ok := tagExpectedVersion(w, r)
	if !ok {
		return
	}
	input, ok := tagJSON[struct {
		Scope     string   `json:"scope"`
		AddIDs    []string `json:"add_ids"`
		RemoveIDs []string `json:"remove_ids"`
	}](w, r)
	if !ok {
		return
	}
	if input.Scope != "public" && input.Scope != "personal" {
		writeTagError(w, errTagInvalid)
		return
	}
	for _, ids := range [][]string{input.AddIDs, input.RemoveIDs} {
		for _, id := range ids {
			if !tagUUID(id) {
				writeTagError(w, errTagInvalid)
				return
			}
		}
	}
	view, err := a.store.ChangeTrackTags(auth, id, input.Scope, version, input.AddIDs, input.RemoveIDs)
	if err != nil {
		writeTagError(w, err)
		return
	}
	writeJSON(w, 200, view)
}
