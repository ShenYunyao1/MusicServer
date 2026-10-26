package music

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func writeMailError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errResourceHidden):
		writeError(w, 401, "AUTH_INVALID", "登录已失效，请重新登录")
	case errors.Is(err, errMailLimited):
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "MAIL_LIMITED", err.Error())
	case errors.Is(err, errVersionConflict):
		writeError(w, 409, "VERSION_CONFLICT", err.Error())
	case errors.Is(err, errMailAddressTaken):
		writeError(w, 409, "EMAIL_UNAVAILABLE", "该邮箱暂不可绑定")
	case errors.Is(err, errCredentials):
		writeError(w, 400, "PASSWORD_INVALID", "当前密码不正确")
	case errors.Is(err, errMailUnavailable):
		writeError(w, 503, "MAIL_UNAVAILABLE", err.Error())
	case errors.Is(err, errMailInvalid):
		writeError(w, 400, "EMAIL_CODE_INVALID", err.Error())
	default:
		writeError(w, 400, "MAIL_INVALID", "邮件请求或配置无效")
	}
}
func (a *API) emailHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.requireAccount(w, r) {
		return
	}
	auth := authFromRequest(r)
	path := r.URL.Path
	admin := strings.HasPrefix(path, "/api/v2/admin/")
	if admin && !a.requireAdmin(w, r) {
		return
	}
	if err := validateAccountSession(a.store.db, auth); err != nil {
		writeMailError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if path == "/api/v2/auth/email" && r.Method == "GET" {
		state, err := a.store.EmailStatus(auth)
		if err != nil {
			writeMailError(w, err)
			return
		}
		writeJSON(w, 200, state)
		return
	}
	if path == "/api/v2/auth/email/challenge" && r.Method == "POST" {
		var input struct {
			Email           string `json:"email"`
			CurrentPassword string `json:"current_password"`
		}
		if decodeStrict(r, &input) != nil {
			writeError(w, 400, "MAIL_INVALID", "请输入邮箱和当前密码（换绑时）")
			return
		}
		if !a.userHashSlot(w) {
			return
		}
		defer func() { <-a.hashSlots }()
		ip := a.requestClientIP(r)
		state, err := a.store.RequestEmailChallenge(auth, input.Email, input.CurrentPassword, ip)
		if err != nil {
			writeMailError(w, err)
			return
		}
		writeJSON(w, 202, state)
		return
	}
	if path == "/api/v2/auth/email/verify" && r.Method == "POST" {
		var input struct {
			ChallengeID string `json:"challenge_id"`
			Code        string `json:"code"`
		}
		if decodeStrict(r, &input) != nil {
			writeError(w, 400, "MAIL_INVALID", "验证码请求无效")
			return
		}
		state, err := a.store.VerifyEmailChallenge(auth, input.ChallengeID, input.Code)
		if err != nil {
			writeMailError(w, err)
			return
		}
		writeJSON(w, 200, state)
		return
	}
	if path == "/api/v2/admin/mail" {
		if r.Method == "GET" {
			settings, err := a.store.MailSettings()
			if err != nil {
				writeMailError(w, err)
				return
			}
			writeJSON(w, 200, settings)
			return
		}
		if r.Method == "PUT" {
			var input MailSettingsInput
			if decodeStrict(r, &input) != nil {
				writeError(w, 400, "MAIL_INVALID", "邮件服务配置无效")
				return
			}
			settings, err := a.store.UpdateMailSettings(input, mutationOptions(r))
			if err != nil {
				writeMailError(w, err)
				return
			}
			writeJSON(w, 200, settings)
			return
		}
	}
	if path == "/api/v2/admin/mail/test" && r.Method == "POST" {
		var input struct {
			Email string `json:"email"`
		}
		if decodeStrict(r, &input) != nil {
			writeError(w, 400, "MAIL_INVALID", "测试邮箱无效")
			return
		}
		job, err := a.store.QueueMailTest(auth, input.Email)
		if err != nil {
			writeMailError(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"job_id": job.ID, "state": job.State})
		return
	}
	if strings.HasPrefix(path, "/api/v2/admin/users/") && strings.HasSuffix(path, "/release-email") && r.Method == "POST" {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v2/admin/users/"), "/release-email")
		if strings.Contains(id, "/") || id == "" {
			writeError(w, 404, "NOT_FOUND", "account not found")
			return
		}
		expected, err := strconv.ParseInt(strings.Trim(r.Header.Get("If-Match"), "\""), 10, 64)
		if err != nil || expected < 1 {
			writeError(w, 428, "VERSION_REQUIRED", "If-Match is required")
			return
		}
		if err = a.store.ReleaseDeletedEmail(auth, id, expected); err != nil {
			writeMailError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
}
