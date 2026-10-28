package music

// TrackTagSummary is an account-only browse projection. It never becomes part
// of Track, a shared cache entry, or a player API response.
type TrackTagSummary struct {
	Items []TrackTagSummaryItem `json:"items"`
	Total int                   `json:"total"`
}

type TrackTagSummaryItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	GroupID string `json:"group_id"`
}

func projectTrackTagSummaries(reader sqlReader, actorID string, items []map[string]any) error {
	if len(items) == 0 {
		return nil
	}
	if len(items) > browseMaxLimit {
		return errTagInvalid
	}
	byID := make(map[string]map[string]any, len(items))
	args := []any{actorID}
	for _, item := range items {
		id, ok := item["id"].(string)
		if !ok || !tagUUID(id) {
			return errTagInvalid
		}
		byID[id] = item
		item["tag_summary"] = TrackTagSummary{Items: []TrackTagSummaryItem{}}
		args = append(args, id)
	}
	// Aggregate only this page. Window counts include all effective labels, but
	// the response carries at most three per track and never reads foreign scopes.
	rows, err := reader.Query(`SELECT track_id,id,name,scope_id,group_id,total FROM (
	 SELECT l.track_id,t.id,t.name,t.scope_id,t.group_id,
	 count(*) OVER(PARTITION BY l.track_id) AS total,
	 row_number() OVER(PARTITION BY l.track_id ORDER BY CASE WHEN t.scope_id='public' THEN 0 ELSE 1 END,g.sort_order,g.id,t.normalized_name,t.id) AS position
	 FROM track_tags l JOIN tags t ON t.id=l.tag_id AND t.scope_id=l.scope_id
	 JOIN tag_groups g ON g.id=t.group_id
	 WHERE t.deleted=0 AND t.scope_id IN ('public',?) AND l.track_id IN (`+filterPlaceholders(len(items))+`)
	 AND NOT EXISTS(SELECT 1 FROM track_tag_overrides o WHERE o.track_id=l.track_id AND o.tag_id=l.tag_id AND o.scope_id=l.scope_id AND o.action='suppressed')
	) WHERE position<=3 ORDER BY track_id,position`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var trackID, scopeID string
		var label TrackTagSummaryItem
		var total int
		if err := rows.Scan(&trackID, &label.ID, &label.Name, &scopeID, &label.GroupID, &total); err != nil {
			return err
		}
		label.Scope = "public"
		if scopeID != "public" {
			label.Scope = "personal"
		}
		item := byID[trackID]
		summary := item["tag_summary"].(TrackTagSummary)
		summary.Total = total
		summary.Items = append(summary.Items, label)
		item["tag_summary"] = summary
	}
	return rows.Err()
}
