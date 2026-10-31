package music

import (
	"database/sql"
	"time"
)

func tagUsedByActiveBatch(tx *sql.Tx, id string) (bool, error) {
	var used bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tag_batches b,json_each(b.payload,'$.add_ids') j WHERE b.expires_at>?1 AND json_extract(b.payload,'$.state') IN ('preview','running') AND j.value=?2)
OR EXISTS(SELECT 1 FROM tag_batches b,json_each(b.payload,'$.remove_ids') j WHERE b.expires_at>?1 AND json_extract(b.payload,'$.state') IN ('preview','running') AND j.value=?2)`, persistedTime(time.Now()), id).Scan(&used)
	return used, err
}
