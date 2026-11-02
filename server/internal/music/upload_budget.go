package music

import (
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var errUploadTemporaryLimit = errors.New("上传临时空间已达上限，请完成或取消待处理任务后重试")

type temporaryUploadLimits struct {
	OwnerBytes, GlobalBytes       int64
	OwnerMetadata, GlobalMetadata int64
	OwnerTasks, GlobalTasks       int
}

func defaultTemporaryUploadLimits() temporaryUploadLimits {
	return temporaryUploadLimits{2 << 30, 16 << 30, 64 << 20, 512 << 20, 16, 256}
}

func (s *Store) temporaryLimits() temporaryUploadLimits {
	if s.tempLimits != nil {
		return *s.tempLimits
	}
	return defaultTemporaryUploadLimits()
}

type temporaryUploadReservation struct {
	owner, id string
	size      int64
}

// Admission uses a separate lock, never the database write lock. Existing files
// (including failed cleanup), full pending resume sizes and in-flight writes all
// count. No byte budget is released merely by marking a checkpoint canceled.
func (s *Store) reserveTemporaryUpload(owner, id string, size int64) (func(), error) {
	s.tempMu.Lock()
	defer s.tempMu.Unlock()
	limits := s.temporaryLimits()
	if s.tempLimits == nil {
		var quota StorageQuota
		if err := readStorageJSON(s.db, "storage_quota/"+owner, &quota); err != nil {
			return nil, err
		}
		// Allow two file names during resume->stage promotion and a full-size
		// duplicate probe even when committed audio has used all account quota.
		fileAllowance := 2 * s.Settings().MaxUploadBytes
		if quota.LimitBytes > 0 {
			limits.OwnerBytes = min(limits.OwnerBytes, max(fileAllowance, quota.LimitBytes))
		}
		limits.OwnerBytes = max(limits.OwnerBytes, fileAllowance)
	}
	global, err := directoryBytes(filepath.Join(s.mediaRoot, ".staging"))
	if errors.Is(err, os.ErrNotExist) {
		global, err = 0, nil
	}
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var own int64
	identities := map[string]string{}
	rows, err := s.db.Query("SELECT id,user_id,size FROM upload_staging")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, user string
		var bytes int64
		if err = rows.Scan(&key, &user, &bytes); err != nil {
			rows.Close()
			return nil, err
		}
		identities[key] = user
		if user == owner {
			own += bytes
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.db.Query(`SELECT substr(key,15),json_extract(value,'$.owner'),json_extract(value,'$.size'),json_extract(value,'$.state'),json_extract(value,'$.expires_at') FROM meta WHERE key LIKE 'upload_resume/%'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, user, state, expires string
		var bytes int64
		if err = rows.Scan(&key, &user, &bytes, &state, &expires); err != nil {
			rows.Close()
			return nil, err
		}
		if !validResumeID(key) || bytes < 0 {
			rows.Close()
			return nil, errResumeCorrupt
		}
		physical := int64(0)
		info, e := root.Lstat(resumeFilePath(key))
		if e == nil {
			if !info.Mode().IsRegular() {
				rows.Close()
				return nil, errResumeCorrupt
			}
			physical = info.Size()
		} else if !errors.Is(e, os.ErrNotExist) {
			rows.Close()
			return nil, e
		}
		when, e := parsePersistedTime(expires)
		pending := state == "uploading" && e == nil && time.Now().Before(when)
		charge := physical
		if pending {
			charge = max(charge, bytes)
			global += max(int64(0), bytes-physical)
		}
		if pending || physical > 0 {
			identities[key] = user
		}
		if user == owner {
			own += charge
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for reservation := range s.tempReservations {
		global += reservation.size
		if reservation.owner == owner {
			own += reservation.size
		}
		identities[reservation.id] = reservation.owner
	}
	identities[id] = owner
	count := 0
	for _, user := range identities {
		if user == owner {
			count++
		}
	}
	if size < 0 || size > limits.OwnerBytes || own > limits.OwnerBytes-size || size > limits.GlobalBytes || global > limits.GlobalBytes-size || count > limits.OwnerTasks || len(identities) > limits.GlobalTasks {
		return nil, errUploadTemporaryLimit
	}
	reservation := &temporaryUploadReservation{owner, id, size}
	if s.tempReservations == nil {
		s.tempReservations = map[*temporaryUploadReservation]bool{}
	}
	s.tempReservations[reservation] = true
	var once sync.Once
	return func() {
		once.Do(func() { s.tempMu.Lock(); delete(s.tempReservations, reservation); s.tempMu.Unlock() })
	}, nil
}

// A seekable multipart/file reader gives an exact bound before writing. A
// streaming reader reserves the configured maximum and is capped to that size.
func temporaryReaderBound(reader io.Reader, maximum int64) int64 {
	if seeker, ok := reader.(io.Seeker); ok {
		position, err := seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return maximum
		}
		end, err := seeker.Seek(0, io.SeekEnd)
		_, restore := seeker.Seek(position, io.SeekStart)
		if err == nil && restore == nil && end >= position {
			return min(maximum, end-position)
		}
	}
	return maximum
}

// Called inside the same transaction as the insert/update, so parallel cover,
// staging and checkpoint writes cannot all pass against an old byte total.
func checkTemporaryMetadata(reader sqlReader, owner, stageID, resumeID string, bytes int64, limits temporaryUploadLimits) error {
	var old int64
	if stageID != "" {
		err := reader.QueryRow("SELECT length(CAST(track_json AS BLOB)) FROM upload_staging WHERE id=? AND user_id=?", stageID, owner).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if resumeID != "" {
		err := reader.QueryRow("SELECT length(CAST(value AS BLOB)) FROM meta WHERE key=?", resumableMetaPrefix+resumeID).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	// Keep cleanup available for data created before these new limits.
	if old > 0 && bytes <= old {
		return nil
	}
	var total, own int64
	err := reader.QueryRow(`SELECT COALESCE(sum(bytes),0),COALESCE(sum(CASE WHEN owner=? THEN bytes ELSE 0 END),0) FROM (
 SELECT length(CAST(track_json AS BLOB)) bytes,user_id owner FROM upload_staging WHERE id!=?
 UNION ALL SELECT length(CAST(value AS BLOB)),json_extract(value,'$.owner') FROM meta WHERE key LIKE 'upload_resume/%' AND key!=?)`, owner, stageID, resumableMetaPrefix+resumeID).Scan(&total, &own)
	if err != nil {
		return err
	}
	if bytes < 0 || bytes > 32<<20 || bytes > limits.GlobalMetadata || total > limits.GlobalMetadata-bytes || bytes > limits.OwnerMetadata || own > limits.OwnerMetadata-bytes {
		return errUploadTemporaryLimit
	}
	return nil
}
