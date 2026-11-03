package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
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

func temporaryPendingBytes(reader sqlReader, owner string) (int64, error) {
	var total int64
	err := reader.QueryRow(`SELECT COALESCE(sum(size),0) FROM (SELECT id,MAX(size) size FROM (
 SELECT id,size FROM upload_staging WHERE user_id=?
 UNION ALL SELECT substr(key,15),CASE WHEN json_extract(value,'$.state')='uploading' THEN COALESCE(json_extract(value,'$.size'),0) ELSE COALESCE(json_extract(value,'$.offset'),0) END FROM meta WHERE key LIKE 'upload_resume/%' AND json_valid(value) AND json_extract(value,'$.owner')=? AND json_extract(value,'$.state') NOT IN ('ready','canceled')) GROUP BY id)`, owner, owner).Scan(&total)
	return total, err
}

// Admission uses a separate lock, never the database write lock. Existing files
// (including failed cleanup), full pending resume sizes and in-flight writes all
// count. No byte budget is released merely by marking a checkpoint canceled.
func (s *Store) reserveTemporaryUpload(owner, id string, size int64) (func(), error) {
	s.tempMu.Lock()
	defer s.tempMu.Unlock()
	limits := s.temporaryLimits()
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
	charges := map[string]int64{}
	identities := map[string]string{}
	tx, err := s.readDatabase().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var quota StorageQuota
	if err := readStorageJSON(tx, "storage_quota/"+owner, &quota); err != nil {
		return nil, err
	}
	rows, err := tx.Query("SELECT id,user_id,size FROM upload_staging")
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
		charges[key] = bytes
		if user == owner {
			own += bytes
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.Query(`SELECT substr(key,15),value FROM meta WHERE key LIKE 'upload_resume/%'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var checkpoint resumableCheckpoint
		if !validResumeID(key) || json.Unmarshal([]byte(raw), &checkpoint) != nil || checkpoint.Owner == "" || checkpoint.Size <= 0 || checkpoint.Size > 1<<40 {
			s.badResumeOnce.Do(func() {
				log.Printf("upload checkpoint metadata ignored during admission; inspect upload_resume records (physical files remain charged)")
			})
			continue
		}
		user, state, bytes := checkpoint.Owner, checkpoint.State, checkpoint.Size
		physical := int64(0)
		info, e := root.Lstat(resumeFilePath(key))
		if e == nil {
			if !info.Mode().IsRegular() {
				continue
			}
			physical = info.Size()
		} else if !errors.Is(e, os.ErrNotExist) {
			rows.Close()
			return nil, e
		}
		pending := state == "uploading" && time.Now().Before(checkpoint.ExpiresAt)
		charge := physical
		if pending {
			charge = max(charge, bytes)
			global += max(int64(0), bytes-physical)
		}
		if pending || physical > 0 {
			identities[key] = user
		}
		if user == owner {
			own += max(int64(0), charge-charges[key])
		}
		charges[key] = max(charges[key], charge)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	committed := int64(0)
	if quota.LimitBytes > 0 {
		used, e := s.accountUsageForIDs(tx, []string{owner})
		if e != nil {
			return nil, e
		}
		committed = used[owner]
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for reservation := range s.tempReservations {
		global += reservation.size
		if reservation.owner == owner {
			own += reservation.size
		}
		identities[reservation.id] = reservation.owner
		charges[reservation.id] += reservation.size
	}
	if prior, ok := identities[id]; ok && prior != owner {
		return nil, errResourceHidden
	}
	// A resume reserves its whole logical payload at creation. Promotion to a
	// staged name consumes that reservation, rather than charging it twice.
	if size < 0 {
		return nil, errUploadTemporaryLimit
	}
	size = max(int64(0), size-charges[id])
	identities[id] = owner
	count := 0
	for _, user := range identities {
		if user == owner {
			count++
		}
	}
	if quota.LimitBytes > 0 && (size > quota.LimitBytes || own > quota.LimitBytes-size || committed > quota.LimitBytes-size-own) {
		return nil, errStorageQuota
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
 UNION ALL SELECT length(CAST(value AS BLOB)),CASE WHEN json_valid(value) THEN json_extract(value,'$.owner') ELSE '' END FROM meta WHERE key LIKE 'upload_resume/%' AND key!=?)`, owner, stageID, resumableMetaPrefix+resumeID).Scan(&total, &own)
	if err != nil {
		return err
	}
	if bytes < 0 || bytes > 32<<20 || bytes > limits.GlobalMetadata || total > limits.GlobalMetadata-bytes || bytes > limits.OwnerMetadata || own > limits.OwnerMetadata-bytes {
		return errUploadTemporaryLimit
	}
	return nil
}
