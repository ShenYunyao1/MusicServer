package music

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log"
)

const browseIndexVersion = "5"

func browseIndexDigest(reader sqlReader) (string, error) {
	rows, err := reader.Query(`SELECT name,sql FROM sqlite_master WHERE (name GLOB 'browse_*' AND type IN ('table','trigger','index')) OR name IN ('tracks_cover_lookup','playlists_cover_lookup','audit_public_contributions','tags_scope_capacity') ORDER BY name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%s\x00%s\x00", name, statement)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), rows.Err()
}

// Rebuildable performance structures, compatible with schema 8 and 9. They do
// not change domain records or migration edges. Counters and row mutations are
// committed atomically by SQLite, including writes from maintenance paths.
func initializeBrowseIndexes(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current, expected string
	if err := tx.QueryRow("SELECT COALESCE((SELECT value FROM meta WHERE key='browse/schema_version'),''),COALESCE((SELECT value FROM meta WHERE key='browse/schema_digest'),'')").Scan(&current, &expected); err != nil {
		return err
	}
	digest, err := browseIndexDigest(tx)
	if err != nil {
		return err
	}
	if current == browseIndexVersion && digest == expected {
		return tx.Commit()
	}
	// These objects are derived caches only. Recreate definitions atomically;
	// retain every original table row, including data needing manual repair.
	rows, err := tx.Query("SELECT name FROM sqlite_master WHERE type='trigger' AND name GLOB 'browse_*'")
	if err != nil {
		return err
	}
	var triggers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		triggers = append(triggers, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range triggers {
		if _, err := tx.Exec(fmt.Sprintf("DROP TRIGGER %q", name)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DROP INDEX IF EXISTS tracks_cover_lookup;DROP INDEX IF EXISTS playlists_cover_lookup;DROP INDEX IF EXISTS audit_public_contributions;DROP INDEX IF EXISTS tags_scope_capacity;DROP TABLE IF EXISTS browse_track_search;DELETE FROM meta WHERE key='browse/search_version';"); err != nil {
		return err
	}
	searchText := func(alias string) string {
		raw := "CASE WHEN json_valid(" + alias + ".payload) THEN " + alias + ".payload ELSE '{}' END"
		return "lower(COALESCE(json_extract(" + raw + ",'$.title'),'')||' '||COALESCE(json_extract(" + raw + ",'$.artist'),'')||' '||COALESCE(json_extract(" + raw + ",'$.album'),'')||' '||COALESCE(json_extract(" + raw + ",'$.genre'),''))"
	}
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS browse_track_search(track_id TEXT PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,search_text TEXT NOT NULL);`); err != nil {
		return err
	}
	var searchVersion string
	err = tx.QueryRow("SELECT value FROM meta WHERE key='browse/search_version'").Scan(&searchVersion)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if searchVersion != "1" {
		if _, err = tx.Exec("DELETE FROM browse_track_search;INSERT INTO browse_track_search SELECT t.id," + searchText("t") + " FROM tracks t;INSERT OR REPLACE INTO meta(key,value) VALUES('browse/search_version','1')"); err != nil {
			return err
		}
	}
	for _, event := range []string{"INSERT", "UPDATE"} {
		if _, err = tx.Exec(fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS browse_search_%s AFTER %s ON tracks BEGIN INSERT INTO browse_track_search(track_id,search_text) VALUES(NEW.id,%s) ON CONFLICT(track_id) DO UPDATE SET search_text=excluded.search_text; END", event, event, searchText("NEW"))); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS tracks_cover_lookup ON tracks(json_extract(payload,'$.cover_id')) WHERE json_valid(payload);
 CREATE INDEX IF NOT EXISTS tags_scope_capacity ON tags(scope_id) WHERE merged_into IS NULL;
 CREATE INDEX IF NOT EXISTS playlists_cover_lookup ON playlists(json_extract(payload,'$.cover_id')) WHERE json_valid(payload);
	CREATE INDEX IF NOT EXISTS audit_public_contributions ON audit(user_id,created_at DESC) WHERE action='tag.create' AND json_valid(changes) AND json_extract(changes,'$.after.scope')='public';
 INSERT OR IGNORE INTO meta(key,value) VALUES('browse/epoch',lower(hex(randomblob(16)))),('browse/library','0'),('browse/admin','0'),('browse/community','0');`); err != nil {
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
			body += bump("'browse/community'")
			if _, err = tx.Exec(fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS browse_%s_%s AFTER %s ON %s BEGIN %s END", table, event, event, table, body)); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`CREATE TRIGGER IF NOT EXISTS browse_user_visibility AFTER UPDATE OF enabled,display_name,role ON users BEGIN
 UPDATE meta SET value=CAST(value AS INTEGER)+1 WHERE key IN ('browse/library','browse/admin','browse/community');
 INSERT INTO meta(key,value) VALUES('browse/account/'||NEW.id,'1') ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1;
 INSERT INTO meta(key,value) SELECT DISTINCT 'browse/account/'||s.user_id,'1' FROM subscriptions s JOIN playlists p ON p.id=s.playlist_id WHERE p.owner_id=NEW.id ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1;
 END;`); err != nil {
		return err
	}
	if err := initializeStorageProjection(tx); err != nil {
		return err
	}
	digest, err = browseIndexDigest(tx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO meta VALUES('browse/schema_version',?),('browse/schema_digest',?)", browseIndexVersion, digest); err != nil {
		return err
	}
	var invalid int
	if err := tx.QueryRow("SELECT (SELECT count(*) FROM tracks WHERE NOT json_valid(payload))+(SELECT count(*) FROM playlists WHERE NOT json_valid(payload))+(SELECT count(*) FROM audit WHERE NOT json_valid(changes))").Scan(&invalid); err != nil {
		return err
	}
	if invalid > 0 {
		log.Printf("runtime index initialization: retained %d records with invalid JSON; inspect and repair source records", invalid)
	}
	return tx.Commit()
}
