package music

import "database/sql"

// Both nickname APIs publish the same author projection invalidations.
func recordUserPlaylistChanges(tx *sql.Tx, id string) error {
	rows, err := tx.Query("SELECT id,visibility FROM playlists WHERE owner_id=? AND visibility IN ('public','shared')", id)
	if err != nil {
		return err
	}
	published := []string{}
	catalogChanged := false
	for rows.Next() {
		var pid string
		var visibility string
		if err := rows.Scan(&pid, &visibility); err != nil {
			rows.Close()
			return err
		}
		published = append(published, pid)
		catalogChanged = catalogChanged || visibility == "public"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(published) > 0 {
		if catalogChanged {
			if err := bumpPublicCatalog(tx); err != nil {
				return err
			}
		}
		for _, pid := range published {
			if err := recordChange(tx, "playlist", pid, false); err != nil {
				return err
			}
		}
	}
	return nil
}
