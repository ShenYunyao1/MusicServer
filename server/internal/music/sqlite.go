package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
	"musicserver/internal/version"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const sqliteSchemaVersion = version.Schema
const sqliteSchema = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO meta VALUES ('revision','0'),('schema_version','7'),('public_revision','0');
CREATE TABLE users (id TEXT PRIMARY KEY,username TEXT NOT NULL UNIQUE,display_name TEXT NOT NULL,password_hash TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('admin','member')),enabled INTEGER NOT NULL,auth_version INTEGER NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,must_change_password INTEGER NOT NULL DEFAULT 0);
CREATE TABLE sessions (id TEXT PRIMARY KEY,token_hash TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL REFERENCES users(id),auth_version INTEGER NOT NULL,created_at TEXT NOT NULL,last_active_at TEXT NOT NULL,expires_at TEXT NOT NULL,revoked_at TEXT NOT NULL DEFAULT '');
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE login_limits (bucket TEXT PRIMARY KEY,attempts INTEGER NOT NULL,window_start TEXT NOT NULL);
CREATE TABLE tracks (id TEXT PRIMARY KEY CHECK(length(id)=36),path TEXT NOT NULL UNIQUE,content_hash TEXT NOT NULL,title TEXT NOT NULL,payload TEXT NOT NULL,version INTEGER NOT NULL,removed INTEGER NOT NULL DEFAULT 0,missing INTEGER NOT NULL DEFAULT 0,owner_id TEXT NOT NULL REFERENCES users(id),uploaded_by TEXT NOT NULL REFERENCES users(id));
CREATE INDEX tracks_hash ON tracks(content_hash);
CREATE INDEX tracks_owner ON tracks(owner_id,id);
CREATE INDEX tracks_uploader ON tracks(uploaded_by,id);
CREATE TABLE scan_stamps (track_id TEXT PRIMARY KEY REFERENCES tracks(id), stamp TEXT NOT NULL);
CREATE TABLE metadata_overrides (track_id TEXT NOT NULL REFERENCES tracks(id),field TEXT NOT NULL,value TEXT NOT NULL,PRIMARY KEY(track_id,field));
CREATE TABLE playlists (id TEXT PRIMARY KEY,payload TEXT NOT NULL,version INTEGER NOT NULL,owner_id TEXT REFERENCES users(id),system INTEGER NOT NULL DEFAULT 0,sort_order INTEGER NOT NULL DEFAULT 0,visibility TEXT NOT NULL DEFAULT 'private' CHECK(visibility IN ('private','public','shared')),published_at TEXT NOT NULL DEFAULT '',CHECK((system=1 AND owner_id IS NULL AND id='library') OR (system=0 AND owner_id IS NOT NULL AND id!='library')));
CREATE INDEX playlists_owner_order ON playlists(owner_id,sort_order,id);
CREATE INDEX playlists_public ON playlists(visibility,published_at,id);
CREATE TABLE playlist_tombstones (id TEXT PRIMARY KEY);
CREATE TABLE subscriptions (user_id TEXT NOT NULL REFERENCES users(id),playlist_id TEXT NOT NULL,last_name TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,PRIMARY KEY(user_id,playlist_id));
CREATE INDEX subscriptions_source ON subscriptions(playlist_id,user_id);
CREATE TABLE playlist_invitations (id TEXT PRIMARY KEY,playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,inviter_id TEXT NOT NULL REFERENCES users(id),recipient_id TEXT NOT NULL REFERENCES users(id),state TEXT NOT NULL CHECK(state IN ('pending','accepted','declined','revoked')),version INTEGER NOT NULL,last_name TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(playlist_id,recipient_id),CHECK(inviter_id!=recipient_id));
CREATE INDEX invitations_recipient ON playlist_invitations(recipient_id,updated_at DESC,id);
CREATE TABLE subscription_notes (user_id TEXT NOT NULL,playlist_id TEXT NOT NULL,track_id TEXT NOT NULL REFERENCES tracks(id),note TEXT NOT NULL,PRIMARY KEY(user_id,playlist_id,track_id),FOREIGN KEY(user_id,playlist_id) REFERENCES subscriptions(user_id,playlist_id) ON DELETE CASCADE);
CREATE TABLE playlist_tracks (playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,track_id TEXT NOT NULL REFERENCES tracks(id),position INTEGER NOT NULL,note TEXT NOT NULL DEFAULT '',PRIMARY KEY(playlist_id,track_id),UNIQUE(playlist_id,position));
CREATE INDEX playlist_member ON playlist_tracks(track_id);
CREATE TABLE api_keys (id TEXT PRIMARY KEY,secret_hash TEXT NOT NULL UNIQUE,payload TEXT NOT NULL,user_id TEXT NOT NULL REFERENCES users(id));
CREATE TABLE key_playlist_grants (key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,playlist_id TEXT NOT NULL,position INTEGER NOT NULL,PRIMARY KEY(key_id,playlist_id));
CREATE TABLE scopes (key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,position INTEGER NOT NULL,scope TEXT NOT NULL,PRIMARY KEY(key_id,position));
CREATE TABLE cover_assets (id TEXT PRIMARY KEY,mime TEXT NOT NULL,data BLOB NOT NULL);
CREATE TABLE settings (key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE upload_staging (id TEXT PRIMARY KEY,original_name TEXT NOT NULL,size INTEGER NOT NULL,content_hash TEXT NOT NULL,temp_path TEXT NOT NULL,track_json TEXT NOT NULL,created_at TEXT NOT NULL,expires_at TEXT NOT NULL,user_id TEXT NOT NULL REFERENCES users(id));
CREATE TABLE api_call_stats (key_id TEXT PRIMARY KEY,total_requests INTEGER NOT NULL DEFAULT 0,success_requests INTEGER NOT NULL DEFAULT 0,failure_requests INTEGER NOT NULL DEFAULT 0,total_bytes INTEGER NOT NULL DEFAULT 0,total_duration_ms INTEGER NOT NULL DEFAULT 0,last_called_at TEXT NOT NULL DEFAULT '',last_status INTEGER NOT NULL DEFAULT 0);
CREATE TABLE changes (revision INTEGER PRIMARY KEY,kind TEXT NOT NULL,entity_id TEXT NOT NULL,deleted INTEGER NOT NULL);
CREATE TABLE audit (id INTEGER PRIMARY KEY AUTOINCREMENT,actor TEXT NOT NULL,action TEXT NOT NULL,entity_id TEXT NOT NULL,changes TEXT NOT NULL,created_at TEXT NOT NULL,actor_type TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',credential_id TEXT NOT NULL DEFAULT '');
PRAGMA user_version=7;
`

type sqlReader interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func (s *Store) DataPath() string { return s.dataPath }
func (s *Store) Close() error {
	s.scanMu.Lock()
	s.closed = true
	if s.scanCancel != nil {
		s.scanCancel()
	}
	s.scanMu.Unlock()
	s.CloseMail()
	s.CancelBackup()
	s.workers.Wait()
	s.changeMu.Lock()
	s.changeClosed = true
	if s.changeSignal != nil {
		close(s.changeSignal)
		s.changeSignal = nil
	}
	s.changeMu.Unlock()
	return s.db.Close()
}
func openSQLite(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := filepath.ToSlash(absolute)
	if !strings.HasPrefix(uri, "/") {
		uri = "/" + uri
	}
	query := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(5000)"}}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: uri, RawQuery: query.Encode()}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}
func (s *Store) loadSQLite() error {
	if strings.TrimSpace(s.dataPath) == "" {
		return errors.New("data path is required")
	}
	if err := os.MkdirAll(filepath.Dir(s.dataPath), 0755); err != nil {
		return err
	}
	db, err := openSQLite(s.dataPath)
	if err != nil {
		return err
	}
	s.db = db
	if err := initializeSQLiteSchema(db); err != nil {
		return err
	}
	if err := os.Chmod(s.dataPath, 0600); err != nil {
		return err
	}
	var raw string
	err = db.QueryRow("SELECT value FROM settings WHERE key='server'").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		settings := ServerSettings{MediaRoot: s.mediaRoot, MaxUploadBytes: 512 << 20, ScanExternalLyrics: true, ScanExternalCovers: true}
		encoded, _ := json.Marshal(settings)
		_, err = db.Exec("INSERT INTO settings VALUES('server',?)", string(encoded))
	}
	return err
}
func initializeSQLiteSchema(db *sql.DB) error {
	return initializeSQLiteSchemaForTarget(db, sqliteSchemaVersion)
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
func readTrackFrom(reader sqlReader, id string, includeRemoved bool) (Track, error) {
	var raw string
	query := "SELECT payload FROM tracks WHERE id=?"
	if !includeRemoved {
		query += " AND removed=0"
	}
	err := reader.QueryRow(query, id).Scan(&raw)
	var track Track
	if err == nil {
		err = json.Unmarshal([]byte(raw), &track)
	}
	return track, err
}
func readPlaylistFrom(reader sqlReader, id string) (Playlist, error) {
	var raw string
	var playlist Playlist
	err := reader.QueryRow("SELECT payload FROM playlists WHERE id=?", id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &playlist)
	}
	return playlist, err
}
func queryJSON[Item any](reader sqlReader, query string, args ...any) ([]Item, error) {
	items := []Item{}
	rows, err := reader.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item Item
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func readOverrides(reader sqlReader, id string) (map[string]string, error) {
	result := map[string]string{}
	rows, err := reader.Query("SELECT field,value FROM metadata_overrides WHERE track_id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var field, value string
		if err := rows.Scan(&field, &value); err != nil {
			return nil, err
		}
		result[field] = value
	}
	return result, rows.Err()
}
func writeOverrides(tx *sql.Tx, id string, fields map[string]string) error {
	for field, value := range fields {
		if _, err := tx.Exec("INSERT INTO metadata_overrides VALUES(?,?,?) ON CONFLICT(track_id,field) DO UPDATE SET value=excluded.value", id, field, value); err != nil {
			return err
		}
	}
	return nil
}
func writeTrack(tx *sql.Tx, track Track) error {
	if _, err := uuid.Parse(track.ID); err != nil {
		return errors.New("track ID must be a UUID")
	}
	encoded, err := json.Marshal(track)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO tracks(id,path,content_hash,title,payload,version,removed,missing,owner_id,uploaded_by) VALUES(?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET path=excluded.path,content_hash=excluded.content_hash,title=excluded.title,payload=excluded.payload,version=excluded.version,removed=excluded.removed,missing=excluded.missing,owner_id=excluded.owner_id,uploaded_by=excluded.uploaded_by`, track.ID, track.Path, track.ContentHash, track.Title, string(encoded), track.Version, track.Removed, track.Missing, track.OwnerID, track.UploadedBy)
	return err
}
func writePlaylist(tx *sql.Tx, playlist Playlist) error {
	before, err := readPlaylistFrom(tx, playlist.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if playlist.Visibility == "" {
		playlist.Visibility = "private"
	}
	playlist.ReadOnly = false
	playlist.SubscriptionStatus = ""
	playlist.SubscriptionReason = ""
	playlist.SubscriptionVersion = 0
	playlist.SourceOwnerName = ""
	encoded, err := json.Marshal(playlist)
	if err != nil {
		return err
	}
	var owner any
	if playlist.OwnerID != "" {
		owner = playlist.OwnerID
	}
	if _, err = tx.Exec("INSERT INTO playlists(id,payload,version,owner_id,system,sort_order,visibility,published_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,version=excluded.version,owner_id=excluded.owner_id,sort_order=excluded.sort_order,visibility=excluded.visibility,published_at=excluded.published_at", playlist.ID, string(encoded), playlist.Version, owner, playlist.System, playlist.SortOrder, playlist.Visibility, persistedTime(playlist.PublishedAt)); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM playlist_tracks WHERE playlist_id=?", playlist.ID); err != nil {
		return err
	}
	for position, id := range playlist.TrackIDs {
		if _, err = tx.Exec("INSERT INTO playlist_tracks VALUES(?,?,?,?)", playlist.ID, id, position, playlist.TrackNotes[id]); err != nil {
			return err
		}
	}
	if publicSummaryChanged(before, playlist) {
		return bumpPublicCatalog(tx)
	}
	return nil
}
func checkVersion(current int64, options []MutationOptions) error {
	if len(options) > 0 && options[0].ExpectedVersion > 0 && options[0].ExpectedVersion != current {
		return errVersionConflict
	}
	return nil
}
func auditMutation(tx *sql.Tx, action, id string, before, after any, options []MutationOptions) error {
	actor := "system"
	if len(options) > 0 && options[0].Actor != "" {
		actor = options[0].Actor
	}
	encoded, err := json.Marshal(map[string]any{"before": before, "after": after})
	if err != nil {
		return err
	}
	identity := MutationOptions{ActorType: "system"}
	if len(options) > 0 {
		identity = options[0]
		if identity.ActorType == "" {
			identity.ActorType = "system"
		}
	}
	_, err = tx.Exec("INSERT INTO audit(actor,action,entity_id,changes,created_at,actor_type,user_id,credential_id) VALUES(?,?,?,?,?,?,?,?)", actor, action, id, string(encoded), persistedTime(time.Now()), identity.ActorType, identity.UserID, identity.CredentialID)
	return err
}
func recordChange(tx *sql.Tx, kind, id string, deleted bool) error {
	var revision int64
	if err := tx.QueryRow("UPDATE meta SET value=CAST(value AS INTEGER)+1 WHERE key='revision' RETURNING value").Scan(&revision); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO changes VALUES(?,?,?,?)", revision, kind, id, deleted); err != nil {
		return err
	}
	_, err := tx.Exec("DELETE FROM changes WHERE revision<=?", revision-10000)
	return err
}
func (s *Store) transaction(run func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = run(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyCommunityCommit()
	return nil
}
func (s *Store) Revision() int64 {
	var value string
	if err := s.db.QueryRow("SELECT value FROM meta WHERE key='revision'").Scan(&value); err != nil {
		return 0
	}
	result, _ := strconv.ParseInt(value, 10, 64)
	return result
}
