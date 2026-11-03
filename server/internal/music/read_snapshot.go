package music

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Keep a consistent authorization/data snapshot without acquiring writeMu or
// waking community streams. Callers must finish filesystem work after it ends.
func (s *Store) readSnapshot(ctx context.Context, run func(*sql.Tx) error) error {
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = run(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) readDatabase() *sql.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
}

func (s *Store) initializeReadPool() error {
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return fmt.Errorf("SQLite WAL required for concurrent runtime readers, got %s", mode)
	}
	db, err := openSQLiteMode(s.dataPath, true)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return err
	}
	s.readDB = db
	return nil
}

func (s *Store) closeDatabases() error {
	var err error
	if s.readDB != nil {
		err = s.readDB.Close()
	}
	if s.db != nil {
		err = errors.Join(err, s.db.Close())
	}
	return err
}
