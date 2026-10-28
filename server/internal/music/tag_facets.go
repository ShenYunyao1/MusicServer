package music

import (
	"database/sql"
	"strings"
)

type TagFacetQuery struct {
	Filter   TrackFilter          `json:"filter"`
	Scope    string               `json:"scope"`
	GroupID  string               `json:"group_id"`
	Search   string               `json:"q,omitempty"`
	Offset   int                  `json:"offset,omitempty"`
	Limit    int                  `json:"limit,omitempty"`
	Baseline *TrackFilterBaseline `json:"baseline,omitempty"`
}
type TagFacetItem struct {
	Tag   Tag `json:"tag"`
	Count int `json:"count"`
}
type TagFacetPage struct {
	Items     []TagFacetItem      `json:"items"`
	Total     int                 `json:"total"`
	Offset    int                 `json:"offset"`
	Limit     int                 `json:"limit"`
	Baseline  TrackFilterBaseline `json:"baseline"`
	Redirects []TagRedirect       `json:"redirects"`
}

func (s *Store) TrackTagFacets(auth authContext, q TagFacetQuery) (TagFacetPage, error) {
	page := TagFacetPage{Items: []TagFacetItem{}, Redirects: []TagRedirect{}, Offset: q.Offset}
	limit, err := filterPageLimits(q.Offset, q.Limit)
	if err != nil {
		return page, err
	}
	page.Limit = limit
	if q.Scope != "public" && q.Scope != "personal" || !tagGroupID(q.GroupID) || !validFilterText(q.Search, 64) {
		return page, errTagInvalid
	}
	err = s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		if _, err := readTagGroup(tx, q.GroupID); err != nil {
			return err
		}
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
		scopeID, _ := tagScopeID(actor.ID, q.Scope)
		dictionaryWhere := "scope_id=? AND group_id=? AND deleted=0"
		dictionaryArgs := []any{scopeID, q.GroupID}
		if strings.TrimSpace(q.Search) != "" {
			_, normalized, err := normalizeTagName(q.Search)
			if err != nil {
				return err
			}
			dictionaryWhere += " AND EXISTS(SELECT 1 FROM tag_aliases a WHERE a.tag_id=tags.id AND instr(a.normalized_name,?)>0)"
			dictionaryArgs = append(dictionaryArgs, normalized)
		}
		if err := tx.QueryRow("SELECT count(*) FROM tags WHERE "+dictionaryWhere, dictionaryArgs...).Scan(&page.Total); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT id FROM tags WHERE "+dictionaryWhere+" ORDER BY normalized_name,id LIMIT ? OFFSET ?", append(dictionaryArgs, page.Limit, page.Offset)...)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		where, args, err := compileTrackFilter(tx, auth, f, q.Scope, q.GroupID)
		if err != nil {
			return err
		}
		// One grouped aggregate over the matching track scope and this page's
		// terms, rather than a query for every term in the full dictionary.
		countArgs := []any{scopeID, q.GroupID}
		for _, id := range ids {
			countArgs = append(countArgs, id)
		}
		countArgs = append(countArgs, args...)
		rows, err = tx.Query(`SELECT fl.tag_id,count(DISTINCT t.id) FROM tracks t JOIN track_tags fl ON fl.track_id=t.id JOIN tags ft ON ft.id=fl.tag_id AND ft.scope_id=fl.scope_id WHERE fl.scope_id=? AND ft.group_id=? AND ft.deleted=0 AND fl.tag_id IN (`+filterPlaceholders(len(ids))+`) AND NOT EXISTS(SELECT 1 FROM track_tag_overrides fo WHERE fo.track_id=fl.track_id AND fo.tag_id=fl.tag_id AND fo.scope_id=fl.scope_id AND fo.action='suppressed') AND (`+where+`) GROUP BY fl.tag_id`, countArgs...)
		if err != nil {
			return err
		}
		counts := map[string]int{}
		for rows.Next() {
			var id string
			var count int
			if err = rows.Scan(&id, &count); err != nil {
				rows.Close()
				return err
			}
			counts[id] = count
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			tag, err := readTag(tx, id)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, TagFacetItem{Tag: tag, Count: counts[id]})
		}
		return nil
	})
	return page, err
}
