package music

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func saveCover(tx *sql.Tx, data []byte, mime string) error {
	if len(data) == 0 || mime == "" {
		return nil
	}
	_, err := tx.Exec("INSERT OR IGNORE INTO cover_assets VALUES(?,?,?)", hashBytes(data), mime, data)
	return err
}
func (s *Store) SetTrackCover(id string, data []byte, options ...MutationOptions) error {
	validated, mime, ok := validateCover(data, "")
	if !ok {
		return errors.New("invalid cover (maximum 8 MiB and 20 MP)")
	}
	return s.transaction(func(tx *sql.Tx) error {
		before, err := readTrackFrom(tx, id, false)
		if err != nil {
			return err
		}
		if err := checkVersion(before.Version, options); err != nil {
			return err
		}
		if err := saveCover(tx, validated, mime); err != nil {
			return err
		}
		track := before
		track.CoverID = hashBytes(validated)
		track.CoverMIME = mime
		track.CoverSource = "manual"
		track.Version++
		if err := writeTrack(tx, track); err != nil {
			return err
		}
		if err := writeOverrides(tx, id, map[string]string{"cover_id": track.CoverID, "cover_mime": mime, "cover_source": "manual"}); err != nil {
			return err
		}
		if err := recordChange(tx, "track", id, false); err != nil {
			return err
		}
		return auditMutation(tx, "track.cover", id, before, track, options)
	})
}
func (s *Store) Cover(id string) (CoverAsset, bool) {
	var asset CoverAsset
	asset.ID = id
	err := s.db.QueryRow("SELECT mime,data FROM cover_assets WHERE id=?", id).Scan(&asset.MIME, &asset.Data)
	return asset, err == nil
}
