package music

import "database/sql"

// Freeze the complete SQL result, rather than the currently loaded page.
// The task keeps UUIDs and per-track tag versions after this transaction.
func filteredTagBatchIDs(tx *sql.Tx, auth authContext, q TagBatchQuery) ([]string, error) {
	filter, _, err := resolveTrackFilter(tx, auth, *q.Conditions)
	if err != nil {
		return nil, err
	}
	baseline, err := trackFilterBaseline(tx, auth)
	if err != nil {
		return nil, err
	}
	if err := checkTrackFilterBaseline(baseline, q.Baseline); err != nil {
		return nil, err
	}
	where, args, err := compileTrackFilter(tx, auth, filter, "", "")
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query("SELECT t.id FROM tracks t WHERE "+where+" ORDER BY t.id LIMIT ?", append(args, tagBatchMaxTracks+1)...)
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
