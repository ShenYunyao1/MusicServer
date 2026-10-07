package music

import (
	"crypto/sha256"
	"encoding/hex"
)

// saveCoverLocked content-addresses validated artwork in the in-memory
// snapshot. The caller must hold s.mu; the next normal snapshot commit writes
// it to SQLite's cover_assets table atomically with the track.
func (s *Store) saveCoverLocked(data []byte, mime, source string) (string, error) {
	if len(data) == 0 || mime == "" {
		return "", nil
	}
	digest := sha256.Sum256(data)
	id := hex.EncodeToString(digest[:])
	if s.data.CoverAssets == nil {
		s.data.CoverAssets = map[string]CoverAsset{}
	}
	if existing, ok := s.data.CoverAssets[id]; !ok || len(existing.Data) == 0 {
		s.data.CoverAssets[id] = CoverAsset{ID: id, MIME: mime, Data: append([]byte(nil), data...)}
	}
	return id, nil
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
