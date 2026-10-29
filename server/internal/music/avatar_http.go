package music

import (
	"errors"
	"io"
	"net/http"
)

func writeAvatarError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTagSession):
		writeError(w, 401, "AUTH_INVALID", "current account session required")
	case errors.Is(err, errVersionConflict):
		writeError(w, 409, "VERSION_CONFLICT", "avatar changed; reload before saving")
	case errors.Is(err, errAvatarImage):
		writeError(w, 400, "INVALID_AVATAR", errAvatarImage.Error())
	default:
		writeError(w, 500, "AVATAR_FAILED", "avatar unavailable")
	}
}
func (a *API) avatarHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v2/auth/avatar/content" {
		if r.Method != "GET" {
			writeError(w, 405, "METHOD_NOT_ALLOWED", "GET required")
			return
		}
		for key := range r.URL.Query() {
			if key != "v" {
				writeError(w, 400, "INVALID_AVATAR", "unknown parameter")
				return
			}
		}
		record, err := a.store.avatar(authFromRequest(r))
		if err != nil {
			writeAvatarError(w, err)
			return
		}
		if len(record.Data) == 0 {
			writeError(w, 404, "NOT_FOUND", "avatar not set")
			return
		}
		etag := "\"" + hashBytes(record.Data) + "\""
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Set("ETag", etag)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "image/png")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(304)
			return
		}
		_, _ = w.Write(record.Data)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_AVATAR", "query parameters are not accepted")
		return
	}
	if r.Method == "GET" {
		record, err := a.store.avatar(authFromRequest(r))
		if err != nil {
			writeAvatarError(w, err)
			return
		}
		writeJSON(w, 200, record.info())
		return
	}
	if r.Method != "PUT" {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "GET or PUT required")
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
			writeError(w, 413, "AVATAR_TOO_LARGE", "avatar exceeds 512 KiB")
		} else {
			writeError(w, 400, "INVALID_AVATAR", "invalid avatar form")
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	for name := range r.MultipartForm.Value {
		if name != "remove" {
			writeError(w, 400, "INVALID_AVATAR", "unknown field")
			return
		}
	}
	for name := range r.MultipartForm.File {
		if name != "avatar" {
			writeError(w, 400, "INVALID_AVATAR", "unknown file")
			return
		}
	}
	values := r.MultipartForm.Value["remove"]
	if len(values) > 1 || len(values) == 1 && values[0] != "true" {
		writeError(w, 400, "INVALID_AVATAR", "invalid remove flag")
		return
	}
	remove := len(values) == 1
	files := r.MultipartForm.File["avatar"]
	if remove && len(files) > 0 || !remove && len(files) != 1 {
		writeError(w, 400, "INVALID_AVATAR", "choose one avatar or remove")
		return
	}
	var data []byte
	if !remove {
		if files[0].Size > maxAvatarBytes {
			writeError(w, 413, "AVATAR_TOO_LARGE", "avatar exceeds 512 KiB")
			return
		}
		file, err := files[0].Open()
		if err != nil {
			writeError(w, 400, "INVALID_AVATAR", "cannot read image")
			return
		}
		defer file.Close()
		data, err = io.ReadAll(io.LimitReader(file, maxAvatarBytes+1))
		if err != nil {
			writeAvatarError(w, err)
			return
		}
	}
	record, err := a.store.saveAvatar(authFromRequest(r), data, remove, expected)
	if err != nil {
		writeAvatarError(w, err)
		return
	}
	writeJSON(w, 200, record.info())
}
