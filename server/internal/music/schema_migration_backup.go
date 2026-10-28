package music

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"musicserver/internal/version"
)

// prepareSQLiteMigrationBackup runs before any startup migration. Even a
// manually replaced executable must leave an independently validated old-format
// snapshot; the updater's own stopped-service backup is an additional safeguard.
func (s *Store) prepareSQLiteMigrationBackup(target int) error {
	var source int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&source); err != nil {
		return err
	}
	if source == 0 || source == target {
		return nil
	}
	identity, err := sqliteSchemaIdentity(s.db)
	if err != nil {
		return err
	}
	if !version.CanMigrateSchema(identity, target) {
		return fmt.Errorf("unsupported SQLite schema migration %d to %d", identity, target)
	}
	if err := validateMigrationDatabase(s.db, source); err != nil {
		return err
	}
	// Include unused SQLite pages and the WAL, plus room for the schema change.
	var pages, pageSize int64
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		return err
	}
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return err
	}
	if pages <= 0 || pageSize <= 0 || pages > (1<<62)/pageSize {
		return errors.New("invalid SQLite database size")
	}
	size := pages * pageSize
	if wal, err := os.Stat(s.dataPath + "-wal"); err == nil {
		size += wal.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if size < 0 || size > 1<<62 {
		return errors.New("SQLite database size exceeds migration backup limit")
	}
	if err := s.checkSpace(filepath.Dir(s.dataPath), size+(16<<20)); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(filepath.Dir(s.dataPath), fmt.Sprintf(".schema-%d-to-%d-", source, target))
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(dir)
		}
	}()
	snapshot, err := filepath.Abs(filepath.Join(dir, "snapshot.sqlite"))
	if err != nil {
		return err
	}
	if _, err = s.db.Exec("VACUUM INTO ?", snapshot); err != nil {
		return fmt.Errorf("create pre-migration snapshot: %w", err)
	}
	if err = os.Chmod(snapshot, 0600); err != nil {
		return err
	}
	backup, err := openSQLite(snapshot)
	if err != nil {
		return err
	}
	checkErr := validateMigrationDatabase(backup, source)
	closeErr := backup.Close()
	if checkErr != nil {
		return checkErr
	}
	if closeErr != nil {
		return closeErr
	}
	file, err := os.OpenFile(snapshot, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr = file.Close()
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Directory fsync is supported on Linux; Windows FlushFileBuffers rejects
	// directory handles, while the database file itself has still been flushed.
	if runtime.GOOS != "windows" {
		for _, path := range []string{dir, filepath.Dir(dir)} {
			d, err := os.Open(path)
			if err != nil {
				return err
			}
			syncErr = d.Sync()
			closeErr = d.Close()
			if syncErr != nil {
				return syncErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	complete = true
	return nil
}

func validateMigrationDatabase(db *sql.DB, expected int) error {
	schema, err := sqliteSchemaIdentity(db)
	if err != nil {
		return err
	}
	if schema != expected {
		return errors.New("migration snapshot schema mismatch")
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return errors.New("migration database integrity check failed")
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	rowErr := rows.Err()
	closeErr := rows.Close()
	if rowErr != nil {
		return rowErr
	}
	if closeErr != nil {
		return closeErr
	}
	if invalid {
		return errors.New("migration database has invalid references")
	}
	return nil
}
