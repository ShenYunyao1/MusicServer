package music

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var errMailUnavailable = errors.New("邮件服务尚未配置或秘密配置不可用，请联系管理员")
var errMailLimited = errors.New("发送或验证过于频繁，请稍后重试")
var errMailInvalid = errors.New("验证码无效、已使用或已过期")
var errMailAddressTaken = errors.New("该邮箱已被其他账户绑定")

type MailSettings struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TLSMode     string `json:"tls_mode"`
	FromAddress string `json:"from_address"`
	Username    string `json:"username"`
	Version     int64  `json:"version"`
	Configured  bool   `json:"configured"`
	Tested      bool   `json:"tested"`
}
type mailSettingsRecord struct {
	MailSettings
	SecretID string `json:"secret_id"`
}
type MailSettingsInput struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TLSMode     string `json:"tls_mode"`
	FromAddress string `json:"from_address"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Version     int64  `json:"version"`
}
type mailSecret struct {
	Password string `json:"password"`
}

var mailLocalPattern = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+/=?^_`{|}~.-]+$")
var mailDomainLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func normalizeMailAddress(input string) (string, error) {
	if strings.ContainsAny(input, "\r\n\x00") {
		return "", errors.New("请输入有效的邮箱地址")
	}
	input = strings.TrimSpace(input)
	if len(input) > 254 || strings.ContainsAny(input, "\r\n\x00") || strings.Count(input, "@") != 1 {
		return "", errors.New("请输入有效的邮箱地址")
	}
	local, domain, _ := strings.Cut(input, "@")
	parsed, err := mail.ParseAddress(input)
	if err != nil || parsed.Name != "" || parsed.Address != input || len(local) > 64 || !mailLocalPattern.MatchString(local) || strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return "", errors.New("请输入有效的邮箱地址")
	}
	domain = strings.ToLower(domain)
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", errors.New("邮箱域名无效")
	}
	for _, label := range labels {
		if !mailDomainLabel.MatchString(label) {
			return "", errors.New("邮箱域名无效")
		}
	}
	return local + "@" + domain, nil
}
func mailReadJSON(reader sqlReader, key string, target any) error {
	var raw string
	err := reader.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&raw)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), target)
}
func mailWriteJSON(tx *sql.Tx, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(raw))
	return err
}
func readMailSettings(reader sqlReader) (mailSettingsRecord, error) {
	var record mailSettingsRecord
	var raw string
	err := reader.QueryRow("SELECT value FROM settings WHERE key='mail_settings'").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	err = json.Unmarshal([]byte(raw), &record)
	return record, err
}
func (s *Store) mailSecretsDirectory() string {
	return filepath.Join(filepath.Dir(s.dataPath), ".mail-secrets")
}
func (s *Store) readMailSecret(name string) ([]byte, error) {
	directory := s.mailSecretsDirectory()
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errMailUnavailable
	}
	path := filepath.Join(directory, name)
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 16384 {
		return nil, errMailUnavailable
	}
	if ensureMailSecretDirectory(directory) != nil || protectMailSecretFile(path) != nil {
		return nil, errMailUnavailable
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errMailUnavailable
	}
	return raw, nil
}

// Callers serialize secret creation/configuration with mail.mu. Immutable
// credential files are referenced by the same committed settings version.
func (s *Store) writeNewMailSecret(name string, raw []byte) error {
	directory := s.mailSecretsDirectory()
	if err := ensureMailSecretDirectory(directory); err != nil {
		return errMailUnavailable
	}
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errMailUnavailable
	}
	if err = protectMailSecretFile(path); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errMailUnavailable
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(filepath.Join(directory, name))
		return errMailUnavailable
	}
	return nil
}
func (s *Store) mailMasterKey(create bool) ([]byte, error) {
	raw, err := s.readMailSecret("master.key")
	if err == nil {
		key, decodeErr := base64.StdEncoding.DecodeString(string(raw))
		if decodeErr != nil || len(key) != 32 {
			return nil, errMailUnavailable
		}
		return key, nil
	}
	if !create {
		return nil, errMailUnavailable
	}
	// Do not replace an unreadable/corrupt existing key, or queued payloads would
	// silently become unrecoverable. Creation is allowed only when truly absent.
	if _, statErr := os.Lstat(filepath.Join(s.mailSecretsDirectory(), "master.key")); !errors.Is(statErr, os.ErrNotExist) {
		return nil, errMailUnavailable
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	if err = s.writeNewMailSecret("master.key", []byte(base64.StdEncoding.EncodeToString(key))); err != nil {
		return nil, err
	}
	return key, nil
}
func mailDerivedKey(master []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte("musicserver/mail/" + purpose + "/v1"))
	return mac.Sum(nil)
}
func (s *Store) encryptMailPayload(raw []byte) (string, error) {
	master, err := s.mailMasterKey(false)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(mailDerivedKey(master, "outbox"))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, raw, []byte("mail_job/v1"))), nil
}
func (s *Store) decryptMailPayload(value string) ([]byte, error) {
	master, err := s.mailMasterKey(false)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, errMailUnavailable
	}
	block, err := aes.NewCipher(mailDerivedKey(master, "outbox"))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return nil, errMailUnavailable
	}
	decoded, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte("mail_job/v1"))
	if err != nil {
		return nil, errMailUnavailable
	}
	return decoded, nil
}
func (s *Store) mailCredential(record mailSettingsRecord) (mailSecret, error) {
	var secret mailSecret
	if !validResumeID(record.SecretID) {
		return secret, errMailUnavailable
	}
	raw, err := s.readMailSecret(record.SecretID + ".json")
	if err != nil {
		return secret, err
	}
	if json.Unmarshal(raw, &secret) != nil || secret.Password == "" {
		return secret, errMailUnavailable
	}
	if _, err = s.mailMasterKey(false); err != nil {
		return secret, err
	}
	return secret, nil
}
func (s *Store) MailSettings() (MailSettings, error) {
	record, err := readMailSettings(s.db)
	if err != nil {
		return MailSettings{}, err
	}
	if _, err = s.mailCredential(record); err != nil {
		record.Configured = false
		record.Tested = false
	}
	return record.MailSettings, nil
}
func (s *Store) mailReadyFrom(reader sqlReader) bool {
	record, err := readMailSettings(reader)
	if err != nil || !record.Configured || !record.Tested {
		return false
	}
	_, err = s.mailCredential(record)
	return err == nil
}
func (s *Store) MailReady() bool { return s.mailReadyFrom(s.db) }
func (s *Store) UpdateMailSettings(input MailSettingsInput, options MutationOptions) (MailSettings, error) {
	host := strings.TrimSpace(input.Host)
	from, err := normalizeMailAddress(input.FromAddress)
	if err != nil {
		return MailSettings{}, err
	}
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "\r\n\x00/:@ ") || input.Port < 1 || input.Port > 65535 || input.TLSMode != "tls" && input.TLSMode != "starttls" || strings.TrimSpace(input.Username) == "" || len(input.Username) > 254 || strings.ContainsAny(input.Username, "\r\n\x00") || len(input.Password) > 4096 {
		return MailSettings{}, errors.New("邮件服务配置无效")
	}
	// Hostnames and numeric IPs are allowed. TLS always verifies the configured host.
	if net.ParseIP(host) == nil {
		for _, label := range strings.Split(host, ".") {
			if !mailDomainLabel.MatchString(label) {
				return MailSettings{}, errors.New("SMTP 主机无效")
			}
		}
	}
	s.mail.mu.Lock()
	defer s.mail.mu.Unlock()
	var result MailSettings
	var createdPath string
	err = s.transaction(func(tx *sql.Tx) error {
		if err := requireAdminActor(tx, []MutationOptions{options}); err != nil {
			return err
		}
		if options.ActorType == "session" {
			user, err := readUser(tx, options.UserID)
			if err != nil {
				return err
			}
			if err = validateAccountSession(tx, authContext{session: true, userID: user.ID, keyID: options.CredentialID, authVersion: user.AuthVersion}); err != nil {
				return err
			}
		}
		current, err := readMailSettings(tx)
		if err != nil {
			return err
		}
		if current.Version != input.Version {
			return errVersionConflict
		}
		secretID := current.SecretID
		if input.Password != "" {
			secretID, err = randomToken(16)
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(mailSecret{input.Password})
			if err = s.writeNewMailSecret(secretID+".json", raw); err != nil {
				return err
			}
			createdPath = filepath.Join(s.mailSecretsDirectory(), secretID+".json")
		} else if _, err = s.mailCredential(current); err != nil {
			return errMailUnavailable
		}
		if _, err = s.mailMasterKey(true); err != nil {
			return err
		}
		result = MailSettings{Host: host, Port: input.Port, TLSMode: input.TLSMode, FromAddress: from, Username: strings.TrimSpace(input.Username), Version: current.Version + 1, Configured: true, Tested: false}
		next := mailSettingsRecord{MailSettings: result, SecretID: secretID}
		raw, _ := json.Marshal(next)
		if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES('mail_settings',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(raw)); err != nil {
			return err
		}
		return auditMutation(tx, "mail.settings", "mail", map[string]any{"version": current.Version}, map[string]any{"version": result.Version, "configured": true}, []MutationOptions{options})
	})
	if err != nil && createdPath != "" {
		_ = os.Remove(createdPath)
	}
	return result, err
}
func (s *Store) markMailTested(version int64) error {
	return s.transaction(func(tx *sql.Tx) error {
		record, err := readMailSettings(tx)
		if err != nil {
			return err
		}
		if record.Version != version {
			return errVersionConflict
		}
		record.Tested = true
		raw, _ := json.Marshal(record)
		_, err = tx.Exec("UPDATE settings SET value=? WHERE key='mail_settings'", string(raw))
		return err
	})
}

const mailChallengeTTL = 10 * time.Minute
