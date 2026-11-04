package music

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// Quotas describe logical account assets. Scanner-maintained sizes avoid disk
// walks in an admission/write lock; external filesystem edits require a scan.
// Covers/lyrics copied out of audio are not billed again when still embedded.
type storageReference struct {
	asset, kind string
	bytes       int64
	owner       string
}
type storageBreakdown struct {
	AudioBytes  int64 `json:"audio_bytes"`
	CoverBytes  int64 `json:"cover_bytes"`
	LyricsBytes int64 `json:"lyrics_bytes"`
}

func (b storageBreakdown) total() int64 { return b.AudioBytes + b.CoverBytes + b.LyricsBytes }
func trackStorageRefs(reader sqlReader, t Track) ([]storageReference, error) {
	refs := []storageReference{}
	if !t.Missing && t.Size > 0 {
		refs = append(refs, storageReference{asset: "audio:" + t.Path, kind: "audio", bytes: t.Size})
	}
	if t.CoverID != "" && t.CoverSource != "embedded" {
		var size int64
		err := reader.QueryRow("SELECT length(data) FROM cover_assets WHERE id=?", t.CoverID).Scan(&size)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if size > 0 {
			refs = append(refs, storageReference{asset: "cover:" + t.CoverID, kind: "cover", bytes: size})
		}
	}
	if t.Lyrics != "" && (t.LyricsSource == "manual" || t.LyricsSource == "sidecar" || t.LyricsSource == "external" || !t.EmbeddedLyrics) {
		refs = append(refs, storageReference{asset: "lyrics:" + hashBytes([]byte(t.Lyrics)), kind: "lyrics", bytes: int64(len(t.Lyrics))})
	}
	return refs, nil
}
func playlistStorageRefs(reader sqlReader, p Playlist) ([]storageReference, error) {
	if p.System || p.OwnerID == "" || p.CoverID == "" {
		return nil, nil
	}
	refs, err := trackStorageRefs(reader, Track{CoverID: p.CoverID})
	return refs, err
}
func storageProjectionReady(reader sqlReader) bool {
	var exists bool
	return reader.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='browse_storage_refs')").Scan(&exists) == nil && exists
}
func accountStorageBreakdown(reader sqlReader, id string) (storageBreakdown, error) {
	var b storageBreakdown
	if !storageProjectionReady(reader) {
		err := reader.QueryRow("SELECT COALESCE(sum(json_extract(payload,'$.size')),0) FROM tracks WHERE uploaded_by=? AND missing=0 AND json_valid(payload)", id).Scan(&b.AudioBytes)
		return b, err
	}
	err := reader.QueryRow(`SELECT COALESCE(sum(CASE WHEN kind='audio' THEN bytes ELSE 0 END),0),COALESCE(sum(CASE WHEN kind='cover' THEN bytes ELSE 0 END),0),COALESCE(sum(CASE WHEN kind='lyrics' THEN bytes ELSE 0 END),0) FROM (SELECT asset,kind,max(bytes) bytes FROM browse_storage_refs WHERE owner=? AND resource_type<>'stage' GROUP BY asset,kind)`, id).Scan(&b.AudioBytes, &b.CoverBytes, &b.LyricsBytes)
	return b, err
}
func replaceStorageRefs(tx *sql.Tx, kind, id, owner string, refs []storageReference, enforce bool) error {
	var before storageBreakdown
	var err error
	if enforce {
		before, err = accountStorageBreakdown(tx, owner)
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM browse_storage_refs WHERE resource_type=? AND resource_id=?", kind, id); err != nil {
		return err
	}
	if owner != "" {
		for _, ref := range refs {
			if ref.bytes > 0 {
				if _, err = tx.Exec("INSERT INTO browse_storage_refs VALUES(?,?,?,?,?,?)", kind, id, owner, ref.asset, ref.kind, ref.bytes); err != nil {
					return err
				}
			}
		}
	}
	if enforce {
		after, err := accountStorageBreakdown(tx, owner)
		if err != nil {
			return err
		}
		if after.total() > before.total() {
			var quota StorageQuota
			if err := readStorageJSON(tx, "storage_quota/"+owner, &quota); err != nil {
				return err
			}
			if quota.LimitBytes > 0 && after.total() > quota.LimitBytes {
				return errStorageQuota
			}
		}
	}
	return nil
}
func writeTrackStorageWithEnforcement(tx *sql.Tx, t Track, enforce bool) error {
	if !storageProjectionReady(tx) {
		return nil
	}
	refs, err := trackStorageRefs(tx, t)
	if err != nil {
		return err
	}
	return replaceStorageRefs(tx, "track", t.ID, t.UploadedBy, refs, enforce)
}

// reconcileTrackStorage rewrites derived rows only when the observed projection
// differs, and never enforces the quota: the media scan is the maintenance path
// that must still correct externally changed files for an over-quota account.
func reconcileTrackStorage(tx *sql.Tx, t Track) error {
	if !storageProjectionReady(tx) {
		return nil
	}
	refs, err := trackStorageRefs(tx, t)
	if err != nil {
		return err
	}
	rows, err := tx.Query("SELECT asset,kind,bytes,owner FROM browse_storage_refs WHERE resource_type='track' AND resource_id=?", t.ID)
	if err != nil {
		return err
	}
	stored := map[string]storageReference{}
	for rows.Next() {
		var ref storageReference
		if err := rows.Scan(&ref.asset, &ref.kind, &ref.bytes, &ref.owner); err != nil {
			rows.Close()
			return err
		}
		stored[ref.asset] = ref
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	current := 0
	for _, ref := range refs {
		if ref.bytes <= 0 {
			continue
		}
		current++
		got, ok := stored[ref.asset]
		if !ok || got.kind != ref.kind || got.bytes != ref.bytes || got.owner != t.UploadedBy {
			return replaceStorageRefs(tx, "track", t.ID, t.UploadedBy, refs, false)
		}
	}
	if current != len(stored) {
		return replaceStorageRefs(tx, "track", t.ID, t.UploadedBy, refs, false)
	}
	return nil
}
func writePlaylistStorage(tx *sql.Tx, p Playlist) error {
	if !storageProjectionReady(tx) {
		return nil
	}
	refs, err := playlistStorageRefs(tx, p)
	if err != nil {
		return err
	}
	return replaceStorageRefs(tx, "playlist", p.ID, p.OwnerID, refs, true)
}

func stageStorageRefs(record stagedRecord) []storageReference {
	refs := []storageReference{}
	if len(record.Cover) > 0 && record.Track.CoverSource != "embedded" {
		refs = append(refs, storageReference{asset: "cover:" + hashBytes(record.Cover), kind: "cover", bytes: int64(len(record.Cover))})
	}
	t := record.Track
	if t.Lyrics != "" && (t.LyricsSource == "manual" || t.LyricsSource == "sidecar" || t.LyricsSource == "external" || !t.EmbeddedLyrics) {
		refs = append(refs, storageReference{asset: "lyrics:" + hashBytes([]byte(t.Lyrics)), kind: "lyrics", bytes: int64(len(t.Lyrics))})
	}
	return refs
}
func pendingAttachmentBytes(reader sqlReader, owner string) (int64, error) {
	if !storageProjectionReady(reader) {
		return 0, nil
	}
	var total int64
	err := reader.QueryRow(`SELECT COALESCE(sum(bytes),0) FROM (SELECT r.asset,max(r.bytes) bytes FROM browse_storage_refs r WHERE r.owner=? AND r.resource_type='stage' AND NOT EXISTS(SELECT 1 FROM browse_storage_refs c WHERE c.owner=r.owner AND c.asset=r.asset AND c.resource_type<>'stage') GROUP BY r.asset)`, owner).Scan(&total)
	return total, err
}
func writeStageStorage(tx *sql.Tx, id, owner string, record stagedRecord, enforce bool) error {
	if !storageProjectionReady(tx) {
		return nil
	}
	before, err := pendingAttachmentBytes(tx, owner)
	if err != nil {
		return err
	}
	if err := replaceStorageRefs(tx, "stage", id, owner, stageStorageRefs(record), false); err != nil {
		return err
	}
	after, err := pendingAttachmentBytes(tx, owner)
	if err != nil {
		return err
	}
	if !enforce || after <= before {
		return nil
	}
	var quota StorageQuota
	if err := readStorageJSON(tx, "storage_quota/"+owner, &quota); err != nil {
		return err
	}
	if quota.LimitBytes == 0 {
		return nil
	}
	b, err := accountStorageBreakdown(tx, owner)
	if err != nil {
		return err
	}
	pending, err := temporaryPendingBytes(tx, owner)
	if err != nil {
		return err
	}
	if b.total() > quota.LimitBytes || pending > quota.LimitBytes-b.total() {
		return errStorageQuota
	}
	return nil
}

// Only derived rows are rebuilt on version changes. Never rewrite legacy domain
// JSON, and preserve over-quota accounts while allowing deletions/shrinking edits.
func initializeStorageProjection(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS browse_storage_refs(resource_type TEXT NOT NULL,resource_id TEXT NOT NULL,owner TEXT NOT NULL,asset TEXT NOT NULL,kind TEXT NOT NULL,bytes INTEGER NOT NULL,PRIMARY KEY(resource_type,resource_id,asset));
 DROP INDEX IF EXISTS browse_storage_owner;
 CREATE INDEX IF NOT EXISTS browse_storage_owner ON browse_storage_refs(owner,asset,kind,bytes);
 DELETE FROM browse_storage_refs;
 CREATE TABLE IF NOT EXISTS browse_temp_metadata(owner TEXT PRIMARY KEY,bytes INTEGER NOT NULL);
 DELETE FROM browse_temp_metadata;`); err != nil {
		return err
	}
	for _, table := range []string{"tracks", "playlists"} {
		rows, err := tx.Query("SELECT payload FROM " + table + " WHERE json_valid(payload)")
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			if table == "tracks" {
				var t Track
				if err = json.Unmarshal([]byte(raw), &t); err == nil {
					var refs []storageReference
					refs, err = trackStorageRefs(tx, t)
					if err == nil {
						err = replaceStorageRefs(tx, "track", t.ID, t.UploadedBy, refs, false)
					}
				}
			} else {
				var p Playlist
				if err = json.Unmarshal([]byte(raw), &p); err == nil {
					var refs []storageReference
					refs, err = playlistStorageRefs(tx, p)
					if err == nil {
						err = replaceStorageRefs(tx, "playlist", p.ID, p.OwnerID, refs, false)
					}
				}
			}
			if err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE TRIGGER browse_storage_track_delete AFTER DELETE ON tracks BEGIN DELETE FROM browse_storage_refs WHERE resource_type='track' AND resource_id=OLD.id; END;
 CREATE TRIGGER browse_storage_playlist_delete AFTER DELETE ON playlists BEGIN DELETE FROM browse_storage_refs WHERE resource_type='playlist' AND resource_id=OLD.id; END;
 CREATE TRIGGER browse_storage_stage_delete AFTER DELETE ON upload_staging BEGIN DELETE FROM browse_storage_refs WHERE resource_type='stage' AND resource_id=OLD.id; END;
 INSERT INTO browse_temp_metadata SELECT owner,sum(bytes) FROM (
 SELECT user_id owner,length(CAST(track_json AS BLOB)) bytes FROM upload_staging UNION ALL SELECT CASE WHEN json_valid(value) THEN COALESCE(json_extract(value,'$.owner'),'') ELSE '' END,length(CAST(value AS BLOB)) FROM meta WHERE key LIKE 'upload_resume/%') GROUP BY owner;
 INSERT INTO browse_temp_metadata VALUES('*',(SELECT COALESCE(sum(bytes),0) FROM browse_temp_metadata)) ON CONFLICT(owner) DO UPDATE SET bytes=excluded.bytes;`); err != nil {
		return err
	}
	for _, table := range []string{"upload_staging", "meta"} {
		owner := func(alias string) string {
			if table == "upload_staging" {
				return alias + ".user_id"
			}
			return "CASE WHEN json_valid(" + alias + ".value) THEN COALESCE(json_extract(" + alias + ".value,'$.owner'),'') ELSE '' END"
		}
		size := func(alias string) string {
			if table == "upload_staging" {
				return "length(CAST(" + alias + ".track_json AS BLOB))"
			}
			return "length(CAST(" + alias + ".value AS BLOB))"
		}
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			body := ""
			for _, alias := range []string{"OLD", "NEW"} {
				if event == "INSERT" && alias == "OLD" || event == "DELETE" && alias == "NEW" {
					continue
				}
				sign := ""
				if alias == "OLD" {
					sign = "-"
				}
				where := " WHERE 1"
				if table == "meta" {
					where = " WHERE " + alias + ".key LIKE 'upload_resume/%'"
				}
				for _, key := range []string{owner(alias), "'*'"} {
					body += "INSERT INTO browse_temp_metadata(owner,bytes) SELECT " + key + "," + sign + size(alias) + where + " ON CONFLICT(owner) DO UPDATE SET bytes=bytes+excluded.bytes;"
				}
			}
			if _, err := tx.Exec("CREATE TRIGGER browse_temp_" + table + "_" + event + " AFTER " + event + " ON " + table + " BEGIN " + body + " END"); err != nil {
				return err
			}
		}
	}
	rows, err := tx.Query("SELECT id,user_id,track_json FROM upload_staging WHERE json_valid(track_json)")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, owner, raw string
		if err = rows.Scan(&id, &owner, &raw); err != nil {
			rows.Close()
			return err
		}
		var record stagedRecord
		if err = json.Unmarshal([]byte(raw), &record); err == nil {
			err = writeStageStorage(tx, id, owner, record, false)
		}
		if err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return nil
}
