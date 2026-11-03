package music

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Keep a consistent authorization/data snapshot without acquiring writeMu or
// waking community streams. Callers must finish filesystem work after it ends.
func (s *Store) readSnapshot(ctx context.Context, run func(*sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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

type contextSQLReader struct {
	queryContext interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}
	ctx context.Context
}

func (r contextSQLReader) Query(query string, args ...any) (*sql.Rows, error) {
	return r.queryContext.QueryContext(r.ctx, query, args...)
}
func (r contextSQLReader) QueryRow(query string, args ...any) *sql.Row {
	return r.queryContext.QueryRowContext(r.ctx, query, args...)
}
func readContext(contexts []context.Context) context.Context {
	if len(contexts) > 0 && contexts[0] != nil {
		return contexts[0]
	}
	return context.Background()
}
func (s *Store) readQuery(ctx context.Context, run func(sqlReader) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.readSnapshot(ctx, func(tx *sql.Tx) error { return run(contextSQLReader{tx, ctx}) })
}

func (s *Store) readDatabase() *sql.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
}

func (s *Store) initializeReadPool() error {
	var mode string
	probeErr := s.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode)
	if err := walSupportError(s.dataPath, mode, probeErr); err != nil {
		return err
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

func walSupportError(path, mode string, cause error) error {
	if cause == nil && mode == "wal" {
		return nil
	}
	message := fmt.Sprintf("SQLite WAL unavailable for %q (mode %q): use a local writable filesystem with permission to create -wal/-shm files; network shares and read-only directories are unsupported", path, mode)
	if cause != nil {
		return fmt.Errorf("%s: %w", message, cause)
	}
	return errors.New(message)
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
