package music

import (
	"database/sql"
	"fmt"
	"time"
)

var errTagContributionBudget = fmt.Errorf("%w: 公共词条贡献每账户每小时最多20个、24小时最多100个，请稍后重试", errTagLimit)

type tagContributionRetry struct{ until time.Time }

func (e *tagContributionRetry) Error() string { return errTagContributionBudget.Error() }
func (e *tagContributionRetry) Unwrap() error { return errTagContributionBudget }

// Successful contributions are audited in the same transaction: cancellation,
// deletion and account restart cannot reset the rolling contribution budget.
func checkPublicContributionBudget(tx *sql.Tx, actor User) error {
	if actor.Role == "admin" {
		return nil
	}
	now := time.Now().UTC()
	rows, err := tx.Query("SELECT created_at FROM audit WHERE user_id=? AND action='tag.create' AND json_valid(changes) AND json_extract(changes,'$.after.scope')='public' AND created_at>=? ORDER BY created_at DESC LIMIT 100", actor.ID, persistedTime(now.Add(-24*time.Hour)))
	if err != nil {
		return err
	}
	defer rows.Close()
	var hour, day int
	var until time.Time
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		at, err := parsePersistedTime(raw)
		if err != nil {
			return err
		}
		day++
		if at.After(now.Add(-time.Hour)) {
			hour++
			if hour == 20 {
				until = at.Add(time.Hour)
			}
		}
		if day == 100 && at.Add(24*time.Hour).After(until) {
			until = at.Add(24 * time.Hour)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !until.IsZero() {
		return &tagContributionRetry{until: until}
	}
	return nil
}

// Tombstones are invalid references, never selectable terms. Removing an unused
// tombstone keeps old saved filters invalid rather than broadening their query.
func compactTagTombstones(tx *sql.Tx, scope string) error {
	result, err := tx.Exec(`DELETE FROM tags WHERE id IN (SELECT t.id FROM tags t WHERE t.scope_id=? AND t.deleted=1 AND t.merged_into IS NULL
AND NOT EXISTS(SELECT 1 FROM tags child WHERE child.merged_into=t.id)
AND NOT EXISTS(SELECT 1 FROM track_tags l WHERE l.tag_id=t.id)
AND NOT EXISTS(SELECT 1 FROM track_tag_overrides o WHERE o.tag_id=t.id)
AND NOT EXISTS(SELECT 1 FROM tag_batches b,json_tree(b.payload) j WHERE json_extract(b.payload,'$.state')='running' AND b.expires_at>? AND j.type='text' AND j.value=t.id)
ORDER BY t.updated_at,t.id LIMIT 1000)`, scope, persistedTime(time.Now()))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed > 0 {
		return bumpTagScope(tx, scope)
	}
	return nil
}

// Maintenance is independent of members' create budgets.
func (s *Store) CleanupTagDictionary() error {
	if s.schema < 8 {
		return nil
	}
	return s.tagTransaction(false, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM tags WHERE scope_id='public'").Scan(&count); err != nil {
			return err
		}
		if count < publicTagLimit {
			return nil
		}
		return compactTagTombstones(tx, "public")
	})
}
