package music

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"musicserver/internal/update"
)

var audioExtensions = map[string]bool{
	".aac": true, ".flac": true, ".m4a": true, ".mp3": true,
	".oga": true, ".ogg": true, ".opus": true, ".wav": true, ".wma": true,
}

type Store struct {
	mu        sync.RWMutex
	dataPath  string
	mediaRoot string
	data      Library
	db        *sql.DB
}

func NewStore(dataPath, mediaRoot string) (*Store, error) {
	if strings.EqualFold(filepath.Ext(dataPath), ".json") {
		return nil, errors.New("JSON data paths are no longer supported; use a SQLite database path")
	}
	root, err := filepath.Abs(mediaRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve media root: %w", err)
	}
	root = filepath.Clean(root)
	store := &Store{dataPath: dataPath, mediaRoot: root, data: emptyLibrary()}
	if err := store.load(); err != nil {
		if store.db != nil {
			_ = store.db.Close()
		}
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	return s.loadSQLite()
}

func randomToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func hashSecret(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

func normalizeRole(role string) string {
	if role == "admin" || role == "readonly" || role == "device" {
		return role
	}
	return "player"
}

func cloneStrings(values []string) []string {
	return append([]string{}, values...)
}

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

func (s *Store) CreateAPIKey(request APIKeyCreateRequest) (APIKeyView, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return APIKeyView{}, errors.New("key name is required")
	}
	secret, err := randomToken(32)
	if err != nil {
		return APIKeyView{}, err
	}
	scopes := cloneStrings(request.Scopes)
	if request.Scopes == nil {
		scopes = defaultScopes()
		if normalizeRole(request.Role) == "admin" {
			scopes = []string{ScopeLibraryRead, ScopeLibraryWrite, ScopeMediaRead, ScopeUploadsWrite, ScopeKeysManage, ScopeSettingsManage, ScopeStatsRead}
		}
	}
	if err := validateScopes(scopes); err != nil {
		return APIKeyView{}, err
	}
	id, err := randomToken(12)
	if err != nil {
		return APIKeyView{}, err
	}
	key := APIKey{ID: id, Name: name, SecretHash: hashSecret(secret), Role: normalizeRole(request.Role), Scopes: scopes, AllowedPlaylistIDs: cloneStrings(request.AllowedPlaylistIDs), DeviceID: strings.TrimSpace(request.DeviceID), MaxDevices: request.MaxDevices, MaxConcurrency: request.MaxConcurrency, CreatedAt: time.Now().UTC(), ExpiresAt: request.ExpiresAt}
	if key.MaxDevices < 1 {
		key.MaxDevices = 1
	}
	if key.MaxConcurrency < 1 {
		key.MaxConcurrency = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.cloneLocked()
	if s.data.Keys == nil {
		s.data.Keys = map[string]APIKey{}
	}
	s.data.Keys[id] = key
	s.data.Revision++
	if err := s.commitLocked(previous); err != nil {
		return APIKeyView{}, err
	}
	return APIKeyView{APIKey: cloneAPIKey(s.data.Keys[id]), Secret: secret}, nil
}

func (s *Store) APIKeys() []APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]APIKey, 0, len(s.data.Keys))
	for _, key := range s.data.Keys {
		keys = append(keys, cloneAPIKey(key))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].CreatedAt.Before(keys[j].CreatedAt) })
	return keys
}

func (s *Store) AuthenticateSecret(secret string) (APIKey, bool) {
	if secret == "" {
		return APIKey{}, false
	}
	hash := hashSecret(secret)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, key := range s.data.Keys {
		if subtle.ConstantTimeCompare([]byte(key.SecretHash), []byte(hash)) != 1 {
			continue
		}
		if !key.RevokedAt.IsZero() || (!key.ExpiresAt.IsZero() && time.Now().UTC().After(key.ExpiresAt)) {
			return APIKey{}, false
		}
		key.LastUsedAt = time.Now().UTC()
		s.data.Keys[id] = key
		return cloneAPIKey(key), true
	}
	return APIKey{}, false
}

func (s *Store) RevokeAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.data.Keys[id]
	if !ok {
		return os.ErrNotExist
	}
	if !key.RevokedAt.IsZero() {
		return nil
	}
	previous := s.cloneLocked()
	key.RevokedAt = time.Now().UTC()
	s.data.Keys[id] = key
	s.data.Revision++
	return s.commitLocked(previous)
}

func (s *Store) RotateAPIKey(id string) (APIKeyView, error) {
	secret, err := randomToken(32)
	if err != nil {
		return APIKeyView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.data.Keys[id]
	if !ok {
		return APIKeyView{}, os.ErrNotExist
	}
	previous := s.cloneLocked()
	key.SecretHash = hashSecret(secret)
	key.RevokedAt = time.Time{}
	key.LastUsedAt = time.Time{}
	s.data.Keys[id] = key
	s.data.Revision++
	if err := s.commitLocked(previous); err != nil {
		return APIKeyView{}, err
	}
	return APIKeyView{APIKey: cloneAPIKey(key), Secret: secret}, nil
}

func (s *Store) UpdateAPIKey(id string, request APIKeyUpdateRequest) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.data.Keys[id]
	if !ok {
		return APIKey{}, os.ErrNotExist
	}
	if request.Name != nil {
		name := strings.TrimSpace(*request.Name)
		if name == "" {
			return APIKey{}, errors.New("key name is required")
		}
		key.Name = name
	}
	if request.Role != nil {
		key.Role = normalizeRole(*request.Role)
	}
	if request.Scopes != nil {
		if err := validateScopes(*request.Scopes); err != nil {
			return APIKey{}, err
		}
		key.Scopes = cloneStrings(*request.Scopes)
	}
	if request.AllowedPlaylistIDs != nil {
		key.AllowedPlaylistIDs = cloneStrings(*request.AllowedPlaylistIDs)
	}
	if request.DeviceID != nil {
		key.DeviceID = strings.TrimSpace(*request.DeviceID)
	}
	if request.MaxDevices != nil {
		if *request.MaxDevices < 1 {
			return APIKey{}, errors.New("max_devices must be at least 1")
		}
		key.MaxDevices = *request.MaxDevices
	}
	if request.MaxConcurrency != nil {
		if *request.MaxConcurrency < 1 {
			return APIKey{}, errors.New("max_concurrency must be at least 1")
		}
		key.MaxConcurrency = *request.MaxConcurrency
	}
	if request.ExpiresAt != nil {
		key.ExpiresAt = request.ExpiresAt.UTC()
	}
	previous := s.cloneLocked()
	s.data.Keys[id] = key
	s.data.Revision++
	if err := s.commitLocked(previous); err != nil {
		return APIKey{}, err
	}
	return cloneAPIKey(s.data.Keys[id]), nil
}

func (s *Store) saveLocked() error {
	if err := saveSQLiteSnapshot(s.db, s.data); err != nil {
		return err
	}
	dropMissingPlaylistGrants(&s.data)
	return nil
}

func (s *Store) Scan() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.cloneLocked()
	success := false
	defer func() {
		if !success {
			s.data = previous
		}
	}()
	if _, err := os.Stat(s.mediaRoot); err != nil {
		return 0, fmt.Errorf("media root: %w", err)
	}
	tracks := map[string]Track{}
	err := filepath.WalkDir(s.mediaRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".staging" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !audioExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			return nil
		}
		track, metadata, err := s.readTrackWithMetadata(path, filepath.Base(path))
		if err != nil {
			return err
		}
		if metadata.CoverSource != "" {
			coverID, coverErr := s.saveCoverLocked(metadata.Cover, metadata.CoverMIME, metadata.CoverSource)
			if coverErr != nil {
				return coverErr
			}
			track.CoverID = coverID
		}
		if !s.data.Excluded[track.ID] {
			applyMetadata(&track, s.data.Overrides[track.ID])
			tracks[track.ID] = track
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan media: %w", err)
	}
	s.data.Tracks = tracks
	s.data.Revision++
	if playlist, ok := s.data.Playlists["library"]; !ok || playlist.Name == "" {
		playlist = Playlist{ID: "library", Name: "All music"}
		playlist.TrackIDs = sortedTrackIDs(tracks)
		playlist.UpdatedAt = time.Now().UTC()
		s.data.Playlists[playlist.ID] = playlist
	} else {
		playlist.TrackIDs = sortedTrackIDs(tracks)
		playlist.UpdatedAt = time.Now().UTC()
		s.data.Playlists[playlist.ID] = playlist
	}
	for id, playlist := range s.data.Playlists {
		remaining := []string{}
		for _, trackID := range playlist.TrackIDs {
			if _, exists := tracks[trackID]; exists {
				remaining = append(remaining, trackID)
			} else {
				delete(playlist.TrackNotes, trackID)
			}
		}
		playlist.TrackIDs = remaining
		s.data.Playlists[id] = playlist
	}
	if err := s.commitLocked(previous); err != nil {
		return 0, err
	}
	success = true
	return len(tracks), nil
}

func (s *Store) readTrack(path string) (Track, error) {
	track, _, err := s.readTrackWithMetadata(path, filepath.Base(path))
	return track, err
}

func (s *Store) readTrackWithMetadata(path, originalName string) (Track, audioMetadata, error) {
	relativePath, err := filepath.Rel(s.mediaRoot, path)
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	relativePath = filepath.ToSlash(relativePath)
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	defer root.Close()
	file, err := root.Open(relativePath)
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	identity := sha256.Sum256([]byte(relativePath))
	hasher := sha256.New()
	_, err = io.Copy(hasher, file)
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	contentDigest := hex.EncodeToString(hasher.Sum(nil))
	// Invalid or absent tags retain filename defaults without rejecting audio.
	metadata, _ := readAudioMetadata(file)
	// Embedded artwork wins; when absent, use a validated same-basename or
	// conventional folder image. Asset persistence is performed by the caller
	// that owns the SQLite assets table; this reader returns the bytes in the
	// metadata value for that purpose.
	if s.data.Settings.ScanExternalCovers {
		metadata = selectCover(root, relativePath, metadata)
	}
	technical := technicalMetadata(path, s.data.Settings.FFprobePath)
	baseName := strings.TrimSuffix(filepath.Base(originalName), filepath.Ext(originalName))
	title, artist := baseName, ""
	if parts := strings.SplitN(baseName, " - ", 2); len(parts) == 2 {
		title, artist = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	if metadata.Title != "" {
		title = metadata.Title
	}
	if metadata.Artist != "" {
		artist = metadata.Artist
	}
	embeddedLyrics := metadata.Lyrics != ""
	lyrics, lyricsSource := metadata.Lyrics, metadata.LyricsSource
	if s.data.Settings.ScanExternalLyrics {
		if value, source := readExternalLyrics(root, relativePath); source != "" {
			lyrics, lyricsSource = value, source
		}
	}
	return Track{
		ID:             hex.EncodeToString(identity[:]),
		Title:          title,
		Artist:         artist,
		Album:          metadata.Album,
		Genre:          metadata.Genre,
		Comment:        metadata.Comment,
		Lyrics:         lyrics,
		EmbeddedLyrics: embeddedLyrics,
		LyricsSource:   lyricsSource,
		CoverMIME:      metadata.CoverMIME,
		CoverSource:    metadata.CoverSource,
		DurationMS:     technical.DurationMS,
		BitrateKbps:    int64(technical.BitrateKbps),
		SampleRate:     technical.SampleRate,
		Channels:       technical.Channels,
		BitsPerSample:  technical.BitsPerSample,
		Path:           relativePath,
		Size:           info.Size(),
		ContentHash:    contentDigest,
		Format:         strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."),
		ModifiedAt:     info.ModTime().UTC(),
	}, metadata, nil
}

func sortedTrackIDs(tracks map[string]Track) []string {
	ids := make([]string, 0, len(tracks))
	for id := range tracks {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(first, second int) bool {
		if tracks[ids[first]].Title == tracks[ids[second]].Title {
			return ids[first] < ids[second]
		}
		return tracks[ids[first]].Title < tracks[ids[second]].Title
	})
	return ids
}

func (s *Store) Revision() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Revision
}

func (s *Store) Tracks() []Track {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tracks := make([]Track, 0, len(s.data.Tracks))
	for _, track := range s.data.Tracks {
		tracks = append(tracks, track)
	}
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].Title < tracks[j].Title })
	return tracks
}

func (s *Store) Track(id string) (Track, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	track, ok := s.data.Tracks[id]
	return track, ok
}

func (s *Store) Playlists() []Playlist {
	s.mu.RLock()
	defer s.mu.RUnlock()
	playlists := make([]Playlist, 0, len(s.data.Playlists))
	for _, playlist := range s.data.Playlists {
		playlists = append(playlists, clonePlaylist(playlist))
	}
	sort.SliceStable(playlists, func(i, j int) bool {
		if playlists[i].SortOrder == playlists[j].SortOrder {
			return playlists[i].Name < playlists[j].Name
		}
		return playlists[i].SortOrder < playlists[j].SortOrder
	})
	return playlists
}

func (s *Store) Playlist(id string) (Playlist, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	playlist, ok := s.data.Playlists[id]
	return clonePlaylist(playlist), ok
}

func (s *Store) PutPlaylist(playlist Playlist) error {
	if !validID.MatchString(playlist.ID) || strings.TrimSpace(playlist.Name) == "" {
		return errors.New("playlist id and name are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	playlist = clonePlaylist(playlist)
	if playlist.ID == "library" {
		playlist.TrackIDs = sortedTrackIDs(s.data.Tracks)
	}
	for _, trackID := range playlist.TrackIDs {
		if _, ok := s.data.Tracks[trackID]; !ok {
			return fmt.Errorf("unknown track: %s", trackID)
		}
	}
	members := make(map[string]struct{}, len(playlist.TrackIDs))
	for _, trackID := range playlist.TrackIDs {
		members[trackID] = struct{}{}
	}
	for trackID := range playlist.TrackNotes {
		if _, ok := members[trackID]; !ok {
			return fmt.Errorf("track note references non-member track: %s", trackID)
		}
	}
	previous := s.cloneLocked()
	playlist.UpdatedAt = time.Now().UTC()
	if existing, exists := s.data.Playlists[playlist.ID]; exists {
		// Display order changes only through ReorderPlaylists, never a stale PUT.
		playlist.SortOrder = existing.SortOrder
	} else if playlist.ID != "library" {
		maxOrder := 0
		for _, existing := range s.data.Playlists {
			if existing.SortOrder > maxOrder {
				maxOrder = existing.SortOrder
			}
		}
		playlist.SortOrder = maxOrder + 1
	}
	if playlist.TrackIDs == nil {
		playlist.TrackIDs = []string{}
	}
	s.data.Playlists[playlist.ID] = playlist
	s.data.Revision++
	return s.commitLocked(previous)
}

func (s *Store) ReorderPlaylists(order []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	next := make([]string, 0, len(s.data.Playlists))
	for _, id := range order {
		if _, ok := s.data.Playlists[id]; !ok || seen[id] {
			continue
		}
		seen[id] = true
		next = append(next, id)
	}
	for id := range s.data.Playlists {
		if !seen[id] {
			next = append(next, id)
		}
	}
	previous := s.cloneLocked()
	for position, id := range next {
		playlist := s.data.Playlists[id]
		playlist.SortOrder = position
		playlist.UpdatedAt = time.Now().UTC()
		s.data.Playlists[id] = playlist
	}
	s.data.Revision++
	return s.commitLocked(previous)
}

func (s *Store) Settings() ServerSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings := s.data.Settings
	settings.MediaRoot = s.mediaRoot
	return settings
}

func (s *Store) UpdateSettings(settings ServerSettings) error {
	if settings.MediaRoot != "" && filepath.Clean(settings.MediaRoot) != s.mediaRoot {
		return errors.New("media_root is a startup setting; change -media-root and restart the server")
	}
	if settings.FFprobePath != "" {
		if info, err := os.Stat(settings.FFprobePath); err != nil || info.IsDir() {
			return errors.New("ffprobe_path must point to an existing executable")
		}
	}
	if settings.MaxUploadBytes < 1<<20 || settings.MaxUploadBytes > 4<<30 {
		return errors.New("max_upload_bytes must be between 1 MiB and 4 GiB")
	}
	if err := update.ValidateRepository(settings.UpdateRepository); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.cloneLocked()
	settings.MediaRoot = s.mediaRoot
	s.data.Settings = settings
	s.data.Revision++
	return s.commitLocked(previous)
}

func (s *Store) Sync(since int64) SyncResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	response := SyncResponse{Revision: s.data.Revision, Changed: []Track{}, Playlists: []Playlist{}}
	if since == s.data.Revision {
		return response
	}
	response.FullSnapshot = true
	response.Changed = make([]Track, 0, len(s.data.Tracks))
	for _, track := range s.data.Tracks {
		response.Changed = append(response.Changed, track)
	}
	response.Playlists = make([]Playlist, 0, len(s.data.Playlists))
	for _, playlist := range s.data.Playlists {
		response.Playlists = append(response.Playlists, clonePlaylist(playlist))
	}
	sort.Slice(response.Playlists, func(i, j int) bool {
		first, second := response.Playlists[i], response.Playlists[j]
		if first.SortOrder != second.SortOrder {
			return first.SortOrder < second.SortOrder
		}
		if first.Name != second.Name {
			return first.Name < second.Name
		}
		return first.ID < second.ID
	})
	return response
}

func (s *Store) MediaPath(track Track) (string, error) {
	path := filepath.Join(s.mediaRoot, filepath.FromSlash(track.Path))
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rootWithSeparator := s.mediaRoot + string(os.PathSeparator)
	if absPath != s.mediaRoot && !strings.HasPrefix(absPath, rootWithSeparator) {
		return "", errors.New("track path escapes media root")
	}
	return absPath, nil
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func clonePlaylist(value Playlist) Playlist {
	value.TrackIDs = append([]string{}, value.TrackIDs...)
	if value.TrackNotes != nil {
		notes := map[string]string{}
		for id, note := range value.TrackNotes {
			notes[id] = note
		}
		value.TrackNotes = notes
	}
	return value
}

func (s *Store) cloneLocked() Library {
	encoded, _ := json.Marshal(s.data)
	var copy Library
	_ = json.Unmarshal(encoded, &copy)
	normalizeLibrary(&copy)
	for id, asset := range s.data.CoverAssets {
		asset.Data = append([]byte(nil), asset.Data...)
		copy.CoverAssets[id] = asset
	}
	return copy
}

func (s *Store) commitLocked(previous Library) error {
	if err := s.saveLocked(); err != nil {
		s.data = previous
		return err
	}
	return nil
}

func applyMetadata(track *Track, values map[string]string) {
	for key, value := range values {
		switch key {
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

func (s *Store) UpdateTrack(id string, fields map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	track, exists := s.data.Tracks[id]
	if !exists {
		return os.ErrNotExist
	}
	for key := range fields {
		if key != "title" && key != "artist" && key != "album" && key != "genre" && key != "comment" && key != "lyrics" {
			return fmt.Errorf("unsupported metadata field: %s", key)
		}
	}
	previous := s.cloneLocked()
	normalizeLibrary(&s.data)
	if s.data.Overrides[id] == nil {
		s.data.Overrides[id] = map[string]string{}
	}
	for key, value := range fields {
		s.data.Overrides[id][key] = value
	}
	applyMetadata(&track, fields)
	s.data.Tracks[id] = track
	s.data.Revision++
	return s.commitLocked(previous)
}

func (s *Store) DeleteTrack(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.data.Tracks[id]; !exists {
		return os.ErrNotExist
	}
	previous := s.cloneLocked()
	delete(s.data.Tracks, id)
	s.data.Excluded[id] = true
	for playlistID, playlist := range s.data.Playlists {
		remaining := []string{}
		for _, trackID := range playlist.TrackIDs {
			if trackID != id {
				remaining = append(remaining, trackID)
			}
		}
		playlist.TrackIDs = remaining
		delete(playlist.TrackNotes, id)
		playlist.UpdatedAt = time.Now().UTC()
		s.data.Playlists[playlistID] = playlist
	}
	s.data.Revision++
	return s.commitLocked(previous)
}

func (s *Store) DeletePlaylist(id string) error {
	if id == "library" {
		return errors.New("the library playlist cannot be deleted")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.data.Playlists[id]; !exists {
		return os.ErrNotExist
	}
	previous := s.cloneLocked()
	delete(s.data.Playlists, id)
	s.data.Revision++
	return s.commitLocked(previous)
}

func (s *Store) ImportAudio(name string, reader io.Reader) (Track, error) {
	return s.ImportAudioWithMetadata(name, reader, nil)
}

// ImportAudioWithMetadata commits the file's tags and user edits as one mutation.
// A failed save restores the library and removes the newly uploaded file.
func (s *Store) ImportAudioWithMetadata(name string, reader io.Reader, fields map[string]string) (Track, error) {
	extension := strings.ToLower(filepath.Ext(filepath.Base(name)))
	if !audioExtensions[extension] {
		return Track{}, errors.New("unsupported audio extension")
	}
	for key := range fields {
		if key != "title" && key != "artist" && key != "album" && key != "genre" && key != "comment" && key != "lyrics" {
			return Track{}, fmt.Errorf("unsupported metadata field: %s", key)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return Track{}, err
	}
	defer root.Close()
	if err := root.MkdirAll("uploads", 0o755); err != nil {
		return Track{}, err
	}
	storedName := fmt.Sprintf("uploads/%d%s", time.Now().UnixNano(), extension)
	file, err := root.OpenFile(storedName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Track{}, err
	}
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = root.Remove(storedName)
		return Track{}, errors.Join(copyErr, closeErr)
	}
	track, metadata, err := s.readTrackWithMetadata(filepath.Join(s.mediaRoot, filepath.FromSlash(storedName)), name)
	if err != nil {
		_ = root.Remove(storedName)
		return Track{}, err
	}
	for _, existing := range s.data.Tracks {
		if existing.ContentHash != "" && existing.ContentHash == track.ContentHash {
			_ = root.Remove(storedName)
			return existing, nil
		}
	}
	previous := s.cloneLocked()
	if metadata.CoverSource != "" {
		coverID, coverErr := s.saveCoverLocked(metadata.Cover, metadata.CoverMIME, metadata.CoverSource)
		if coverErr != nil {
			_ = root.Remove(storedName)
			return Track{}, coverErr
		}
		track.CoverID = coverID
	}
	overrides := map[string]string{}
	// Preserve original-name defaults when the stored filename is a timestamp.
	if metadata.Title == "" {
		overrides["title"] = track.Title
	}
	if metadata.Artist == "" && track.Artist != "" {
		overrides["artist"] = track.Artist
	}
	for key, value := range fields {
		overrides[key] = value
	}
	applyMetadata(&track, fields)
	s.data.Tracks[track.ID] = track
	if len(overrides) > 0 {
		s.data.Overrides[track.ID] = overrides
	}
	playlist := s.data.Playlists["library"]
	playlist.ID = "library"
	if playlist.Name == "" {
		playlist.Name = "All music"
	}
	playlist.TrackIDs = sortedTrackIDs(s.data.Tracks)
	playlist.UpdatedAt = time.Now().UTC()
	s.data.Playlists["library"] = playlist
	s.data.Revision++
	if err := s.commitLocked(previous); err != nil {
		_ = root.Remove(storedName)
		return Track{}, err
	}
	return track, nil
}
