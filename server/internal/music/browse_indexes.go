package music

import (
	"database/sql"
	"fmt"
)

// Rebuildable performance structures, compatible with schema 8 and 9. They do
// not change domain records or migration edges. Counters and row mutations are
// committed atomically by SQLite, including writes from maintenance paths.
func initializeBrowseIndexes(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS tracks_cover_lookup ON tracks(json_extract(payload,'$.cover_id'));
 CREATE INDEX IF NOT EXISTS playlists_cover_lookup ON playlists(json_extract(payload,'$.cover_id'));
 INSERT OR IGNORE INTO meta(key,value) VALUES('browse/epoch',lower(hex(randomblob(16)))),('browse/library','0'),('browse/admin','0');`); err != nil {
		return err
	}
	bump := func(key string) string {
		return "INSERT INTO meta(key,value) VALUES(" + key + ",'1') ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1;"
	}
	account := func(value string) string {
		return "INSERT INTO meta(key,value) SELECT 'browse/account/'||" + value + ",'1' WHERE " + value + " IS NOT NULL ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1;"
	}
	playlist := func(alias string) string {
		return bump("'browse/admin'") + account(alias+".owner_id") + "INSERT INTO meta(key,value) SELECT 'browse/account/'||user_id,'1' FROM subscriptions WHERE playlist_id=" + alias + ".id ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1;"
	}
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		alias := "NEW"
		if event == "DELETE" {
			alias = "OLD"
		}
		definitions := map[string]string{
			"tracks":               bump("'browse/library'"),
			"playlists":            playlist(alias),
			"subscriptions":        account(alias + ".user_id"),
			"playlist_invitations": account(alias+".recipient_id") + account(alias+".inviter_id"),
		}
		if event == "UPDATE" {
			definitions["playlists"] += account("OLD.owner_id")
			definitions["subscriptions"] += account("OLD.user_id")
		}
		for table, body := range definitions {
			if _, err = tx.Exec(fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS browse_%s_%s AFTER %s ON %s BEGIN %s END", table, event, event, table, body)); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`CREATE TRIGGER IF NOT EXISTS browse_user_visibility AFTER UPDATE OF enabled,display_name,role ON users BEGIN
 UPDATE meta SET value=CAST(value AS INTEGER)+1 WHERE key IN ('browse/library','browse/admin');
 INSERT INTO meta(key,value) VALUES('browse/account/'||NEW.id,'1') ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1;
 INSERT INTO meta(key,value) SELECT DISTINCT 'browse/account/'||s.user_id,'1' FROM subscriptions s JOIN playlists p ON p.id=s.playlist_id WHERE p.owner_id=NEW.id ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1;
 END;`); err != nil {
		return err
	}
	return tx.Commit()
}
