package music

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// The editor submits an already cropped PNG/JPEG, never an arbitrary asset ID.
func (s *Store) SavePlaylistEditor(id, name, description string, cover []byte, remove bool, options ...MutationOptions) (Playlist, error) {
	var result Playlist
	if strings.TrimSpace(name) == "" {
		return result, errors.New("playlist name is required")
	}
	mime := ""
	if cover != nil {
		if remove || len(cover) == 0 || len(cover) > maxEmbeddedCoverBytes {
			return result, errors.New("invalid playlist cover: maximum 8 MiB")
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(cover))
		if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Width != config.Height || config.Width > 1024 {
			return result, errors.New("playlist cover must be a square PNG/JPEG, at most 1024 pixels")
		}
		if format == "png" {
			for pos := 8; pos+12 <= len(cover); {
				length := uint64(binary.BigEndian.Uint32(cover[pos:]))
				if uint64(pos)+12+length > uint64(len(cover)) {
					return result, errors.New("damaged playlist cover")
				}
				if string(cover[pos+4:pos+8]) == "acTL" {
					return result, errors.New("animated playlist covers are not supported")
				}
				pos += 12 + int(length)
			}
		}
		if _, _, err := image.Decode(bytes.NewReader(cover)); err != nil {
			return result, errors.New("damaged playlist cover")
		}
		mime = "image/" + format
	}
	err := s.transaction(func(tx *sql.Tx) error {
		before, err := readPlaylistFrom(tx, id)
		if err != nil {
			return err
		}
		if before.System {
			return errLibraryMembers
		}
		if err := checkActorOwner(tx, before.OwnerID, options); err != nil {
			return err
		}
		if err := checkVersion(before.Version, options); err != nil {
			return err
		}
		result = clonePlaylist(before)
		result.Name = name
		result.Description = description
		if len(options) > 0 && options[0].PlaylistVisibility != nil {
			if err := setPlaylistVisibility(&result, *options[0].PlaylistVisibility); err != nil {
				return err
			}
		}
		if remove {
			result.CoverID = ""
			result.CoverMIME = ""
		} else if cover != nil {
			result.CoverID = hashBytes(cover)
			result.CoverMIME = mime
		}
		if reflect.DeepEqual(before, result) {
			return nil
		}
		if err := saveCover(tx, cover, mime); err != nil {
			return err
		}
		result.Version++
		result.UpdatedAt = time.Now().UTC()
		if err := writePlaylist(tx, result); err != nil {
			return err
		}
		if err := recordChange(tx, "playlist", id, false); err != nil {
			return err
		}
		return auditMutation(tx, "playlist.editor", id, before, result, options)
	})
	return result, err
}

func (a *API) playlistEditor(response http.ResponseWriter, request *http.Request, id string) {
	if request.Method != http.MethodPut {
		writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if !a.requireAccount(response, request) {
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxEmbeddedCoverBytes+(64<<10))
	if err := request.ParseMultipartForm(maxEmbeddedCoverBytes); err != nil {
		var tooLarge *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(response, status, "INVALID_PLAYLIST_FORM", "invalid editor form or image exceeds 8 MiB")
		return
	}
	defer request.MultipartForm.RemoveAll()
	var data []byte
	if files := request.MultipartForm.File["cover"]; len(files) > 0 {
		if len(files) != 1 || files[0].Size > maxEmbeddedCoverBytes {
			writeError(response, 413, "COVER_TOO_LARGE", "cover exceeds 8 MiB")
			return
		}
		file, err := files[0].Open()
		if err != nil {
			writeError(response, 400, "INVALID_COVER", "unable to read cover")
			return
		}
		defer file.Close()
		data, err = io.ReadAll(io.LimitReader(file, maxEmbeddedCoverBytes+1))
		if err != nil || len(data) > maxEmbeddedCoverBytes {
			writeError(response, 400, "INVALID_COVER", "unable to read cover")
			return
		}
	}
	options := mutationOptions(request)
	if values := request.MultipartForm.Value["visibility"]; len(values) > 0 {
		options.PlaylistVisibility = &values[0]
	}
	stored, err := a.store.SavePlaylistEditor(id, request.FormValue("name"), request.FormValue("description"), data, request.FormValue("remove_cover") == "true", options)
	if err != nil {
		writePlaylistMutationError(response, err)
		return
	}
	writeJSON(response, 200, stored)
}

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func saveCover(tx *sql.Tx, data []byte, mime string) error {
	if len(data) == 0 || mime == "" {
		return nil
	}
	_, err := tx.Exec("INSERT OR IGNORE INTO cover_assets VALUES(?,?,?)", hashBytes(data), mime, data)
	return err
}
func (s *Store) SetTrackCover(id string, data []byte, options ...MutationOptions) error {
	validated, mime, ok := validateCover(data, "")
	if !ok {
		return errors.New("invalid cover (maximum 8 MiB and 20 MP)")
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
		if err := saveCover(tx, validated, mime); err != nil {
			return err
		}
		track := before
		track.CoverID = hashBytes(validated)
		track.CoverMIME = mime
		track.CoverSource = "manual"
		track.Version++
		if err := writeTrack(tx, track); err != nil {
			return err
		}
		if err := writeOverrides(tx, id, map[string]string{"cover_id": track.CoverID, "cover_mime": mime, "cover_source": "manual"}); err != nil {
			return err
		}
		if err := recordChange(tx, "track", id, false); err != nil {
			return err
		}
		return auditMutation(tx, "track.cover", id, before, track, options)
	})
}
func (s *Store) Cover(id string) (CoverAsset, bool) {
	var asset CoverAsset
	asset.ID = id
	err := s.db.QueryRow("SELECT mime,data FROM cover_assets WHERE id=?", id).Scan(&asset.MIME, &asset.Data)
	return asset, err == nil
}
