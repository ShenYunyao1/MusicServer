package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteSchemaVersion = 2

// The store retains its in-memory snapshot API. Each durable mutation replaces
// the relational snapshot in one transaction; the mutex and commitLocked keep
// failed writes from publishing a changed revision. This design is intended for
// one server process and has O(library size) writes rather than incremental SQL.
const sqliteSchema = `
CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tracks (
    id TEXT PRIMARY KEY NOT NULL,
    title TEXT NOT NULL,
    artist TEXT NOT NULL,
    album TEXT NOT NULL,
    genre TEXT NOT NULL,
    comment TEXT NOT NULL,
    lyrics TEXT NOT NULL,
    embedded_lyrics INTEGER NOT NULL DEFAULT 0 CHECK (embedded_lyrics IN (0, 1)),
    lyrics_source TEXT NOT NULL DEFAULT '',
    cover_id TEXT NOT NULL DEFAULT '',
    cover_mime TEXT NOT NULL DEFAULT '',
    cover_source TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    bitrate_kbps INTEGER NOT NULL DEFAULT 0,
    sample_rate INTEGER NOT NULL DEFAULT 0,
    channels INTEGER NOT NULL DEFAULT 0,
    bits_per_sample INTEGER NOT NULL DEFAULT 0,
    path TEXT NOT NULL,
    size INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    format TEXT NOT NULL,
    modified_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS playlists (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    updated_at TEXT NOT NULL
    ,sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS playlist_tracks (
    playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    note TEXT,
    PRIMARY KEY (playlist_id, position)
);
CREATE INDEX IF NOT EXISTS playlist_tracks_track_id ON playlist_tracks(track_id);
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    secret_hash TEXT NOT NULL,
    role TEXT NOT NULL,
    device_id TEXT NOT NULL,
    max_devices INTEGER NOT NULL,
    max_concurrency INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_used_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS key_playlist_grants (
    key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (key_id, playlist_id),
    UNIQUE (key_id, position)
);
CREATE INDEX IF NOT EXISTS key_playlist_grants_playlist_id ON key_playlist_grants(playlist_id);
CREATE TABLE IF NOT EXISTS scopes (
    key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    scope TEXT NOT NULL,
    PRIMARY KEY (key_id, position)
);
-- These records deliberately outlive tracks that disappear or are excluded.
CREATE TABLE IF NOT EXISTS metadata_overrides (
    track_id TEXT NOT NULL,
    field TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (track_id, field)
);
CREATE TABLE IF NOT EXISTS excluded_tracks (
    track_id TEXT PRIMARY KEY NOT NULL,
    excluded INTEGER NOT NULL CHECK (excluded IN (0, 1))
);
CREATE TABLE IF NOT EXISTS cover_assets (
    id TEXT PRIMARY KEY NOT NULL,
    mime TEXT NOT NULL,
    source TEXT NOT NULL,
    data BLOB NOT NULL,
    content_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS upload_staging (
    id TEXT PRIMARY KEY NOT NULL,
    original_name TEXT NOT NULL,
    size INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    temp_path TEXT NOT NULL,
    track_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS api_call_stats (
    key_id TEXT PRIMARY KEY NOT NULL,
    total_requests INTEGER NOT NULL DEFAULT 0,
    success_requests INTEGER NOT NULL DEFAULT 0,
    failure_requests INTEGER NOT NULL DEFAULT 0,
    total_bytes INTEGER NOT NULL DEFAULT 0,
    total_duration_ms INTEGER NOT NULL DEFAULT 0,
    last_called_at TEXT NOT NULL DEFAULT '',
    last_status INTEGER NOT NULL DEFAULT 0
);
`

func emptyLibrary() Library {
	return Library{
		Tracks: map[string]Track{}, Playlists: map[string]Playlist{}, Keys: map[string]APIKey{},
		Overrides: map[string]map[string]string{}, Excluded: map[string]bool{}, CoverAssets: map[string]CoverAsset{},
		Settings: ServerSettings{MaxUploadBytes: 512 << 20, ScanExternalLyrics: true, ScanExternalCovers: true},
	}
}

func normalizeLibrary(data *Library) {
	if data.Tracks == nil {
		data.Tracks = map[string]Track{}
	}
	if data.Playlists == nil {
		data.Playlists = map[string]Playlist{}
	}
	if data.Keys == nil {
		data.Keys = map[string]APIKey{}
	}
	if data.Overrides == nil {
		data.Overrides = map[string]map[string]string{}
	}
	if data.Excluded == nil {
		data.Excluded = map[string]bool{}
	}
	if data.CoverAssets == nil {
		data.CoverAssets = map[string]CoverAsset{}
	}
	if data.Settings.MaxUploadBytes <= 0 {
		data.Settings.MaxUploadBytes = 512 << 20
	}
	if data.Settings.MediaRoot == "" {
		data.Settings.MediaRoot = ""
	}
}

// DataPath returns the SQLite database path supplied to NewStore.
func (s *Store) DataPath() string {
	return s.dataPath
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) loadSQLite() error {
	if strings.TrimSpace(s.dataPath) == "" {
		return errors.New("data path is required")
	}
	if err := os.MkdirAll(filepath.Dir(s.dataPath), 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	absolutePath, err := filepath.Abs(s.dataPath)
	if err != nil {
		return fmt.Errorf("resolve data path: %w", err)
	}
	uriPath := filepath.ToSlash(absolutePath)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	query := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(5000)"}}
	dsn := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: query.Encode()}).String()
	s.db, err = sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open SQLite: %w", err)
	}
	// Keep one connection, with foreign key enforcement applied in the DSN even
	// if the driver reconnects after an error.
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
	if _, err := s.db.Exec("PRAGMA journal_mode = DELETE"); err != nil {
		return fmt.Errorf("configure SQLite: %w", err)
	}
	if err := initializeSQLiteSchema(s.db); err != nil {
		return err
	}
	var revision string
	err = s.db.QueryRow("SELECT value FROM meta WHERE key = 'revision'").Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		// A new database starts with a clean empty snapshot.
		s.data = emptyLibrary()
		s.data.Settings.MediaRoot = s.mediaRoot
		if err := s.saveLocked(); err != nil {
			return fmt.Errorf("initialize SQLite snapshot: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("read SQLite initialization: %w", err)
	}
	data, err := readSQLiteSnapshot(s.db)
	if err != nil {
		return fmt.Errorf("read SQLite snapshot: %w", err)
	}
	normalizeLibrary(&data)
	if data.Settings.MediaRoot == "" {
		data.Settings.MediaRoot = s.mediaRoot
	}
	if data.Settings.MaxUploadBytes <= 0 {
		data.Settings.MaxUploadBytes = 512 << 20
	}
	s.data = data
	return nil
}

func initializeSQLiteSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin SQLite schema: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read SQLite schema version: %w", err)
	}
	if version != 0 && version != 1 && version != sqliteSchemaVersion {
		return fmt.Errorf("unsupported SQLite schema version %d (supported: 0, 1, %d)", version, sqliteSchemaVersion)
	}
	if version == 0 {
		if _, err := tx.Exec(sqliteSchema); err != nil {
			return fmt.Errorf("create SQLite schema: %w", err)
		}
	} else if version == 1 {
		// v1 kept the relational snapshot but predated artwork, technical
		// metadata, staging, settings, ordering and call statistics.
		for _, statement := range []string{
			"ALTER TABLE tracks ADD COLUMN cover_id TEXT NOT NULL DEFAULT ''",
			"ALTER TABLE tracks ADD COLUMN cover_mime TEXT NOT NULL DEFAULT ''",
			"ALTER TABLE tracks ADD COLUMN cover_source TEXT NOT NULL DEFAULT ''",
			"ALTER TABLE tracks ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE tracks ADD COLUMN bitrate_kbps INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE tracks ADD COLUMN sample_rate INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE tracks ADD COLUMN channels INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE tracks ADD COLUMN bits_per_sample INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE playlists ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0",
			"CREATE TABLE IF NOT EXISTS cover_assets (id TEXT PRIMARY KEY NOT NULL, mime TEXT NOT NULL, source TEXT NOT NULL, data BLOB NOT NULL, content_hash TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL)",
			"CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL)",
			"CREATE TABLE IF NOT EXISTS upload_staging (id TEXT PRIMARY KEY NOT NULL, original_name TEXT NOT NULL, size INTEGER NOT NULL, content_hash TEXT NOT NULL, temp_path TEXT NOT NULL, track_json TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL)",
			"CREATE TABLE IF NOT EXISTS api_call_stats (key_id TEXT PRIMARY KEY NOT NULL, total_requests INTEGER NOT NULL DEFAULT 0, success_requests INTEGER NOT NULL DEFAULT 0, failure_requests INTEGER NOT NULL DEFAULT 0, total_bytes INTEGER NOT NULL DEFAULT 0, total_duration_ms INTEGER NOT NULL DEFAULT 0, last_called_at TEXT NOT NULL DEFAULT '', last_status INTEGER NOT NULL DEFAULT 0)",
		} {
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("migrate SQLite schema v1 to v2: %w", err)
			}
		}
	}
	if _, err := tx.Exec("INSERT OR IGNORE INTO meta (key, value) VALUES ('schema_version', ?)", strconv.Itoa(sqliteSchemaVersion)); err != nil {
		return fmt.Errorf("write SQLite schema version: %w", err)
	}
	var metadataVersion string
	if err := tx.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'").Scan(&metadataVersion); err != nil {
		return fmt.Errorf("read SQLite schema metadata: %w", err)
	}
	if metadataVersion != strconv.Itoa(sqliteSchemaVersion) {
		if version == 1 && metadataVersion == "1" {
			if _, err := tx.Exec("UPDATE meta SET value = ? WHERE key = 'schema_version'", strconv.Itoa(sqliteSchemaVersion)); err != nil {
				return fmt.Errorf("update SQLite schema metadata version: %w", err)
			}
			metadataVersion = strconv.Itoa(sqliteSchemaVersion)
		}
	}
	if metadataVersion != strconv.Itoa(sqliteSchemaVersion) {
		return fmt.Errorf("unsupported SQLite schema metadata version %q", metadataVersion)
	}
	if _, err := tx.Exec("PRAGMA user_version = 2"); err != nil {
		return fmt.Errorf("set SQLite schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema: %w", err)
	}
	return nil
}

func persistedTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parsePersistedTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func validPlaylistGrants(data Library, grants []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, id := range grants {
		if _, exists := data.Playlists[id]; exists && !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	return result
}

func dropMissingPlaylistGrants(data *Library) {
	for id, key := range data.Keys {
		key.AllowedPlaylistIDs = validPlaylistGrants(*data, key.AllowedPlaylistIDs)
		data.Keys[id] = key
	}
}

type sqliteInserter struct {
	tx         *sql.Tx
	statements map[string]*sql.Stmt
}

func (writer *sqliteInserter) insert(query string, values ...any) error {
	statement := writer.statements[query]
	if statement == nil {
		var err error
		statement, err = writer.tx.Prepare(query)
		if err != nil {
			return err
		}
		writer.statements[query] = statement
	}
	_, err := statement.Exec(values...)
	return err
}

func (writer *sqliteInserter) close() {
	for _, statement := range writer.statements {
		_ = statement.Close()
	}
}

func saveSQLiteSnapshot(db *sql.DB, data Library, committedStages ...string) error {
	if db == nil {
		return errors.New("SQLite database is not open")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin SQLite snapshot: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"scopes", "key_playlist_grants", "playlist_tracks", "tracks", "playlists", "api_keys", "metadata_overrides", "excluded_tracks", "cover_assets", "settings"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return fmt.Errorf("clear SQLite %s: %w", table, err)
		}
	}
	writer := sqliteInserter{tx: tx, statements: map[string]*sql.Stmt{}}
	defer writer.close()
	for id, track := range data.Tracks {
		if id != track.ID {
			return fmt.Errorf("track map ID %q differs from record ID %q", id, track.ID)
		}
		if err := writer.insert(`INSERT INTO tracks (id, title, artist, album, genre, comment, lyrics, embedded_lyrics, lyrics_source, cover_id, cover_mime, cover_source, duration_ms, bitrate_kbps, sample_rate, channels, bits_per_sample, path, size, content_hash, format, modified_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, track.Title, track.Artist, track.Album, track.Genre, track.Comment, track.Lyrics, track.EmbeddedLyrics, track.LyricsSource, track.CoverID, track.CoverMIME, track.CoverSource, track.DurationMS, track.BitrateKbps, track.SampleRate, track.Channels, track.BitsPerSample, track.Path, track.Size, track.ContentHash, track.Format, persistedTime(track.ModifiedAt)); err != nil {
			return fmt.Errorf("write SQLite track %q: %w", id, err)
		}
	}
	for id, asset := range data.CoverAssets {
		if id == "" || asset.ID != id || len(asset.Data) == 0 || asset.MIME == "" {
			continue
		}
		if err := writer.insert("INSERT INTO cover_assets (id, mime, source, data, content_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)", id, asset.MIME, "scan", asset.Data, id, persistedTime(time.Now().UTC())); err != nil {
			return fmt.Errorf("write SQLite cover asset %q: %w", id, err)
		}
	}
	for id, playlist := range data.Playlists {
		if id != playlist.ID {
			return fmt.Errorf("playlist map ID %q differs from record ID %q", id, playlist.ID)
		}
		if err := writer.insert("INSERT INTO playlists (id, name, description, updated_at, sort_order) VALUES (?, ?, ?, ?, ?)", id, playlist.Name, playlist.Description, persistedTime(playlist.UpdatedAt), playlist.SortOrder); err != nil {
			return fmt.Errorf("write SQLite playlist %q: %w", id, err)
		}
		for position, trackID := range playlist.TrackIDs {
			var note any
			if value, exists := playlist.TrackNotes[trackID]; exists {
				note = value
			}
			if err := writer.insert("INSERT INTO playlist_tracks (playlist_id, position, track_id, note) VALUES (?, ?, ?, ?)", id, position, trackID, note); err != nil {
				return fmt.Errorf("write SQLite playlist track %q/%q: %w", id, trackID, err)
			}
		}
	}
	for id, key := range data.Keys {
		if id != key.ID {
			return fmt.Errorf("API key map ID %q differs from record ID %q", id, key.ID)
		}
		if err := writer.insert(`INSERT INTO api_keys (id, name, secret_hash, role, device_id, max_devices, max_concurrency, created_at, expires_at, last_used_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, key.Name, key.SecretHash, key.Role, key.DeviceID, key.MaxDevices, key.MaxConcurrency, persistedTime(key.CreatedAt), persistedTime(key.ExpiresAt), persistedTime(key.LastUsedAt), persistedTime(key.RevokedAt)); err != nil {
			return fmt.Errorf("write SQLite API key %q: %w", id, err)
		}
		for position, scope := range key.Scopes {
			if err := writer.insert("INSERT INTO scopes (key_id, position, scope) VALUES (?, ?, ?)", id, position, scope); err != nil {
				return fmt.Errorf("write SQLite API key scope %q: %w", id, err)
			}
		}
		for position, playlistID := range validPlaylistGrants(data, key.AllowedPlaylistIDs) {
			if err := writer.insert("INSERT INTO key_playlist_grants (key_id, playlist_id, position) VALUES (?, ?, ?)", id, playlistID, position); err != nil {
				return fmt.Errorf("write SQLite API key grant %q: %w", id, err)
			}
		}
	}
	for trackID, fields := range data.Overrides {
		for field, value := range fields {
			if err := writer.insert("INSERT INTO metadata_overrides (track_id, field, value) VALUES (?, ?, ?)", trackID, field, value); err != nil {
				return fmt.Errorf("write SQLite metadata override %q: %w", trackID, err)
			}
		}
	}
	for trackID, excluded := range data.Excluded {
		if err := writer.insert("INSERT INTO excluded_tracks (track_id, excluded) VALUES (?, ?)", trackID, excluded); err != nil {
			return fmt.Errorf("write SQLite exclusion %q: %w", trackID, err)
		}
	}
	settings := map[string]any{
		"media_root":           data.Settings.MediaRoot,
		"ffprobe_path":         data.Settings.FFprobePath,
		"update_repository":    data.Settings.UpdateRepository,
		"max_upload_bytes":     data.Settings.MaxUploadBytes,
		"scan_external_lyrics": data.Settings.ScanExternalLyrics,
		"scan_external_covers": data.Settings.ScanExternalCovers,
	}
	for key, value := range settings {
		encoded, _ := json.Marshal(value)
		if err := writer.insert("INSERT INTO settings (key, value) VALUES (?, ?)", key, string(encoded)); err != nil {
			return fmt.Errorf("write SQLite setting %q: %w", key, err)
		}
	}
	if err := writer.insert("INSERT INTO meta (key, value) VALUES ('revision', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", strconv.FormatInt(data.Revision, 10)); err != nil {
		return fmt.Errorf("write SQLite snapshot metadata: %w", err)
	}
	for _, id := range committedStages {
		if _, err := tx.Exec("DELETE FROM upload_staging WHERE id = ?", id); err != nil {
			return fmt.Errorf("commit upload stage: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite snapshot: %w", err)
	}
	return nil
}

func querySQLiteRows(tx *sql.Tx, query string, visit func(*sql.Rows) error) error {
	rows, err := tx.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := visit(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func readSQLiteSnapshot(db *sql.DB) (Library, error) {
	data := emptyLibrary()
	tx, err := db.Begin()
	if err != nil {
		return data, err
	}
	defer tx.Rollback()
	var revision string
	if err := tx.QueryRow("SELECT value FROM meta WHERE key = 'revision'").Scan(&revision); err != nil {
		return data, err
	}
	if data.Revision, err = strconv.ParseInt(revision, 10, 64); err != nil {
		return data, fmt.Errorf("parse revision: %w", err)
	}
	if err := querySQLiteRows(tx, `SELECT id, title, artist, album, genre, comment, lyrics, embedded_lyrics, lyrics_source, cover_id, cover_mime, cover_source, duration_ms, bitrate_kbps, sample_rate, channels, bits_per_sample, path, size, content_hash, format, modified_at FROM tracks`, func(rows *sql.Rows) error {
		var track Track
		var modified string
		if err := rows.Scan(&track.ID, &track.Title, &track.Artist, &track.Album, &track.Genre, &track.Comment, &track.Lyrics, &track.EmbeddedLyrics, &track.LyricsSource, &track.CoverID, &track.CoverMIME, &track.CoverSource, &track.DurationMS, &track.BitrateKbps, &track.SampleRate, &track.Channels, &track.BitsPerSample, &track.Path, &track.Size, &track.ContentHash, &track.Format, &modified); err != nil {
			return err
		}
		var err error
		if track.ModifiedAt, err = parsePersistedTime(modified); err != nil {
			return fmt.Errorf("track %q modified time: %w", track.ID, err)
		}
		data.Tracks[track.ID] = track
		return nil
	}); err != nil {
		return data, err
	}
	if err := querySQLiteRows(tx, "SELECT id, name, description, updated_at, sort_order FROM playlists ORDER BY sort_order, id", func(rows *sql.Rows) error {
		playlist := Playlist{TrackIDs: []string{}}
		var updated string
		if err := rows.Scan(&playlist.ID, &playlist.Name, &playlist.Description, &updated, &playlist.SortOrder); err != nil {
			return err
		}
		var err error
		if playlist.UpdatedAt, err = parsePersistedTime(updated); err != nil {
			return fmt.Errorf("playlist %q updated time: %w", playlist.ID, err)
		}
		data.Playlists[playlist.ID] = playlist
		return nil
	}); err != nil {
		return data, err
	}
	if err := querySQLiteRows(tx, "SELECT playlist_id, track_id, note FROM playlist_tracks ORDER BY playlist_id, position", func(rows *sql.Rows) error {
		var playlistID, trackID string
		var note sql.NullString
		if err := rows.Scan(&playlistID, &trackID, &note); err != nil {
			return err
		}
		playlist := data.Playlists[playlistID]
		playlist.TrackIDs = append(playlist.TrackIDs, trackID)
		if note.Valid {
			if playlist.TrackNotes == nil {
				playlist.TrackNotes = map[string]string{}
			}
			playlist.TrackNotes[trackID] = note.String
		}
		data.Playlists[playlistID] = playlist
		return nil
	}); err != nil {
		return data, err
	}
	if err := querySQLiteRows(tx, `SELECT id, name, secret_hash, role, device_id, max_devices, max_concurrency, created_at, expires_at, last_used_at, revoked_at FROM api_keys`, func(rows *sql.Rows) error {
		key := APIKey{Scopes: []string{}, AllowedPlaylistIDs: []string{}}
		var created, expires, lastUsed, revoked string
		if err := rows.Scan(&key.ID, &key.Name, &key.SecretHash, &key.Role, &key.DeviceID, &key.MaxDevices, &key.MaxConcurrency, &created, &expires, &lastUsed, &revoked); err != nil {
			return err
		}
		for _, field := range []struct {
			value  string
			target *time.Time
		}{{created, &key.CreatedAt}, {expires, &key.ExpiresAt}, {lastUsed, &key.LastUsedAt}, {revoked, &key.RevokedAt}} {
			var err error
			if *field.target, err = parsePersistedTime(field.value); err != nil {
				return fmt.Errorf("API key %q time: %w", key.ID, err)
			}
		}
		data.Keys[key.ID] = key
		return nil
	}); err != nil {
		return data, err
	}
	if err := querySQLiteRows(tx, "SELECT key_id, scope FROM scopes ORDER BY key_id, position", func(rows *sql.Rows) error {
		var keyID, scope string
		if err := rows.Scan(&keyID, &scope); err != nil {
			return err
		}
		key := data.Keys[keyID]
		key.Scopes = append(key.Scopes, scope)
		data.Keys[keyID] = key
		return nil
	}); err != nil {
		return data, err
	}
	if err := querySQLiteRows(tx, "SELECT key_id, playlist_id FROM key_playlist_grants ORDER BY key_id, position", func(rows *sql.Rows) error {
		var keyID, playlistID string
		if err := rows.Scan(&keyID, &playlistID); err != nil {
			return err
		}
		key := data.Keys[keyID]
		key.AllowedPlaylistIDs = append(key.AllowedPlaylistIDs, playlistID)
		data.Keys[keyID] = key
		return nil
	}); err != nil {
		return data, err
	}
	if err := querySQLiteRows(tx, "SELECT track_id, field, value FROM metadata_overrides", func(rows *sql.Rows) error {
		var trackID, field, value string
		if err := rows.Scan(&trackID, &field, &value); err != nil {
			return err
		}
		if data.Overrides[trackID] == nil {
			data.Overrides[trackID] = map[string]string{}
		}
		data.Overrides[trackID][field] = value
		return nil
	}); err != nil {
		return data, err
	}
	if err := querySQLiteRows(tx, "SELECT track_id, excluded FROM excluded_tracks", func(rows *sql.Rows) error {
		var trackID string
		var excluded bool
		if err := rows.Scan(&trackID, &excluded); err != nil {
			return err
		}
		data.Excluded[trackID] = excluded
		return nil
	}); err != nil {
		return data, err
	}
	if err := querySQLiteRows(tx, "SELECT id, mime, data FROM cover_assets", func(rows *sql.Rows) error {
		var asset CoverAsset
		if err := rows.Scan(&asset.ID, &asset.MIME, &asset.Data); err != nil {
			return err
		}
		if data.CoverAssets == nil {
			data.CoverAssets = map[string]CoverAsset{}
		}
		data.CoverAssets[asset.ID] = asset
		return nil
	}); err != nil {
		return data, err
	}
	if err := querySQLiteRows(tx, "SELECT key, value FROM settings", func(rows *sql.Rows) error {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		switch key {
		case "media_root":
			_ = json.Unmarshal([]byte(value), &data.Settings.MediaRoot)
		case "ffprobe_path":
			_ = json.Unmarshal([]byte(value), &data.Settings.FFprobePath)
		case "update_repository":
			_ = json.Unmarshal([]byte(value), &data.Settings.UpdateRepository)
		case "max_upload_bytes":
			_ = json.Unmarshal([]byte(value), &data.Settings.MaxUploadBytes)
		case "scan_external_lyrics":
			_ = json.Unmarshal([]byte(value), &data.Settings.ScanExternalLyrics)
		case "scan_external_covers":
			_ = json.Unmarshal([]byte(value), &data.Settings.ScanExternalCovers)
		}
		return nil
	}); err != nil {
		return data, err
	}
	normalizeLibrary(&data)
	return data, tx.Commit()
}
