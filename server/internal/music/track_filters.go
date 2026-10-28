package music

import (
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var errFilterReference = errors.New("filter contains an unavailable tag reference")
var errFilterSnapshot = errors.New("filter snapshot changed")

type TrackFilter struct {
	Version          int              `json:"version"`
	Search           string           `json:"q,omitempty"`
	Range            string           `json:"range,omitempty"`
	Tags             []TrackTagClause `json:"tags,omitempty"`
	Artists          []string         `json:"artists,omitempty"`
	Albums           []string         `json:"albums,omitempty"`
	Formats          []string         `json:"formats,omitempty"`
	DurationMinMS    *int64           `json:"duration_min_ms,omitempty"`
	DurationMaxMS    *int64           `json:"duration_max_ms,omitempty"`
	UploadedBy       string           `json:"uploaded_by,omitempty"`
	HasLyrics        *bool            `json:"has_lyrics,omitempty"`
	HasCover         *bool            `json:"has_cover,omitempty"`
	UntaggedPublic   bool             `json:"untagged_public,omitempty"`
	UntaggedPersonal bool             `json:"untagged_personal,omitempty"`
}
type TrackTagClause struct {
	Scope      string   `json:"scope"`
	GroupID    string   `json:"group_id"`
	Match      string   `json:"match,omitempty"`
	IncludeIDs []string `json:"include_ids,omitempty"`
	ExcludeIDs []string `json:"exclude_ids,omitempty"`
}
type TagRedirect struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type TrackFilterBaseline struct {
	AccountID string `json:"account_id"`
	Library   int64  `json:"library"`
	Public    int64  `json:"public"`
	Personal  int64  `json:"personal"`
}
type TrackFilterQuery struct {
	Filter    TrackFilter          `json:"filter"`
	Sort      string               `json:"sort,omitempty"`
	Direction string               `json:"direction,omitempty"`
	Offset    int                  `json:"offset,omitempty"`
	Limit     int                  `json:"limit,omitempty"`
	Baseline  *TrackFilterBaseline `json:"baseline,omitempty"`
}
type TrackFilterPage struct {
	Items     []map[string]any    `json:"items"`
	Total     int                 `json:"total"`
	Offset    int                 `json:"offset"`
	Limit     int                 `json:"limit"`
	Baseline  TrackFilterBaseline `json:"baseline"`
	Redirects []TagRedirect       `json:"redirects"`
}

func validFilterText(value string, max int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func filterTextList(values []string, max int) ([]string, error) {
	if len(values) > max {
		return nil, errTagInvalid
	}
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || !validFilterText(value, 256) {
			return nil, errTagInvalid
		}
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result, nil
}

func resolveFilterTag(reader sqlReader, actor User, id, scopeID, groupID string) (string, error) {
	if !tagUUID(id) {
		return "", errTagInvalid
	}
	seen := map[string]bool{}
	for hop := 0; hop < 16; hop++ {
		if seen[id] {
			return "", errFilterReference
		}
		seen[id] = true
		tag, err := readTag(reader, id)
		if err != nil {
			if errors.Is(err, errResourceHidden) && hop > 0 {
				return "", errFilterReference
			}
			return "", err
		}
		if err := authorizeTag(actor, tag, false); err != nil {
			return "", err
		}
		if tag.scopeID != scopeID || tag.GroupID != groupID {
			return "", errTagInvalid
		}
		if tag.MergedInto != "" {
			id = tag.MergedInto
			continue
		}
		if tag.Deleted {
			return "", errFilterReference
		}
		return id, nil
	}
	return "", errFilterReference
}

func resolveTrackFilter(reader sqlReader, auth authContext, input TrackFilter) (TrackFilter, []TagRedirect, error) {
	f := input
	redirects := []TagRedirect{}
	actor, err := tagActor(reader, auth)
	if err != nil {
		return f, nil, err
	}
	if err := requireTagsSchema(reader); err != nil {
		return f, nil, err
	}
	if f.Version != 1 || !validFilterText(f.Search, 256) || len(f.Tags) > 12 {
		return f, nil, errTagInvalid
	}
	f.Search = strings.TrimSpace(f.Search)
	if f.Range == "" {
		f.Range = "all"
	}
	switch f.Range {
	case "all", "mine", "managed", "orphan":
	case "missing":
		if actor.Role != "admin" {
			return f, nil, errResourceHidden
		}
	default:
		return f, nil, errTagInvalid
	}
	if f.UploadedBy != "" {
		if actor.Role != "admin" {
			return f, nil, errTagForbidden
		}
		if !tagUUID(f.UploadedBy) {
			return f, nil, errTagInvalid
		}
		if _, err := readUser(reader, f.UploadedBy); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return f, nil, errResourceHidden
			}
			return f, nil, err
		}
	}
	if f.DurationMinMS != nil && *f.DurationMinMS < 0 || f.DurationMaxMS != nil && *f.DurationMaxMS < 0 || f.DurationMinMS != nil && f.DurationMaxMS != nil && *f.DurationMaxMS < *f.DurationMinMS {
		return f, nil, errTagInvalid
	}
	if f.Artists, err = filterTextList(f.Artists, 20); err != nil {
		return f, nil, err
	}
	if f.Albums, err = filterTextList(f.Albums, 20); err != nil {
		return f, nil, err
	}
	if len(f.Formats) > len(audioExtensions) {
		return f, nil, errTagInvalid
	}
	f.Formats = slices.Clone(f.Formats)
	seenFormats := map[string]bool{}
	formats := []string{}
	for _, format := range f.Formats {
		format = strings.ToLower(strings.TrimSpace(format))
		if !audioExtensions["."+format] {
			return f, nil, errTagInvalid
		}
		if !seenFormats[format] {
			formats = append(formats, format)
			seenFormats[format] = true
		}
	}
	f.Formats = formats
	f.Tags = slices.Clone(f.Tags)
	groups := map[string]bool{}
	total := 0
	redirectSeen := map[string]bool{}
	for i, clause := range f.Tags {
		if len(clause.IncludeIDs)+len(clause.ExcludeIDs) == 0 {
			return f, nil, errTagInvalid
		}
		if clause.Scope != "public" && clause.Scope != "personal" || !tagGroupID(clause.GroupID) {
			return f, nil, errTagInvalid
		}
		key := clause.Scope + "/" + clause.GroupID
		if groups[key] {
			return f, nil, errTagInvalid
		}
		groups[key] = true
		if _, err := readTagGroup(reader, clause.GroupID); err != nil {
			if errors.Is(err, errResourceHidden) {
				return f, nil, errFilterReference
			}
			return f, nil, err
		}
		if clause.Match == "" {
			clause.Match = "any"
		}
		if clause.Match != "any" && clause.Match != "all" {
			return f, nil, errTagInvalid
		}
		total += len(clause.IncludeIDs) + len(clause.ExcludeIDs)
		if total > 64 {
			return f, nil, errTagInvalid
		}
		scopeID, _ := tagScopeID(actor.ID, clause.Scope)
		canonical := make([][]string, 2)
		for setIndex, ids := range [][]string{clause.IncludeIDs, clause.ExcludeIDs} {
			originalSeen := map[string]bool{}
			canonicalSeen := map[string]bool{}
			canonical[setIndex] = []string{}
			for _, id := range ids {
				if originalSeen[id] {
					return f, nil, errTagInvalid
				}
				originalSeen[id] = true
				target, err := resolveFilterTag(reader, actor, id, scopeID, clause.GroupID)
				if err != nil {
					return f, nil, err
				}
				if target != id && !redirectSeen[id] {
					redirects = append(redirects, TagRedirect{From: id, To: target})
					redirectSeen[id] = true
				}
				if !canonicalSeen[target] {
					canonical[setIndex] = append(canonical[setIndex], target)
					canonicalSeen[target] = true
				}
			}
		}
		clause.IncludeIDs, clause.ExcludeIDs = canonical[0], canonical[1]
		f.Tags[i] = clause
	}
	return f, redirects, nil
}

const filterVisibleLink = `JOIN tags ft ON ft.id=fl.tag_id AND ft.scope_id=fl.scope_id WHERE fl.track_id=t.id AND ft.deleted=0 AND fl.scope_id=? AND NOT EXISTS(SELECT 1 FROM track_tag_overrides fo WHERE fo.track_id=fl.track_id AND fo.tag_id=fl.tag_id AND fo.scope_id=fl.scope_id AND fo.action='suppressed')`

func filterPlaceholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

func compileTrackFilter(reader sqlReader, auth authContext, f TrackFilter, omitScope, omitGroup string) (string, []any, error) {
	actor, err := tagActor(reader, auth)
	if err != nil {
		return "", nil, err
	}
	parts := []string{"t.removed=0"}
	args := []any{}
	switch f.Range {
	case "mine":
		parts = append(parts, "t.uploaded_by=?")
		args = append(args, actor.ID)
	case "managed":
		parts = append(parts, "t.owner_id=?")
		args = append(args, actor.ID)
	case "orphan":
		parts = append(parts, "NOT EXISTS(SELECT 1 FROM playlists fp JOIN playlist_tracks fm ON fm.playlist_id=fp.id WHERE fp.owner_id=? AND fp.system=0 AND fm.track_id=t.id)")
		args = append(args, actor.ID)
	case "missing":
		if actor.Role != "admin" {
			return "", nil, errResourceHidden
		}
		parts = append(parts, "t.missing=1")
	}
	if f.UploadedBy != "" {
		if actor.Role != "admin" {
			return "", nil, errTagForbidden
		}
		parts = append(parts, "t.uploaded_by=?")
		args = append(args, f.UploadedBy)
	}
	if f.Search != "" {
		parts = append(parts, browseTrackSearch)
		args = append(args, f.Search)
	}
	for _, item := range []struct {
		field  string
		values []string
	}{{"artist", f.Artists}, {"album", f.Albums}, {"format", f.Formats}} {
		if len(item.values) > 0 {
			parts = append(parts, "COALESCE(json_extract(t.payload,'$."+item.field+"'),'') COLLATE NOCASE IN ("+filterPlaceholders(len(item.values))+")")
			for _, value := range item.values {
				args = append(args, value)
			}
		}
	}
	if f.DurationMinMS != nil {
		parts = append(parts, "COALESCE(json_extract(t.payload,'$.duration_ms'),0)>=?")
		args = append(args, *f.DurationMinMS)
	}
	if f.DurationMaxMS != nil {
		parts = append(parts, "COALESCE(json_extract(t.payload,'$.duration_ms'),0)<=?")
		args = append(args, *f.DurationMaxMS)
	}
	for _, item := range []struct {
		value *bool
		field string
	}{{f.HasLyrics, "lyrics"}, {f.HasCover, "cover_id"}} {
		if item.value != nil {
			op := "!=''"
			if !*item.value {
				op = "=''"
			}
			parts = append(parts, "COALESCE(json_extract(t.payload,'$."+item.field+"'),'')"+op)
		}
	}
	for _, item := range []struct {
		enabled bool
		scope   string
	}{{f.UntaggedPublic, "public"}, {f.UntaggedPersonal, actor.ID}} {
		if item.enabled {
			parts = append(parts, "NOT EXISTS(SELECT 1 FROM track_tags fl "+filterVisibleLink+")")
			args = append(args, item.scope)
		}
	}
	for _, clause := range f.Tags {
		if clause.Scope == omitScope && clause.GroupID == omitGroup {
			continue
		}
		scopeID, _ := tagScopeID(actor.ID, clause.Scope)
		for setIndex, ids := range [][]string{clause.IncludeIDs, clause.ExcludeIDs} {
			if len(ids) == 0 {
				continue
			}
			prefix, suffix := "EXISTS(SELECT 1 FROM track_tags fl ", ")"
			if setIndex == 1 {
				prefix = "NOT EXISTS(SELECT 1 FROM track_tags fl "
			} else if clause.Match == "all" {
				prefix = "(SELECT count(DISTINCT fl.tag_id) FROM track_tags fl "
				suffix = ")=?"
			}
			parts = append(parts, prefix+filterVisibleLink+" AND ft.group_id=? AND fl.tag_id IN ("+filterPlaceholders(len(ids))+")"+suffix)
			args = append(args, scopeID, clause.GroupID)
			for _, id := range ids {
				args = append(args, id)
			}
			if setIndex == 0 && clause.Match == "all" {
				args = append(args, len(ids))
			}
		}
	}
	return strings.Join(parts, " AND "), args, nil
}

func trackFilterBaseline(reader sqlReader, auth authContext) (TrackFilterBaseline, error) {
	actor, err := tagActor(reader, auth)
	if err != nil {
		return TrackFilterBaseline{}, err
	}
	baseline := TrackFilterBaseline{AccountID: actor.ID}
	err = reader.QueryRow(`SELECT (SELECT value FROM meta WHERE key='revision'),COALESCE((SELECT revision FROM tag_scopes WHERE id='public'),0),COALESCE((SELECT revision FROM tag_scopes WHERE id=?),0)`, actor.ID).Scan(&baseline.Library, &baseline.Public, &baseline.Personal)
	return baseline, err
}
func checkTrackFilterBaseline(current TrackFilterBaseline, expected *TrackFilterBaseline) error {
	if expected != nil && !reflect.DeepEqual(current, *expected) {
		return errFilterSnapshot
	}
	return nil
}

func filterPageLimits(offset, limit int) (int, error) {
	if limit == 0 {
		limit = 50
	}
	if offset < 0 || offset > 1000000 || limit < 1 || limit > 100 {
		return 0, errTagInvalid
	}
	return limit, nil
}

func (s *Store) QueryTrackFilter(auth authContext, q TrackFilterQuery) (TrackFilterPage, error) {
	page := TrackFilterPage{Items: []map[string]any{}, Redirects: []TagRedirect{}, Offset: q.Offset}
	limit, err := filterPageLimits(q.Offset, q.Limit)
	if err != nil {
		return page, err
	}
	page.Limit = limit
	if q.Sort == "" {
		q.Sort = "title"
	}
	if q.Direction == "" {
		q.Direction = "asc"
	}
	sorts := map[string]string{"title": "t.title COLLATE NOCASE", "artist": "COALESCE(json_extract(t.payload,'$.artist'),'') COLLATE NOCASE", "album": "COALESCE(json_extract(t.payload,'$.album'),'') COLLATE NOCASE", "duration_ms": "COALESCE(json_extract(t.payload,'$.duration_ms'),0)", "modified_at": "julianday(json_extract(t.payload,'$.modified_at'))"}
	sort, ok := sorts[q.Sort]
	if !ok || q.Direction != "asc" && q.Direction != "desc" {
		return page, errTagInvalid
	}
	err = s.tagTransaction(false, func(tx *sql.Tx) error {
		f, redirects, err := resolveTrackFilter(tx, auth, q.Filter)
		if err != nil {
			return err
		}
		page.Redirects = redirects
		if page.Baseline, err = trackFilterBaseline(tx, auth); err != nil {
			return err
		}
		if err := checkTrackFilterBaseline(page.Baseline, q.Baseline); err != nil {
			return err
		}
		where, args, err := compileTrackFilter(tx, auth, f, "", "")
		if err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT count(*) FROM tracks t WHERE "+where, args...).Scan(&page.Total); err != nil {
			return err
		}
		page.Items, err = queryJSON[map[string]any](tx, "SELECT json_remove(t.payload,'$.lyrics') FROM tracks t WHERE "+where+" ORDER BY "+sort+" "+strings.ToUpper(q.Direction)+",t.id ASC LIMIT ? OFFSET ?", append(args, page.Limit, page.Offset)...)
		if err != nil {
			return err
		}
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			item["detail_loaded"] = false
			if actor.Role != "admin" {
				item["path"] = ""
			}
		}
		return projectTrackTagSummaries(tx, actor.ID, page.Items)
	})
	return page, err
}
