package music

import (
	"context"
	"database/sql"
)

// Keep a consistent authorization/data snapshot without acquiring writeMu or
// waking community streams. Callers must finish filesystem work after it ends.
func (s *Store) readSnapshot(ctx context.Context, run func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = run(tx); err != nil {
		return err
	}
	return tx.Commit()
}
