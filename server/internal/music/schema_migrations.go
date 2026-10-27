package music

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"musicserver/internal/version"
)

type sqliteMigration struct {
	from, to int
	apply    func(*sql.Tx) error
}

// initializeSQLiteSchemaForTarget exposes the planned target only to package
// code and tests. Normal server startup remains bound to version.Schema (7).
func initializeSQLiteSchemaForTarget(db *sql.DB, target int) error {
	return initializeSQLiteSchemaWithMigrations(db, target, []sqliteMigration{{from: 7, to: 8, apply: migrateSQLite7To8}})
}

func sqliteSchemaIdentity(reader sqlReader) (int, error) {
	var schema int
	if err := reader.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		return 0, err
	}
	if !version.KnownSchema(schema) {
		return schema, fmt.Errorf("unsupported SQLite schema version %d", schema)
	}
	var metadata string
	if err := reader.QueryRow("SELECT value FROM meta WHERE key='schema_version'").Scan(&metadata); err != nil {
		return schema, err
	}
	if metadata != strconv.Itoa(schema) {
		return schema, errors.New("schema metadata mismatch")
	}
	return schema, nil
}

func initializeSQLiteSchemaWithMigrations(db *sql.DB, target int, migrations []sqliteMigration) error {
	if !version.KnownSchema(target) {
		return fmt.Errorf("unsupported target SQLite schema version %d", target)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var source int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&source); err != nil {
		return err
	}
	if source == 0 {
		var tables int
		if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if tables != 0 {
			return errors.New("existing unversioned database refused; use a new database")
		}
		if _, err := tx.Exec(sqliteSchema); err != nil {
			return err
		}
		secret, err := randomToken(32)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO meta VALUES('sync_secret',?)", secret); err != nil {
			return err
		}
	}
	source, err = sqliteSchemaIdentity(tx)
	if err != nil {
		return err
	}
	if source == target {
		return tx.Commit()
	}
	if !version.CanMigrateSchema(source, target) {
		return fmt.Errorf("unsupported SQLite schema migration %d to %d", source, target)
	}
	var migration *sqliteMigration
	for i := range migrations {
		if migrations[i].from == source && migrations[i].to == target {
			if migration != nil {
				return errors.New("duplicate SQLite schema migration")
			}
			migration = &migrations[i]
		}
	}
	if migration == nil || migration.apply == nil {
		return fmt.Errorf("missing SQLite schema migration %d to %d", source, target)
	}
	if err := migration.apply(tx); err != nil {
		return fmt.Errorf("migrate SQLite schema %d to %d: %w", source, target, err)
	}
	if _, err := tx.Exec("UPDATE meta SET value=? WHERE key='schema_version'", strconv.Itoa(target)); err != nil {
		return err
	}
	if _, err := tx.Exec("PRAGMA user_version=" + strconv.Itoa(target)); err != nil {
		return err
	}
	if _, err := sqliteSchemaIdentity(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// P01 rehearses the migration ledger only. P02 must finalize the tag DDL here
// before version.Schema becomes 8; a schema 8 database is not emitted by the
// current server's normal startup or restore entry points.
func migrateSQLite7To8(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE schema_migrations (
from_version INTEGER NOT NULL,
to_version INTEGER PRIMARY KEY,
applied_at TEXT NOT NULL,
CHECK(from_version < to_version));
INSERT INTO schema_migrations VALUES(7,8,strftime('%Y-%m-%dT%H:%M:%fZ','now'));`)
	return err
}
