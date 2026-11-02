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
		{`SELECT t.id,CAST(t.version AS TEXT),CAST(t.removed AS TEXT)||':'||CAST(t.missing AS TEXT)||':'||COALESCE(CAST(u.enabled AS TEXT),'') FROM tracks t LEFT JOIN users u ON u.id=t.owner_id ORDER BY t.id`, nil},
		{`SELECT p.id,CAST(p.version AS TEXT),CAST(p.sort_order AS TEXT)||':'||COALESCE(CAST(u.enabled AS TEXT),'')||':'||p.visibility||':'||COALESCE(CAST(s.version AS TEXT),'') FROM playlists p LEFT JOIN users u ON u.id=p.owner_id LEFT JOIN subscriptions s ON s.playlist_id=p.id AND s.user_id=? WHERE p.system=0 AND (p.owner_id=? OR s.user_id IS NOT NULL OR ?='admin') ORDER BY p.id`, []any{auth.userID, auth.userID, auth.role}},
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
