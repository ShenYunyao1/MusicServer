package music

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
)

// saveCoverLocked content-addresses validated artwork in the in-memory
// snapshot. The caller must hold s.mu; the next normal snapshot commit writes
// it to SQLite's cover_assets table atomically with the track.
func (s *Store) saveCoverLocked(data []byte, mime, source string) (string, error) {
	if len(data) == 0 || mime == "" {
		return "", nil
	}
	id := hashBytes(data)
	if s.data.CoverAssets == nil {
		s.data.CoverAssets = map[string]CoverAsset{}
	}
	if existing, ok := s.data.CoverAssets[id]; !ok || len(existing.Data) == 0 {
		s.data.CoverAssets[id] = CoverAsset{ID: id, MIME: mime, Data: append([]byte(nil), data...)}
	}
	return id, nil
}

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (s *Store) SetTrackCover(id string, data []byte) error {
	validated, mime, ok := validateCover(data, "")
	if !ok {
		return errors.New("invalid cover (maximum 8 MiB and 20 MP)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	track, exists := s.data.Tracks[id]
	if !exists {
		return os.ErrNotExist
	}
	previous := s.cloneLocked()
	track.CoverID, _ = s.saveCoverLocked(validated, mime, "manual")
	track.CoverMIME, track.CoverSource = mime, "manual"
	if s.data.Overrides[id] == nil {
		s.data.Overrides[id] = map[string]string{}
	}
	s.data.Overrides[id]["cover_id"] = track.CoverID
	s.data.Overrides[id]["cover_mime"] = mime
	s.data.Overrides[id]["cover_source"] = "manual"
	s.data.Tracks[id] = track
	s.data.Revision++
	return s.commitLocked(previous)
}

// Cover returns a copy of a stored artwork asset for HTTP handlers. Assets are
// addressed by SHA-256 ID and therefore safe to cache indefinitely.
func (s *Store) Cover(id string) (CoverAsset, bool) {
	if id == "" {
		return CoverAsset{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.data.CoverAssets[id]
	if !ok || len(asset.Data) == 0 {
		return CoverAsset{}, false
	}
	asset.Data = append([]byte(nil), asset.Data...)
	return asset, true
}
