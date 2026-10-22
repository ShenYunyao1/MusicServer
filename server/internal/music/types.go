package music

import "time"

type Track struct {
	UploadedBy     string    `json:"uploaded_by"`
	OwnerID        string    `json:"owner_id"`
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Artist         string    `json:"artist,omitempty"`
	Album          string    `json:"album,omitempty"`
	Genre          string    `json:"genre,omitempty"`
	Comment        string    `json:"comment,omitempty"`
	Lyrics         string    `json:"lyrics,omitempty"`
	EmbeddedLyrics bool      `json:"embedded_lyrics,omitempty"`
	LyricsSource   string    `json:"lyrics_source,omitempty"`
	CoverID        string    `json:"cover_id,omitempty"`
	CoverMIME      string    `json:"cover_mime,omitempty"`
	CoverSource    string    `json:"cover_source,omitempty"`
	DurationMS     int64     `json:"duration_ms,omitempty"`
	BitrateKbps    int64     `json:"bitrate_kbps,omitempty"`
	SampleRate     int       `json:"sample_rate,omitempty"`
	Channels       int       `json:"channels,omitempty"`
	BitsPerSample  int       `json:"bits_per_sample,omitempty"`
	Path           string    `json:"path"`
	Size           int64     `json:"size"`
	ContentHash    string    `json:"content_hash"`
	Format         string    `json:"format"`
	ModifiedAt     time.Time `json:"modified_at"`
	Version        int64     `json:"version"`
	Removed        bool      `json:"removed,omitempty"`
	Missing        bool      `json:"missing,omitempty"`
}

type Playlist struct {
	UnavailableTrackIDs []string          `json:"unavailable_track_ids,omitempty"`
	OwnerID             string            `json:"owner_id,omitempty"`
	System              bool              `json:"system"`
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Description         string            `json:"description,omitempty"`
	TrackIDs            []string          `json:"track_ids"`
	UpdatedAt           time.Time         `json:"updated_at"`
	SortOrder           int               `json:"sort_order"`
	TrackNotes          map[string]string `json:"track_notes,omitempty"`
	Version             int64             `json:"version"`
}

type ServerSettings struct {
	MediaRoot          string `json:"media_root"`
	FFprobePath        string `json:"ffprobe_path,omitempty"`
	UpdateRepository   string `json:"update_repository,omitempty"`
	MaxUploadBytes     int64  `json:"max_upload_bytes"`
	ScanExternalLyrics bool   `json:"scan_external_lyrics"`
	ScanExternalCovers bool   `json:"scan_external_covers"`
}

type CoverAsset struct {
	ID   string `json:"id"`
	MIME string `json:"mime"`
	Data []byte `json:"-"`
}

type UploadPreview struct {
	ID           string    `json:"id"`
	OriginalName string    `json:"original_name"`
	Size         int64     `json:"size"`
	ContentHash  string    `json:"content_hash"`
	DuplicateOf  string    `json:"duplicate_of,omitempty"`
	Track        Track     `json:"track"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Validation   string    `json:"validation"`
}

// APIKey stores only the hash of a secret. Plaintext is returned once when a
// key is created or rotated and is never persisted.
type APIKey struct {
	UserID             string    `json:"user_id"`
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	SecretHash         string    `json:"secret_hash,omitempty"`
	Scopes             []string  `json:"scopes,omitempty"`
	AllowedPlaylistIDs []string  `json:"allowed_playlist_ids,omitempty"`
	MaxConcurrency     int       `json:"max_concurrency,omitempty"`
	MaxDownloads       int       `json:"max_downloads"`
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
	Scopes             []string  `json:"scopes,omitempty"`
	AllowedPlaylistIDs []string  `json:"allowed_playlist_ids,omitempty"`
	MaxConcurrency     int       `json:"max_concurrency,omitempty"`
	MaxDownloads       int       `json:"max_downloads,omitempty"`
	ExpiresAt          time.Time `json:"expires_at,omitempty"`
}

// APIKeyUpdateRequest contains the mutable policy and display fields of a key.
// The secret is changed only through the rotate endpoint.
type APIKeyUpdateRequest struct {
	Name               *string    `json:"name,omitempty"`
	Scopes             *[]string  `json:"scopes,omitempty"`
	AllowedPlaylistIDs *[]string  `json:"allowed_playlist_ids,omitempty"`
	MaxConcurrency     *int       `json:"max_concurrency,omitempty"`
	MaxDownloads       *int       `json:"max_downloads,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
}

type SyncResponse struct {
	Revision         int64      `json:"revision"`
	Changed          []Track    `json:"changed_tracks"`
	Playlists        []Playlist `json:"playlists"`
	FullSnapshot     bool       `json:"full_snapshot"`
	DeletedTracks    []string   `json:"deleted_tracks"`
	DeletedPlaylists []string   `json:"deleted_playlists"`
	Cursor           string     `json:"cursor"`
	HasMore          bool       `json:"has_more"`
	Reset            bool       `json:"reset"`
}

type MutationOptions struct {
	UserID          string
	CredentialID    string
	ActorType       string
	Actor           string
	ExpectedVersion int64
	ExpectedOrder   []string
	CreateOnly      bool
}

type ScanStatus struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	Full       bool      `json:"full"`
	Processed  int       `json:"processed"`
	Changed    int       `json:"changed"`
	Errors     []string  `json:"errors"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
}

type AuditEntry struct {
	ActorType    string         `json:"actor_type"`
	UserID       string         `json:"user_id,omitempty"`
	CredentialID string         `json:"credential_id,omitempty"`
	ID           int64          `json:"id"`
	Actor        string         `json:"actor"`
	Action       string         `json:"action"`
	EntityID     string         `json:"entity_id"`
	Changes      map[string]any `json:"changes"`
	CreatedAt    time.Time      `json:"created_at"`
}
