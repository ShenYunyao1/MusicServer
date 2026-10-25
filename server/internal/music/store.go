package music

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"musicserver/internal/activity"
	"musicserver/internal/diskspace"
	"musicserver/internal/update"
)

var audioExtensions = map[string]bool{".aac": true, ".flac": true, ".m4a": true, ".mp3": true, ".oga": true, ".ogg": true, ".opus": true, ".wav": true, ".wma": true}
var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var errPlaylistNotFound = errors.New("playlist not found")
var errMemberNotFound = errors.New("playlist member not found")
var errLibraryMembers = errors.New("the library playlist manages its own tracks")
var errPlaylistConflict = errors.New("playlist members changed; reload before reordering")
var errVersionConflict = errors.New("record changed; refresh before editing")
var errTrackInUse = errors.New("this track is used by another account's playlist; contact an administrator")

type Store struct {
	storageWriteMu sync.Mutex
	spaceProbe     func(string) (diskspace.Space, error)
	backupBundleMu sync.Mutex
	backupMu       sync.Mutex
	backupJob      BackupJob
	backupCancel   context.CancelFunc
	backupReaders  map[string]int
	changeMu       sync.Mutex
	changeSignal   chan struct{}
	changeStamp    string
	changeWatchers int
	changeClosed   bool
	activity       *activity.Journal
	stageSlots     chan struct{}
	activeStages   map[string]bool
	dataPath       string
	mediaRoot      string
	db             *sql.DB
	writeMu        sync.Mutex
	stageMu        sync.Mutex
	mediaMu        sync.RWMutex
	scanMu         sync.Mutex
	scanCancel     context.CancelFunc
	scanStatus     ScanStatus
	workers        sync.WaitGroup
	closed         bool
}

func NewStore(dataPath, mediaRoot string) (*Store, error) {
	if strings.EqualFold(filepath.Ext(dataPath), ".json") {
		return nil, errors.New("JSON data paths are unsupported; use a new SQLite database")
	}
	root, err := filepath.Abs(mediaRoot)
	if err != nil {
		return nil, err
	}
	store := &Store{dataPath: dataPath, mediaRoot: filepath.Clean(root), stageSlots: make(chan struct{}, 2), activeStages: map[string]bool{}}
	if err := store.loadSQLite(); err != nil {
		if store.db != nil {
			_ = store.db.Close()
		}
		return nil, err
	}
	if err := store.InitializeResumableUploads(); err != nil {
		_ = store.db.Close()
		return nil, err
	}
	return store, nil
}
func randomToken(count int) (string, error) {
	value := make([]byte, count)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
func hashSecret(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}
func cloneStrings(values []string) []string { return append([]string{}, values...) }
func cloneAPIKey(value APIKey) APIKey {
	value.Scopes = cloneStrings(value.Scopes)
	value.AllowedPlaylistIDs = cloneStrings(value.AllowedPlaylistIDs)
	return value
}
func publicAPIKey(value APIKey) APIKey {
	value = cloneAPIKey(value)
	value.SecretHash = ""
	return value
}

func validateKeyGrants(tx *sql.Tx, key APIKey) error {
	user, err := readUser(tx, key.UserID)
	if err != nil {
		return err
	}
	for _, id := range key.AllowedPlaylistIDs {
		playlist, err := readPlaylistFrom(tx, id)
		if err == nil && (playlist.System || playlist.OwnerID == user.ID) {
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if hasSubscription(tx, user.ID, id) {
			if err == nil && externalPlaylistAccess(tx, user.ID, playlist) {
				continue
			}
			var configured bool
			if e := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM key_playlist_grants WHERE key_id=? AND playlist_id=?)", key.ID, id).Scan(&configured); e != nil {
				return e
			}
			if configured {
				continue
			}
		}
		return errors.New("key owner cannot access requested playlist; subscribe first")
	}
	return nil
}

func saveKey(tx *sql.Tx, key APIKey) error {
	encoded, err := json.Marshal(key)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO api_keys VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET secret_hash=excluded.secret_hash,payload=excluded.payload", key.ID, key.SecretHash, string(encoded), key.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM key_playlist_grants WHERE key_id=?", key.ID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for position, id := range key.AllowedPlaylistIDs {
		if seen[id] {
			return errors.New("duplicate playlist grant")
		}
		seen[id] = true
		if _, err = tx.Exec("INSERT INTO key_playlist_grants VALUES(?,?,?)", key.ID, id, position); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM scopes WHERE key_id=?", key.ID); err != nil {
		return err
	}
	for position, scope := range key.Scopes {
		if _, err = tx.Exec("INSERT INTO scopes VALUES(?,?,?)", key.ID, position, scope); err != nil {
			return err
		}
	}
	if !key.RevokedAt.IsZero() {
		_, err = tx.Exec("DELETE FROM api_call_stats WHERE key_id=?", key.ID)
	} else {
		_, err = tx.Exec("INSERT INTO api_call_stats(key_id) VALUES(?) ON CONFLICT(key_id) DO NOTHING", key.ID)
	}
	return err
}
func (s *Store) CreateAPIKey(request APIKeyCreateRequest, options ...MutationOptions) (APIKeyView, error) {
	if strings.TrimSpace(request.Name) == "" {
		return APIKeyView{}, errors.New("key name is required")
	}
	secret, err := randomToken(32)
	if err != nil {
		return APIKeyView{}, err
	}
	scopes := request.Scopes
	if scopes == nil {
		scopes = defaultScopes()
	}
	if err := validatePlayerScopes(scopes); err != nil {
		return APIKeyView{}, err
	}
	key := APIKey{ID: uuid.NewString(), Name: strings.TrimSpace(request.Name), SecretHash: hashSecret(secret), Scopes: cloneStrings(scopes), AllowedPlaylistIDs: cloneStrings(request.AllowedPlaylistIDs), MaxConcurrency: request.MaxConcurrency, MaxDownloads: request.MaxDownloads, CreatedAt: time.Now().UTC(), ExpiresAt: request.ExpiresAt}
	if key.MaxConcurrency < 1 {
		key.MaxConcurrency = 4
	}
	if key.MaxDownloads < 1 {
		key.MaxDownloads = 1
	}
	if key.MaxConcurrency > 64 || key.MaxDownloads > 16 {
		return APIKeyView{}, errors.New("concurrency limits exceeded")
	}
	err = s.transaction(func(tx *sql.Tx) error {
		owner, err := internalOwner(tx, options)
		if err != nil {
			return err
		}
		if err := checkActorOwner(tx, owner, options); err != nil {
			return err
		}
		key.UserID = owner
		if err := validateKeyGrants(tx, key); err != nil {
			return err
		}
		if err := saveKey(tx, key); err != nil {
			return err
		}
		return auditMutation(tx, "key.create", key.ID, nil, publicAPIKey(key), options)
	})
	return APIKeyView{APIKey: key, Secret: secret}, err
}
func (s *Store) APIKeys() []APIKey {
	keys, _ := queryJSON[APIKey](s.db, "SELECT payload FROM api_keys ORDER BY id")
	for index := range keys {
		var last string
		if s.db.QueryRow("SELECT last_called_at FROM api_call_stats WHERE key_id=?", keys[index].ID).Scan(&last) == nil {
			keys[index].LastUsedAt, _ = parsePersistedTime(last)
		}
	}
	return keys
}
func (s *Store) AuthenticateSecret(secret string) (APIKey, bool) {
	if secret == "" {
		return APIKey{}, false
	}
	var raw string
	if err := s.db.QueryRow("SELECT payload FROM api_keys WHERE secret_hash=?", hashSecret(secret)).Scan(&raw); err != nil {
		return APIKey{}, false
	}
	var key APIKey
	if json.Unmarshal([]byte(raw), &key) != nil || !key.RevokedAt.IsZero() || (!key.ExpiresAt.IsZero() && !time.Now().Before(key.ExpiresAt)) {
		return APIKey{}, false
	}
	return key, true
}
func (s *Store) mutateKey(id, action string, edit func(*APIKey) error, options []MutationOptions) (APIKey, error) {
	var result APIKey
	err := s.transaction(func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRow("SELECT payload FROM api_keys WHERE id=?", id).Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return err
		}
		if len(options) > 0 && options[0].UserID != "" && options[0].UserID != result.UserID {
			return errResourceHidden
		}
		if err := checkActorOwner(tx, result.UserID, options); err != nil {
			return err
		}
		before := publicAPIKey(result)
		if err := edit(&result); err != nil {
			return err
		}
		if action == "key.update" {
			if err := validateKeyGrants(tx, result); err != nil {
				return err
			}
		}
		if err := saveKey(tx, result); err != nil {
			return err
		}
		return auditMutation(tx, action, id, before, publicAPIKey(result), options)
	})
	return result, err
}
func (s *Store) RotateAPIKey(id string, options ...MutationOptions) (APIKeyView, error) {
	secret, err := randomToken(32)
	if err != nil {
		return APIKeyView{}, err
	}
	key, err := s.mutateKey(id, "key.rotate", func(key *APIKey) error { key.SecretHash = hashSecret(secret); key.RevokedAt = time.Time{}; return nil }, options)
	return APIKeyView{APIKey: key, Secret: secret}, err
}
func (s *Store) UpdateAPIKey(id string, request APIKeyUpdateRequest, options ...MutationOptions) (APIKey, error) {
	return s.mutateKey(id, "key.update", func(key *APIKey) error {
		if request.Name != nil {
			if strings.TrimSpace(*request.Name) == "" {
				return errors.New("key name is required")
			}
			key.Name = strings.TrimSpace(*request.Name)
		}
		if request.Scopes != nil {
			if err := validatePlayerScopes(*request.Scopes); err != nil {
				return err
			}
			key.Scopes = cloneStrings(*request.Scopes)
		}
		if request.AllowedPlaylistIDs != nil {
			key.AllowedPlaylistIDs = cloneStrings(*request.AllowedPlaylistIDs)
		}
		if request.MaxConcurrency != nil {
			if *request.MaxConcurrency < 1 || *request.MaxConcurrency > 64 {
				return errors.New("max_concurrency must be 1..64")
			}
			key.MaxConcurrency = *request.MaxConcurrency
		}
		if request.MaxDownloads != nil {
			if *request.MaxDownloads < 1 || *request.MaxDownloads > 16 {
				return errors.New("max_downloads must be 1..16")
			}
			key.MaxDownloads = *request.MaxDownloads
		}
		if request.ExpiresAt != nil {
			key.ExpiresAt = request.ExpiresAt.UTC()
		}
		return nil
	}, options)
}

func (s *Store) Tracks() []Track {
	tracks, _ := queryJSON[Track](s.db, "SELECT payload FROM tracks WHERE removed=0 ORDER BY title,id")
	return tracks
}
func (s *Store) RemovedTracks() []Track {
	tracks, _ := queryJSON[Track](s.db, "SELECT payload FROM tracks WHERE removed=1 ORDER BY title,id")
	return tracks
}
func (s *Store) Track(id string) (Track, bool) {
	track, err := readTrackFrom(s.db, id, false)
	return track, err == nil
}
func (s *Store) allTracks() ([]Track, error) {
	return queryJSON[Track](s.db, "SELECT payload FROM tracks ORDER BY id")
}
func (s *Store) Playlists() []Playlist {
	playlists, _ := queryJSON[Playlist](s.db, "SELECT payload FROM playlists ORDER BY id")
	sort.Slice(playlists, func(first, second int) bool {
		if playlists[first].SortOrder != playlists[second].SortOrder {
			return playlists[first].SortOrder < playlists[second].SortOrder
		}
		if playlists[first].Name != playlists[second].Name {
			return playlists[first].Name < playlists[second].Name
		}
		return playlists[first].ID < playlists[second].ID
	})
	return playlists
}
func (s *Store) Playlist(id string) (Playlist, bool) {
	playlist, err := readPlaylistFrom(s.db, id)
	return playlist, err == nil
}
func clonePlaylist(value Playlist) Playlist {
	value.TrackIDs = cloneStrings(value.TrackIDs)
	notes := map[string]string{}
	for id, note := range value.TrackNotes {
		notes[id] = note
	}
	value.TrackNotes = notes
	return value
}
func (s *Store) PutPlaylist(playlist Playlist, options ...MutationOptions) error {
	if !validID.MatchString(playlist.ID) || strings.TrimSpace(playlist.Name) == "" {
		return errors.New("playlist id and name are required")
	}
	return s.transaction(func(tx *sql.Tx) error {
		before, err := readPlaylistFrom(tx, playlist.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if before.ID != "" {
			if err := checkActorOwner(tx, before.OwnerID, options); err != nil {
				return err
			}
		}
		if before.ID != "" && len(options) > 0 && options[0].CreateOnly {
			return errVersionConflict
		}
		if err := checkVersion(before.Version, options); err != nil {
			return err
		}
		if playlist.ID == "library" {
			return errLibraryMembers
		}
		playlist.System = false
		members := map[string]bool{}
		for _, id := range playlist.TrackIDs {
			if members[id] {
				return errors.New("duplicate playlist track")
			}
			members[id] = true
			if _, err := readTrackFrom(tx, id, false); err != nil {
				return fmt.Errorf("unknown track %s", id)
			}
		}
		for id := range playlist.TrackNotes {
			if !members[id] {
				return errors.New("track note references non-member track")
			}
		}
		if before.ID != "" {
			playlist.OwnerID = before.OwnerID
			playlist.SortOrder = before.SortOrder
			playlist.CoverID = before.CoverID
			playlist.CoverMIME = before.CoverMIME
			playlist.Visibility = before.Visibility
			playlist.PublishedAt = before.PublishedAt
		} else {
			var retired bool
			if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM playlist_tombstones WHERE id=?)", playlist.ID).Scan(&retired); err != nil {
				return err
			}
			if retired {
				return errors.New("deleted playlist ID cannot be reused")
			}
			playlist.Visibility = "private"
			playlist.PublishedAt = time.Time{}
			playlist.CoverID, playlist.CoverMIME = "", ""
			owner, err := internalOwner(tx, options)
			if err != nil {
				return err
			}
			if err := checkActorOwner(tx, owner, options); err != nil {
				return err
			}
			playlist.OwnerID = owner
			var count int
			if err := tx.QueryRow("SELECT COALESCE(MAX(sort_order),0) FROM playlists WHERE owner_id=? AND system=0", owner).Scan(&count); err != nil {
				return err
			}
			playlist.SortOrder = count + 1
		}
		playlist.Version = before.Version + 1
		playlist.UpdatedAt = time.Now().UTC()
		playlist.TrackIDs = cloneStrings(playlist.TrackIDs)
		if err := writePlaylist(tx, playlist); err != nil {
			return err
		}
		if err := recordChange(tx, "playlist", playlist.ID, false); err != nil {
			return err
		}
		return auditMutation(tx, "playlist.put", playlist.ID, before, playlist, options)
	})
}
func (s *Store) mutatePlaylist(id string, members bool, edit func(*Playlist) error, options ...MutationOptions) (Playlist, error) {
	return s.mutatePlaylistAction(id, members, "playlist.update", edit, options...)
}
func (s *Store) mutatePlaylistAction(id string, members bool, action string, edit func(*Playlist) error, options ...MutationOptions) (Playlist, error) {
	var result Playlist
	err := s.transaction(func(tx *sql.Tx) error {
		before, err := readPlaylistFrom(tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return errPlaylistNotFound
		}
		if err != nil {
			return err
		}
		if members && id == "library" {
			return errLibraryMembers
		}
		if before.ID != "" {
			if err := checkActorOwner(tx, before.OwnerID, options); err != nil {
				return err
			}
		}
		if err := checkVersion(before.Version, options); err != nil {
			return err
		}
		result = clonePlaylist(before)
		if err := edit(&result); err != nil {
			return err
		}
		for _, trackID := range result.TrackIDs {
			track, err := readTrackFrom(tx, trackID, true)
			if err != nil || track.Removed && !slices.Contains(before.TrackIDs, trackID) {
				return fmt.Errorf("unknown track: %s", trackID)
			}
		}
		if reflect.DeepEqual(before, result) {
			return nil
		}
		result.Version++
		result.UpdatedAt = time.Now().UTC()
		if err := writePlaylist(tx, result); err != nil {
			return err
		}
		if err := recordChange(tx, "playlist", id, false); err != nil {
			return err
		}
		return auditMutation(tx, action, id, before, result, options)
	})
	return result, err
}
func (s *Store) UpdatePlaylistMetadata(id string, name, description *string, options ...MutationOptions) (Playlist, error) {
	return s.mutatePlaylist(id, false, func(playlist *Playlist) error {
		if name != nil {
			if strings.TrimSpace(*name) == "" {
				return errors.New("playlist name is required")
			}
			playlist.Name = *name
		}
		if description != nil {
			playlist.Description = *description
		}
		return nil
	}, options...)
}
func (s *Store) AddPlaylistTracks(id string, ids []string, options ...MutationOptions) (Playlist, error) {
	return s.mutatePlaylist(id, true, func(playlist *Playlist) error {
		seen := map[string]bool{}
		for _, member := range playlist.TrackIDs {
			seen[member] = true
		}
		for _, member := range ids {
			if !seen[member] {
				playlist.TrackIDs = append(playlist.TrackIDs, member)
				seen[member] = true
			}
		}
		return nil
	}, options...)
}
func (s *Store) UpdatePlaylistTrackNote(id, trackID, note string, options ...MutationOptions) (Playlist, error) {
	return s.mutatePlaylist(id, true, func(playlist *Playlist) error {
		if !slices.Contains(playlist.TrackIDs, trackID) {
			return errMemberNotFound
		}
		if playlist.TrackNotes == nil {
			playlist.TrackNotes = map[string]string{}
		}
		if note == "" {
			delete(playlist.TrackNotes, trackID)
		} else {
			playlist.TrackNotes[trackID] = note
		}
		return nil
	}, options...)
}
func (s *Store) RemovePlaylistTrack(id, trackID string, options ...MutationOptions) error {
	_, err := s.mutatePlaylist(id, true, func(playlist *Playlist) error {
		index := slices.Index(playlist.TrackIDs, trackID)
		if index < 0 {
			return errMemberNotFound
		}
		playlist.TrackIDs = slices.Delete(playlist.TrackIDs, index, index+1)
		delete(playlist.TrackNotes, trackID)
		return nil
	}, options...)
	return err
}
func (s *Store) ReorderPlaylistTracks(id string, expected, order []string, options ...MutationOptions) (Playlist, error) {
	return s.mutatePlaylist(id, true, func(playlist *Playlist) error {
		if !slices.Equal(playlist.TrackIDs, expected) {
			return errPlaylistConflict
		}
		first, second := cloneStrings(expected), cloneStrings(order)
		sort.Strings(first)
		sort.Strings(second)
		if !slices.Equal(first, second) {
			return errors.New("order must contain every member exactly once")
		}
		playlist.TrackIDs = cloneStrings(order)
		return nil
	}, options...)
}
func withoutLibrary(ids []string) []string {
	result := []string{}
	for _, id := range ids {
		if id != "library" {
			result = append(result, id)
		}
	}
	return result
}
func (s *Store) ReorderPlaylists(order []string, options ...MutationOptions) error {
	return s.transaction(func(tx *sql.Tx) error {
		owner, err := internalOwner(tx, options)
		if err != nil {
			return err
		}
		if err := checkActorOwner(tx, owner, options); err != nil {
			return err
		}
		playlists, err := queryJSON[Playlist](tx, "SELECT payload FROM playlists WHERE owner_id=? AND system=0", owner)
		order = withoutLibrary(order)
		if err != nil {
			return err
		}
		if len(options) > 0 && options[0].ExpectedOrder != nil {
			sort.Slice(playlists, func(first, second int) bool {
				if playlists[first].SortOrder != playlists[second].SortOrder {
					return playlists[first].SortOrder < playlists[second].SortOrder
				}
				if playlists[first].Name != playlists[second].Name {
					return playlists[first].Name < playlists[second].Name
				}
				return playlists[first].ID < playlists[second].ID
			})
			current := []string{}
			for _, playlist := range playlists {
				current = append(current, playlist.ID)
			}
			if !slices.Equal(current, withoutLibrary(options[0].ExpectedOrder)) {
				return errPlaylistConflict
			}
		}
		if len(order) != len(playlists) {
			return errPlaylistConflict
		}
		available := map[string]Playlist{}
		for _, playlist := range playlists {
			available[playlist.ID] = playlist
		}
		for position, id := range order {
			playlist, ok := available[id]
			if !ok {
				return errPlaylistConflict
			}
			delete(available, id)
			if playlist.SortOrder == position+1 {
				continue
			}
			before := playlist
			playlist.SortOrder = position + 1
			playlist.Version++
			playlist.UpdatedAt = time.Now().UTC()
			if err := writePlaylist(tx, playlist); err != nil {
				return err
			}
			if err := recordChange(tx, "playlist", id, false); err != nil {
				return err
			}
			if err := auditMutation(tx, "playlist.order", id, before, playlist, options); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) DeletePlaylist(id string, options ...MutationOptions) error {
	if id == "library" {
		return errLibraryMembers
	}
	return s.transaction(func(tx *sql.Tx) error {
		before, err := readPlaylistFrom(tx, id)
		if err != nil {
			return err
		}
		if before.ID != "" {
			if err := checkActorOwner(tx, before.OwnerID, options); err != nil {
				return err
			}
		}
		if err := checkVersion(before.Version, options); err != nil {
			return err
		}
		keys, err := queryJSON[APIKey](tx, "SELECT payload FROM api_keys")
		if err != nil {
			return err
		}
		for _, key := range keys {
			index := slices.Index(key.AllowedPlaylistIDs, id)
			if index >= 0 && !hasSubscription(tx, key.UserID, id) {
				key.AllowedPlaylistIDs = slices.Delete(key.AllowedPlaylistIDs, index, index+1)
				if err := saveKey(tx, key); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec("DELETE FROM playlists WHERE id=?", id); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO playlist_tombstones(id) VALUES(?)", id); err != nil {
			return err
		}
		if before.Visibility == "public" {
			if err := bumpPublicCatalog(tx); err != nil {
				return err
			}
		}
		if err := recordChange(tx, "playlist", id, true); err != nil {
			return err
		}
		return auditMutation(tx, "playlist.delete", id, before, nil, options)
	})
}
func (s *Store) Settings() ServerSettings {
	var raw string
	var settings ServerSettings
	if s.db.QueryRow("SELECT value FROM settings WHERE key='server'").Scan(&raw) == nil {
		_ = json.Unmarshal([]byte(raw), &settings)
	}
	settings.MediaRoot = s.mediaRoot
	return settings
}
func (s *Store) UpdateSettings(settings ServerSettings, options ...MutationOptions) error {
	if settings.MediaRoot != "" && settings.MediaRoot != s.mediaRoot {
		return errors.New("media_root is read-only")
	}
	if settings.MaxUploadBytes < 1 || settings.MaxUploadBytes > 4<<30 {
		return errors.New("upload limit must be between 1 byte and 4 GiB")
	}
	if err := update.ValidateRepository(settings.UpdateRepository); err != nil {
		return err
	}
	settings.MediaRoot = s.mediaRoot
	return s.transaction(func(tx *sql.Tx) error {
		var before string
		if err := tx.QueryRow("SELECT value FROM settings WHERE key='server'").Scan(&before); err != nil {
			return err
		}
		encoded, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE settings SET value=? WHERE key='server'", string(encoded)); err != nil {
			return err
		}
		var previous ServerSettings
		_ = json.Unmarshal([]byte(before), &previous)
		return auditMutation(tx, "settings.update", "server", previous, settings, options)
	})
}
func applyMetadata(track *Track, values map[string]string) {
	for field, value := range values {
		switch field {
		case "title":
			track.Title = value
		case "artist":
			track.Artist = value
		case "album":
			track.Album = value
		case "genre":
			track.Genre = value
		case "comment":
			track.Comment = value
		case "lyrics":
			track.Lyrics = value
			track.LyricsSource = "manual"
		case "cover_id":
			track.CoverID = value
		case "cover_mime":
			track.CoverMIME = value
		case "cover_source":
			track.CoverSource = value
		}
	}
}
func (s *Store) UpdateTrack(id string, fields map[string]string, options ...MutationOptions) error {
	if err := validMetadataFields(fields); err != nil {
		return err
	}
	return s.transaction(func(tx *sql.Tx) error {
		before, err := readTrackFrom(tx, id, false)
		if err != nil {
			return err
		}
		if before.ID != "" {
			if err := checkActorOwner(tx, before.OwnerID, options); err != nil {
				return err
			}
		}
		if err := checkVersion(before.Version, options); err != nil {
			return err
		}
		track := before
		applyMetadata(&track, fields)
		track.Version++
		if err := writeTrack(tx, track); err != nil {
			return err
		}
		if err := writeOverrides(tx, id, fields); err != nil {
			return err
		}
		if err := recordChange(tx, "track", id, false); err != nil {
			return err
		}
		return auditMutation(tx, "track.update", id, before, track, options)
	})
}
func (s *Store) DeleteTrack(id string, options ...MutationOptions) error {
	return s.setRemoved(id, true, options)
}
func (s *Store) RestoreTrack(id string, options ...MutationOptions) error {
	return s.setRemoved(id, false, options)
}
func (s *Store) setRemoved(id string, removed bool, options []MutationOptions) error {
	return s.transaction(func(tx *sql.Tx) error {
		before, err := readTrackFrom(tx, id, true)
		if err != nil {
			return err
		}
		if before.ID != "" {
			if err := checkActorOwner(tx, before.OwnerID, options); err != nil {
				return err
			}
		}
		if len(options) > 0 && options[0].UserID != "" {
			actor, err := readUser(tx, options[0].UserID)
			if err != nil {
				return err
			}
			if actor.Role != "admin" {
				if !removed {
					return errResourceHidden
				}
				var referenced int
				if err := tx.QueryRow("SELECT count(*) FROM playlist_tracks m JOIN playlists p ON p.id=m.playlist_id WHERE m.track_id=? AND p.system=0 AND p.owner_id<>?", id, actor.ID).Scan(&referenced); err != nil {
					return err
				}
				if referenced > 0 {
					return errTrackInUse
				}
			}
		}
		if err := checkVersion(before.Version, options); err != nil {
			return err
		}
		if before.Removed == removed {
			return nil
		}
		track := before
		track.Removed = removed
		track.Version++
		if err := writeTrack(tx, track); err != nil {
			return err
		}
		if err := recordChange(tx, "track", id, removed); err != nil {
			return err
		}
		playlists, err := queryJSON[Playlist](tx, "SELECT p.payload FROM playlists p JOIN playlist_tracks m ON m.playlist_id=p.id WHERE m.track_id=? AND p.system=0", id)
		if err != nil {
			return err
		}
		for _, p := range playlists {
			if err := recordChange(tx, "playlist", p.ID, false); err != nil {
				return err
			}
		}
		if err := refreshLibrary(tx); err != nil {
			return err
		}
		return auditMutation(tx, "track.removed", id, before, track, options)
	})
}
func refreshLibrary(tx *sql.Tx) error {
	before, err := readPlaylistFrom(tx, "library")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tracks, err := queryJSON[Track](tx, "SELECT payload FROM tracks WHERE removed=0 ORDER BY title,id")
	if err != nil {
		return err
	}
	ids := []string{}
	for _, track := range tracks {
		ids = append(ids, track.ID)
	}
	if before.ID != "" && slices.Equal(before.TrackIDs, ids) {
		return nil
	}
	playlist := before
	playlist.ID = "library"
	playlist.System = true
	if playlist.Name == "" {
		playlist.Name = "All music"
	}
	playlist.TrackIDs = ids
	playlist.Version++
	playlist.UpdatedAt = time.Now().UTC()
	if err := writePlaylist(tx, playlist); err != nil {
		return err
	}
	return recordChange(tx, "playlist", "library", false)
}
func (s *Store) MediaPath(track Track) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(track.Path)) {
		return "", errors.New("track path escapes media root")
	}
	return filepath.Join(s.mediaRoot, filepath.FromSlash(track.Path)), nil
}
func (s *Store) ImportAudio(name string, reader io.Reader) (Track, error) {
	return s.ImportAudioWithMetadata(name, reader, nil)
}
func (s *Store) ImportAudioWithMetadata(name string, reader io.Reader, fields map[string]string, options ...MutationOptions) (Track, error) {
	stage, err := s.StageAudio(name, reader, fields, options...)
	if err != nil {
		return Track{}, err
	}
	track, _, err := s.CommitUploadStage(stage.ID, options...)
	if err != nil {
		_ = s.DeleteUploadStage(stage.ID)
	}
	return track, err
}
