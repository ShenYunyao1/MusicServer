package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const registrationPolicyKey = "registration_policy"

var errRegistrationDisabled = errors.New("当前未开放注册")
var errRegistrationMail = errors.New("邮件服务尚未配置并测试成功，暂时无法注册")
var errRegistrationLimited = errors.New("注册请求过于频繁，请稍后再试")

type RegistrationPolicy struct {
	Enabled           bool  `json:"enabled"`
	DefaultQuotaBytes int64 `json:"default_quota_bytes"`
	QuotaConfigured   bool  `json:"quota_configured"`
	Version           int64 `json:"version"`
}

type RegistrationPolicyInput struct {
	Enabled           *bool  `json:"enabled"`
	DefaultQuotaBytes *int64 `json:"default_quota_bytes"`
	QuotaConfigured   *bool  `json:"quota_configured"`
	Version           int64  `json:"version"`
}

func readRegistrationPolicy(reader sqlReader) (RegistrationPolicy, error) {
	var policy RegistrationPolicy
	err := readStorageJSON(reader, registrationPolicyKey, &policy)
	return policy, err
}

func (s *Store) RegistrationPolicy() (RegistrationPolicy, error) { return readRegistrationPolicy(s.db) }

func (s *Store) UpdateRegistrationPolicy(input RegistrationPolicyInput, options MutationOptions) (RegistrationPolicy, error) {
	var policy RegistrationPolicy
	if input.Version < 0 {
		return policy, errors.New("invalid registration policy version")
	}
	if input.DefaultQuotaBytes != nil {
		if err := validateQuotaBytes(*input.DefaultQuotaBytes); err != nil {
			return policy, err
		}
	}
	err := s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
			return err
		}
		if err := validateAccountMutationSession(tx, options); err != nil {
			return err
		}
		before, err := readRegistrationPolicy(tx)
		if err != nil {
			return err
		}
		if before.Version != input.Version {
			return errVersionConflict
		}
		policy = before
		if input.Enabled != nil {
			policy.Enabled = *input.Enabled
		}
		if input.DefaultQuotaBytes != nil {
			policy.DefaultQuotaBytes = *input.DefaultQuotaBytes
			policy.QuotaConfigured = true
		}
		if input.QuotaConfigured != nil {
			if *input.QuotaConfigured && !policy.QuotaConfigured {
				return errors.New("请先明确设置新注册默认额度")
			}
			policy.QuotaConfigured = *input.QuotaConfigured
		}
		if policy.Enabled {
			if !policy.QuotaConfigured {
				return errors.New("请先明确设置新注册默认额度")
			}
			if !s.mailReadyFrom(tx) {
				return errRegistrationMail
			}
		}
		policy.Version++
		raw, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", registrationPolicyKey, string(raw)); err != nil {
			return err
		}
		return auditMutation(tx, "registration.policy", registrationPolicyKey, before, policy, []MutationOptions{options})
	})
	return policy, err
}

type registrationCounter struct {
	Count       int    `json:"count"`
	WindowStart string `json:"window_start"`
}

// Limits are stored independently from login_limits, whose one-minute cleanup
// must not reset the one-hour registration budget. The global cap also bounds
// how many distinct client-IP buckets can be created in one window.
func (s *Store) reserveRegistration(ip string) error {
	return s.transaction(func(tx *sql.Tx) error {
		policy, err := readRegistrationPolicy(tx)
		if err != nil {
			return err
		}
		if !policy.Enabled || !policy.QuotaConfigured {
			return errRegistrationDisabled
		}
		if !s.mailReadyFrom(tx) {
			return errRegistrationMail
		}
		now := time.Now().UTC()
		cutoff := persistedTime(now.Add(-time.Hour))
		if _, err := tx.Exec("DELETE FROM meta WHERE key LIKE 'registration_limit/%' AND json_extract(value,'$.window_start')<=?", cutoff); err != nil {
			return err
		}
		buckets := []struct {
			key   string
			limit int
		}{{"registration_limit/global", 100}, {"registration_limit/ip/" + hashSecret(strings.TrimSpace(ip)), 5}}
		values := make([]registrationCounter, len(buckets))
		for index, bucket := range buckets {
			var raw string
			err := tx.QueryRow("SELECT value FROM meta WHERE key=?", bucket.key).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			counter := registrationCounter{WindowStart: persistedTime(now)}
			if err == nil {
				if err := json.Unmarshal([]byte(raw), &counter); err != nil {
					return err
				}
			}
			if counter.Count >= bucket.limit {
				return errRegistrationLimited
			}
			counter.Count++
			values[index] = counter
		}
		for index, bucket := range buckets {
			raw, _ := json.Marshal(values[index])
			if _, err := tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", bucket.key, string(raw)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) RegisterAccount(username, password, ip string) (User, error) {
	if err := s.reserveRegistration(ip); err != nil {
		return User{}, err
	}
	return s.createUserRecordInitialized(username, password, "member", "", false, false, nil, func(tx *sql.Tx, user *User) error {
		policy, err := readRegistrationPolicy(tx)
		if err != nil {
			return err
		}
		if !policy.Enabled || !policy.QuotaConfigured {
			return errRegistrationDisabled
		}
		if !s.mailReadyFrom(tx) {
			return errRegistrationMail
		}
		state := AccountEmailState{Required: true, QuotaSource: "registration", RegistrationQuotaBytes: policy.DefaultQuotaBytes, RegistrationPolicyVersion: policy.Version}
		if err := writeAccountEmail(tx, user.ID, state); err != nil {
			return err
		}
		return writeNewAccountQuota(tx, user.ID, policy.DefaultQuotaBytes)
	})
}
