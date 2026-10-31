package music

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) liveUploadDuplicate(reader sqlReader, hash string) (Track, bool, error) {
	tracks, err := queryJSON[Track](reader, "SELECT payload FROM tracks WHERE content_hash=? AND removed=0 AND missing=0 ORDER BY id", hash)
	if err != nil {
		return Track{}, false, err
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return Track{}, false, err
	}
	defer root.Close()
	for _, track := range tracks {
		if !filepath.IsLocal(filepath.FromSlash(track.Path)) {
			return Track{}, false, errors.New("invalid duplicate media path")
		}
		info, err := root.Lstat(filepath.FromSlash(track.Path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Track{}, false, err
		}
		if info.Mode().IsRegular() && info.Size() == track.Size {
			return track, true, nil
		}
	}
	return Track{}, false, nil
}

func generatedUploadPath(path string) bool {
	return strings.HasPrefix(path, "uploads/") && generatedStageFilename(strings.TrimPrefix(path, "uploads/"))
}

func (s *Store) recoverUploadPublications() error {
	rows, err := s.db.Query("SELECT key,value FROM meta WHERE key LIKE 'upload_publish/%'")
	if err != nil {
		return err
	}
	type intent struct{ key, path string }
	var intents []intent
	for rows.Next() {
		var item intent
		if err := rows.Scan(&item.key, &item.path); err != nil {
			rows.Close()
			return err
		}
		intents = append(intents, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, item := range intents {
		if !generatedUploadPath(item.path) {
			return errors.New("invalid pending publication path")
		}
		var exists bool
		if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM tracks WHERE path=?)", item.path).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			for _, path := range uploadOwnedPaths(item.path) {
				if err := root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
		if _, err := s.db.Exec("DELETE FROM meta WHERE key=?", item.key); err != nil {
			return err
		}
	}
	return nil
}

func uploadOwnedPaths(path string) []string {
	if !generatedUploadPath(path) {
		return []string{path}
	}
	base := strings.TrimSuffix(path, filepath.Ext(path))
	return []string{path, base + ".lrc", base + ".jpg", base + ".png", base + ".gif", base + ".webp"}
}

func cleanupDetachedAssets(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM cover_assets WHERE id NOT IN (
 SELECT json_extract(payload,'$.cover_id') FROM tracks WHERE json_extract(payload,'$.cover_id') IS NOT NULL
 UNION SELECT json_extract(payload,'$.cover_id') FROM playlists WHERE json_extract(payload,'$.cover_id') IS NOT NULL
 UNION SELECT value FROM metadata_overrides WHERE field='cover_id'
 UNION SELECT json_extract(track_json,'$.track.cover_id') FROM upload_staging WHERE json_extract(track_json,'$.track.cover_id') IS NOT NULL)`); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM meta WHERE key LIKE 'validated_audio/%' AND substr(key,17) NOT IN (SELECT content_hash FROM tracks UNION SELECT content_hash FROM upload_staging)`)
	return err
}
