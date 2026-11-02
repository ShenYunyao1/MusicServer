package music

import (
	"encoding/json"
	"strings"
)

func legacyTagBatchIDs(reader sqlReader, auth authContext, q TagBatchQuery) ([]string, error) {
	where, args, err := browseTrackWhere(reader, auth, browseQuery{Search: strings.TrimSpace(q.Search), Filter: q.Filter})
	if err != nil {
		return nil, err
	}
	rows, err := reader.Query("SELECT t.id FROM tracks t WHERE "+where+" ORDER BY t.title,t.id LIMIT ?", append(args, tagBatchMaxTracks+1)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > tagBatchMaxTracks {
		return nil, errTagLimit
	}
	return ids, nil
}

func tagBatchSelection(reader sqlReader, ids []string, scopeID string) ([]TagBatchItem, map[string]int, error) {
	input, err := json.Marshal(ids)
	if err != nil {
		return nil, nil, err
	}
	rows, err := reader.Query(`SELECT t.id,COALESCE(v.version,1) FROM json_each(?) selected JOIN tracks t ON t.id=selected.value AND t.removed=0 LEFT JOIN track_tag_states v ON v.track_id=t.id AND v.scope_id=? ORDER BY selected.key`, string(input), scopeID)
	if err != nil {
		return nil, nil, err
	}
	items := []TagBatchItem{}
	for rows.Next() {
		var item TagBatchItem
		item.State = "pending"
		if err = rows.Scan(&item.TrackID, &item.Version); err != nil {
			rows.Close()
			return nil, nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(items) != len(ids) {
		return nil, nil, errResourceHidden
	}
	rows, err = reader.Query(`SELECT l.tag_id,count(*) FROM json_each(?) selected JOIN track_tags l ON l.track_id=selected.value AND l.scope_id=? JOIN tags ON tags.id=l.tag_id AND tags.scope_id=l.scope_id AND tags.deleted=0 WHERE NOT EXISTS(SELECT 1 FROM track_tag_overrides o WHERE o.track_id=l.track_id AND o.tag_id=l.tag_id AND o.scope_id=l.scope_id AND o.action='suppressed') GROUP BY l.tag_id`, string(input), scopeID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, nil, err
		}
		counts[id] = count
	}
	return items, counts, rows.Err()
}
