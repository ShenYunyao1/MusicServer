package music

import (
	"database/sql"
	"fmt"
	"time"
)

var errTagContributionBudget = fmt.Errorf("%w: 公共词条贡献每账户每小时最多20个、24小时最多100个，请稍后重试", errTagLimit)

// Successful contributions are audited in the same transaction: cancellation,
// deletion and account restart cannot reset the rolling contribution budget.
func checkPublicContributionBudget(tx *sql.Tx, actor User) error {
	if actor.Role == "admin" {
		return nil
	}
	var hour, day int
	now := time.Now().UTC()
	if err := tx.QueryRow("SELECT COALESCE(sum(created_at>=?),0),count(*) FROM audit WHERE user_id=? AND action='tag.create' AND json_extract(changes,'$.after.scope')='public' AND created_at>=?", persistedTime(now.Add(-time.Hour)), actor.ID, persistedTime(now.Add(-24*time.Hour))).Scan(&hour, &day); err != nil {
		return err
	}
	if hour >= 20 || day >= 100 {
		return errTagContributionBudget
	}
	return nil
}

// Tombstones are invalid references, never selectable terms. Removing an unused
// tombstone keeps old saved filters invalid rather than broadening their query.
func compactTagTombstones(tx *sql.Tx, scope string) error {
	_, err := tx.Exec(`DELETE FROM tags WHERE id IN (SELECT t.id FROM tags t WHERE t.scope_id=? AND t.deleted=1
AND NOT EXISTS(SELECT 1 FROM tags child WHERE child.merged_into=t.id)
AND NOT EXISTS(SELECT 1 FROM track_tags l WHERE l.tag_id=t.id)
AND NOT EXISTS(SELECT 1 FROM track_tag_overrides o WHERE o.tag_id=t.id)
AND NOT EXISTS(SELECT 1 FROM tag_batches b,json_tree(b.payload) j WHERE json_extract(b.payload,'$.state')='running' AND b.expires_at>? AND j.type='text' AND j.value=t.id)
ORDER BY t.updated_at,t.id LIMIT 1000)`, scope, persistedTime(time.Now()))
	return err
}
