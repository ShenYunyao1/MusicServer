package music

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"strconv"
)

const maxAvatarBytes = 512 << 10

var errAvatarImage = errors.New("avatar must be a static 256x256 PNG or JPEG no larger than 512 KiB")

type avatarRecord struct {
	Version int64  `json:"version"`
	Data    []byte `json:"data,omitempty"`
}
type avatarInfo struct {
	Version   int64  `json:"version"`
	HasAvatar bool   `json:"has_avatar"`
	URL       string `json:"url"`
}

func (v avatarRecord) info() avatarInfo {
	result := avatarInfo{Version: v.Version, HasAvatar: len(v.Data) > 0}
	if result.HasAvatar {
		result.URL = "/api/v2/auth/avatar/content?v=" + strconv.FormatInt(v.Version, 10)
	}
	return result
}
func readAvatar(reader sqlReader, id string) (avatarRecord, error) {
	var value string
	err := reader.QueryRow("SELECT value FROM meta WHERE key=?", "user_avatar/"+id).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return avatarRecord{Version: 1}, nil
	}
	if err != nil {
		return avatarRecord{}, err
	}
	var record avatarRecord
	if err = json.Unmarshal([]byte(value), &record); err != nil {
		return record, err
	}
	if record.Version < 1 || len(record.Data) > maxAvatarBytes {
		return record, errors.New("invalid stored avatar")
	}
	if len(record.Data) > 0 {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(record.Data))
		if err != nil || format != "png" || cfg.Width != 256 || cfg.Height != 256 {
			return record, errors.New("invalid stored avatar image")
		}
		if _, err := png.Decode(bytes.NewReader(record.Data)); err != nil {
			return record, err
		}
	}
	return record, nil
}
func validateAvatar(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > maxAvatarBytes {
		return nil, errAvatarImage
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || cfg.Width != 256 || cfg.Height != 256 {
		return nil, errAvatarImage
	}
	if format == "png" {
		for pos := 8; pos+12 <= len(data); {
			size := int64(binary.BigEndian.Uint32(data[pos : pos+4]))
			if size > int64(len(data)-pos-12) {
				return nil, errAvatarImage
			}
			if string(data[pos+4:pos+8]) == "acTL" {
				return nil, errAvatarImage
			}
			pos += int(size) + 12
		}
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errAvatarImage
	}
	var out bytes.Buffer
	if err = png.Encode(&out, decoded); err != nil || out.Len() > maxAvatarBytes {
		return nil, errAvatarImage
	}
	return out.Bytes(), nil
}
func (s *Store) avatar(auth authContext) (avatarRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return avatarRecord{}, err
	}
	defer tx.Rollback()
	actor, err := tagActor(tx, auth)
	if err != nil {
		return avatarRecord{}, err
	}
	return readAvatar(tx, actor.ID)
}
func (s *Store) saveAvatar(auth authContext, data []byte, remove bool, expected int64) (avatarRecord, error) {
	var record avatarRecord
	var err error
	if !remove {
		data, err = validateAvatar(data)
		if err != nil {
			return record, err
		}
	}
	err = s.transaction(func(tx *sql.Tx) error {
		actor, e := tagActor(tx, auth)
		if e != nil {
			return e
		}
		record, e = readAvatar(tx, actor.ID)
		if e != nil {
			return e
		}
		if expected != record.Version {
			return errVersionConflict
		}
		record.Version++
		record.Data = data
		if remove {
			record.Data = nil
		}
		encoded, e := json.Marshal(record)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "user_avatar/"+actor.ID, string(encoded)); e != nil {
			return e
		}
		action := "avatar.update"
		if remove {
			action = "avatar.remove"
		}
		return auditMutation(tx, action, actor.ID, nil, map[string]any{"version": record.Version, "has_avatar": len(record.Data) > 0}, []MutationOptions{{UserID: actor.ID, Actor: actor.Username, ActorType: "session", CredentialID: auth.keyID}})
	})
	return record, err
}
