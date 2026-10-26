package music

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func writeRegistrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errRegistrationDisabled):
		writeError(w, 403, "REGISTRATION_CLOSED", err.Error())
	case errors.Is(err, errRegistrationMail):
		writeError(w, 409, "MAIL_NOT_READY", err.Error())
	case errors.Is(err, errRegistrationLimited):
		w.Header().Set("Retry-After", "3600")
		writeError(w, 429, "REGISTRATION_LIMITED", err.Error())
	default:
		writeUserError(w, err)
	}
}

func (a *API) registration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/api/v2/registration/config" && r.Method == "GET" {
		policy, err := a.store.RegistrationPolicy()
		if err != nil {
			writeError(w, 500, "REGISTRATION_FAILED", "registration unavailable")
			return
		}
		ready := a.store.MailReady()
		writeJSON(w, 200, map[string]bool{"enabled": policy.Enabled && policy.QuotaConfigured && ready, "mail_ready": ready})
		return
	}
	if r.URL.Path == "/api/v2/registration" && r.Method == "POST" {
		if !a.validOrigin(r) {
			writeError(w, 403, "ORIGIN_DENIED", "same-origin request required")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if decodeStrict(r, &input) != nil || len(input.Username) > 64 || len(input.Password) > 1024 {
			writeError(w, 400, "INVALID_REGISTRATION", "invalid registration request")
			return
		}
		if !a.userHashSlot(w) {
			return
		}
		defer func() { <-a.hashSlots }()
		ip := a.requestClientIP(r)
		if _, err := a.store.RegisterAccount(input.Username, input.Password, ip); err != nil {
			writeRegistrationError(w, err)
			return
		}
		writeJSON(w, 201, map[string]bool{"registered": true})
		return
	}
	if r.URL.Path == "/api/v2/admin/registration" {
		if !a.requireAdmin(w, r) {
			return
		}
		switch r.Method {
		case "GET":
			policy, err := a.store.RegistrationPolicy()
			if err != nil {
				writeError(w, 500, "REGISTRATION_FAILED", "registration policy unavailable")
				return
			}
			writeJSON(w, 200, struct {
				RegistrationPolicy
				MailReady bool `json:"mail_ready"`
			}{policy, a.store.MailReady()})
		case "PUT":
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			var input RegistrationPolicyInput
			if decodeStrict(r, &input) != nil {
				writeError(w, 400, "INVALID_POLICY", "invalid registration policy")
				return
			}
			if expected := r.Header.Get("If-Match"); expected != "" {
				version, err := strconv.ParseInt(strings.Trim(expected, `"`), 10, 64)
				if err != nil || version != input.Version {
					writeError(w, 400, "INVALID_POLICY", "If-Match differs from policy version")
					return
				}
			}
			policy, err := a.store.UpdateRegistrationPolicy(input, mutationOptions(r))
			if err != nil {
				writeRegistrationError(w, err)
				return
			}
			writeJSON(w, 200, struct {
				RegistrationPolicy
				MailReady bool `json:"mail_ready"`
			}{policy, a.store.MailReady()})
		default:
			writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		}
		return
	}
	writeError(w, 404, "NOT_FOUND", "registration endpoint not found")
}
