package music

import "time"

type Track struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Artist         string    `json:"artist,omitempty"`
	Album          string    `json:"album,omitempty"`
	Genre          string    `json:"genre,omitempty"`
	Comment        string    `json:"comment,omitempty"`
	Lyrics         string    `json:"lyrics,omitempty"`
	EmbeddedLyrics bool      `json:"embedded_lyrics,omitempty"`
	LyricsSource   string    `json:"lyrics_source,omitempty"`
	Path           string    `json:"path"`
	Size           int64     `json:"size"`
	ContentHash    string    `json:"content_hash"`
	Format         string    `json:"format"`
	ModifiedAt     time.Time `json:"modified_at"`
}

type Playlist struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	TrackIDs    []string          `json:"track_ids"`
	UpdatedAt   time.Time         `json:"updated_at"`
	TrackNotes  map[string]string `json:"track_notes,omitempty"`
}

type Library struct {
	Revision  int64                        `json:"revision"`
	Tracks    map[string]Track             `json:"tracks"`
	Playlists map[string]Playlist          `json:"playlists"`
	Keys      map[string]APIKey            `json:"keys,omitempty"`
	Overrides map[string]map[string]string `json:"overrides,omitempty"`
	Excluded  map[string]bool              `json:"excluded,omitempty"`
}

// APIKey stores only the hash of a secret. Plaintext is returned once when a
// key is created or rotated and is never persisted.
type APIKey struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	SecretHash         string    `json:"secret_hash,omitempty"`
	Role               string    `json:"role"`
	Scopes             []string  `json:"scopes,omitempty"`
	AllowedPlaylistIDs []string  `json:"allowed_playlist_ids,omitempty"`
	DeviceID           string    `json:"device_id,omitempty"`
	MaxDevices         int       `json:"max_devices,omitempty"`
	MaxConcurrency     int       `json:"max_concurrency,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	ExpiresAt          time.Time `json:"expires_at,omitzero"`
	LastUsedAt         time.Time `json:"last_used_at,omitzero"`
	RevokedAt          time.Time `json:"revoked_at,omitzero"`
}

type APIKeyView struct {
	APIKey
	Secret string `json:"secret,omitempty"`
}

type APIKeyCreateRequest struct {
	Name               string    `json:"name"`
	Role               string    `json:"role"`
	Scopes             []string  `json:"scopes,omitempty"`
	AllowedPlaylistIDs []string  `json:"allowed_playlist_ids,omitempty"`
	DeviceID           string    `json:"device_id,omitempty"`
	MaxDevices         int       `json:"max_devices,omitempty"`
	MaxConcurrency     int       `json:"max_concurrency,omitempty"`
	ExpiresAt          time.Time `json:"expires_at,omitempty"`
}

// APIKeyUpdateRequest contains the mutable policy and display fields of a key.
// The secret is changed only through the rotate endpoint.
type APIKeyUpdateRequest struct {
	Name               *string    `json:"name,omitempty"`
	Role               *string    `json:"role,omitempty"`
	Scopes             *[]string  `json:"scopes,omitempty"`
	AllowedPlaylistIDs *[]string  `json:"allowed_playlist_ids,omitempty"`
	DeviceID           *string    `json:"device_id,omitempty"`
	MaxDevices         *int       `json:"max_devices,omitempty"`
	MaxConcurrency     *int       `json:"max_concurrency,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
}

type SyncResponse struct {
	Revision     int64      `json:"revision"`
	Changed      []Track    `json:"changed_tracks"`
	Playlists    []Playlist `json:"playlists"`
	FullSnapshot bool       `json:"full_snapshot"`
}
