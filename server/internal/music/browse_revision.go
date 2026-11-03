package music

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
)

// A JSON-safe numeric fingerprint of data visible to this account. Stream
// metadata columns only; private changes by unrelated accounts are excluded.
func browseAccountRevision(reader sqlReader, auth authContext) (int64, error) {
	return browseAccountRevisionData(reader, auth, true)
}

func playbackRevision(account int64, id, source string, version int64) int64 {
	encoded, _ := json.Marshal([]any{account, id, source, version})
	hash := sha256.Sum256(encoded)
	return int64(binary.BigEndian.Uint64(hash[:8]) & ((1 << 53) - 1))
}
func browseAccountRevisionData(reader sqlReader, auth authContext, includeTags bool) (int64, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	if err := encoder.Encode([]string{auth.userID, auth.role}); err != nil {
		return 0, err
	}
	queries := []struct {
		query string
		args  []any
	}{
		{`SELECT key,value,'' FROM meta WHERE key IN ('browse/epoch','browse/library','browse/account/'||?,CASE WHEN ?='admin' THEN 'browse/admin' ELSE '' END) ORDER BY key`, []any{auth.userID, auth.role}},
	}
	for _, q := range queries {
		rows, err := reader.Query(q.query, q.args...)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var a, b, c string
			if err := rows.Scan(&a, &b, &c); err != nil {
				rows.Close()
				return 0, err
			}
			if err := encoder.Encode([]string{a, b, c}); err != nil {
				rows.Close()
				return 0, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
	}
	var hasTags bool
	if err := reader.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='tag_scopes' AND type='table')").Scan(&hasTags); err != nil {
		return 0, err
	}
	if hasTags && includeTags {
		rows, err := reader.Query("SELECT id,revision FROM tag_scopes WHERE id='public' OR id=? ORDER BY id", auth.userID)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var id string
			var revision int64
			if err := rows.Scan(&id, &revision); err != nil {
				rows.Close()
				return 0, err
			}
			if err := encoder.Encode([]any{id, revision}); err != nil {
				rows.Close()
				return 0, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
	}
	return int64(binary.BigEndian.Uint64(hash.Sum(nil)[:8]) & ((1 << 53) - 1)), nil
}
