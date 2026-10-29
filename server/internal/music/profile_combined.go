package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var errProfileInput = errors.New("invalid profile request")

func (s *Store) saveCombinedProfile(auth authContext, name string, data []byte, changeAvatar, remove bool, userVersion, avatarVersion int64) (User, avatarRecord, error) {
	var user User
	var avatar avatarRecord
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 128 {
		return user, avatar, errProfileInput
	}
	var err error
	if changeAvatar && !remove {
		data, err = validateAvatar(data)
		if err != nil {
			return user, avatar, err
		}
	}
	err = s.transaction(func(tx *sql.Tx) error {
		var e error
		user, e = tagActor(tx, auth)
		if e != nil {
			return e
		}
		before := user
		if userVersion != user.Version {
			return errVersionConflict
		}
		avatar, e = readAvatar(tx, user.ID)
		if e != nil {
			return e
		}
		if changeAvatar && avatarVersion != avatar.Version {
			return errVersionConflict
		}
		if name == user.DisplayName && !changeAvatar {
			return nil
		}
		user.DisplayName = name
		user.Version++
		if _, e = tx.Exec("UPDATE users SET display_name=?,version=?,updated_at=? WHERE id=?", name, user.Version, persistedTime(time.Now()), user.ID); e != nil {
			return e
		}
		if changeAvatar {
			avatar.Version++
			avatar.Data = data
			if remove {
				avatar.Data = nil
			}
			encoded, e := json.Marshal(avatar)
			if e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "user_avatar/"+user.ID, string(encoded)); e != nil {
				return e
			}
		}
		if name != before.DisplayName {
			if e = recordUserPlaylistChanges(tx, user.ID); e != nil {
				return e
			}
		}
		return auditMutation(tx, "account.profile", user.ID, before, map[string]any{"user": user, "avatar_version": avatar.Version, "has_avatar": len(avatar.Data) > 0}, []MutationOptions{{UserID: user.ID, Actor: user.Username, ActorType: "session", CredentialID: auth.keyID}})
	})
	return user, avatar, err
}

func (a *API) combinedProfileHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "PUT required")
		return
	}
	fail := func(code int, message string) { writeError(w, code, "INVALID_PROFILE", message) }
	if r.URL.RawQuery != "" {
		fail(400, "query parameters are not accepted")
		return
	}
	expected, ok := tagExpectedVersion(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBytes+65536)
	if err := r.ParseMultipartForm(maxAvatarBytes + 65536); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			fail(413, "profile image exceeds 512 KiB")
		} else {
			fail(400, "invalid profile form")
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	for k, v := range r.MultipartForm.Value {
		if (k != "display_name" && k != "avatar_version" && k != "remove_avatar") || len(v) != 1 {
			fail(400, "unknown or repeated field")
			return
		}
	}
	for k, v := range r.MultipartForm.File {
		if k != "avatar" || len(v) != 1 {
			fail(400, "unknown or repeated file")
			return
		}
	}
	if len(r.MultipartForm.Value["display_name"]) != 1 {
		fail(400, "display_name required")
		return
	}
	values := r.MultipartForm.Value["remove_avatar"]
	remove := len(values) == 1
	if remove && values[0] != "true" {
		fail(400, "invalid remove flag")
		return
	}
	files := r.MultipartForm.File["avatar"]
	if remove && len(files) > 0 {
		fail(400, "choose image or removal")
		return
	}
	change := remove || len(files) > 0
	var av int64
	if v := r.FormValue("avatar_version"); v != "" {
		var err error
		av, err = strconv.ParseInt(v, 10, 64)
		if err != nil || av < 1 {
			fail(400, "invalid avatar version")
			return
		}
	}
	if change && av < 1 {
		writeError(w, 428, "VERSION_REQUIRED", "avatar_version required when changing avatar")
		return
	}
	var data []byte
	if len(files) > 0 {
		if files[0].Size > maxAvatarBytes {
			fail(413, "profile image exceeds 512 KiB")
			return
		}
		file, err := files[0].Open()
		if err != nil {
			fail(400, "unable to read image")
			return
		}
		defer file.Close()
		data, err = io.ReadAll(io.LimitReader(file, maxAvatarBytes+1))
		if err != nil {
			fail(400, "unable to read image")
			return
		}
	}
	user, avatar, err := a.store.saveCombinedProfile(authFromRequest(r), r.FormValue("display_name"), data, change, remove, expected, av)
	if err != nil {
		switch {
		case errors.Is(err, errProfileInput):
			fail(400, "display name must contain 1–128 characters")
		case errors.Is(err, errAvatarImage):
			writeAvatarError(w, err)
		case errors.Is(err, errTagSession):
			writeAvatarError(w, err)
		case errors.Is(err, errVersionConflict):
			writeAvatarError(w, err)
		default:
			writeError(w, 500, "PROFILE_FAILED", "unable to save profile")
		}
		return
	}
	writeJSON(w, 200, map[string]any{"user": user, "csrf_token": authFromRequest(r).csrf, "avatar": avatar.info()})
}
