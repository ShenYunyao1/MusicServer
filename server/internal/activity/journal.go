// Package activity stores a bounded operational journal. Only predefined event
// messages and typed metadata are accepted; request bodies and credentials are
// deliberately absent from the logging interface.
package activity

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Fields struct {
	Reason     string           `json:"reason,omitempty"`
	RequestID  string           `json:"request_id,omitempty"`
	UserID     string           `json:"user_id,omitempty"`
	Method     string           `json:"method,omitempty"`
	Route      string           `json:"route,omitempty"`
	Status     int              `json:"status,omitempty"`
	DurationMS int64            `json:"duration_ms,omitempty"`
	Bytes      int64            `json:"bytes,omitempty"`
	Timings    map[string]int64 `json:"timings,omitempty"`
}
type Entry struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Level    string    `json:"level"`
	Category string    `json:"category"`
	Event    string    `json:"event"`
	Message  string    `json:"message"`
	Fields
}
type Query struct {
	Limit                                  int
	Before, After, Level, Category, Search string
}
type Page struct {
	Warning   string  `json:"warning,omitempty"`
	Entries   []Entry `json:"entries"`
	Newest    string  `json:"newest_cursor"`
	Oldest    string  `json:"oldest_cursor"`
	HasMore   bool    `json:"has_more"`
	Truncated bool    `json:"truncated"`
	Retention string  `json:"retention"`
}
type eventInfo struct{ category, level, message string }

var events = map[string]eventInfo{
	"mail.queued": {"mail", "info", "验证邮件已排队"}, "mail.submitted": {"mail", "info", "邮件已提交投递服务"}, "mail.failed": {"mail", "warn", "邮件投递失败"},
	"mail.verified": {"mail", "info", "账户邮箱验证完成"}, "mail.tested": {"mail", "info", "测试邮件已提交投递服务"},
	"server.start": {"server", "info", "服务器启动"}, "server.stop": {"server", "info", "服务器停止"},
	"server.cleanup_failed": {"server", "error", "清理过期暂存失败"},
	"server.backup_started": {"server", "info", "开始创建备份"}, "server.backup_completed": {"server", "info", "备份创建完成"}, "server.backup_failed": {"server", "error", "备份失败或取消"},
	"request.completed": {"http", "info", "请求处理完成"}, "request.slow": {"http", "warn", "请求处理时间较长"}, "request.failed": {"http", "error", "请求处理失败"},
	"request.denied":   {"http", "warn", "请求被拒绝"},
	"upload.receiving": {"upload", "info", "开始接收上传文件"}, "upload.received": {"upload", "info", "上传文件已接收，开始校验"},
	"upload.probing": {"upload", "info", "开始音频流校验"}, "upload.ready": {"upload", "info", "校验完成，等待确认入库"},
	"upload.failed": {"upload", "error", "上传或校验失败"}, "upload.committed": {"upload", "info", "上传曲目确认入库"},
	"scan.started": {"scan", "info", "开始曲库扫描"}, "scan.completed": {"scan", "info", "曲库扫描完成"}, "scan.failed": {"scan", "error", "曲库扫描失败或取消"},
	"update.checked": {"update", "info", "已请求检查程序更新"}, "update.applied": {"update", "info", "已请求安装更新"},
}

type Journal struct {
	mu                 sync.Mutex
	directory          string
	file               *os.File
	size, maxBytes     int64
	maxFiles, capacity int
	entries            []Entry
	lastID             int64
	closed             bool
	storageFailed      bool
}

func Open(directory string) (*Journal, error) { return open(directory, 4<<20, 5, 2000) }
func open(directory string, maxBytes int64, maxFiles, capacity int) (*Journal, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	j := &Journal{directory: directory, maxBytes: maxBytes, maxFiles: maxFiles, capacity: capacity}
	for index := maxFiles - 1; index >= 0; index-- {
		f, err := os.Open(j.path(index))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 8192), 16<<10)
		for scanner.Scan() {
			var entry Entry
			if json.Unmarshal(scanner.Bytes(), &entry) == nil {
				if n, e := strconv.ParseInt(entry.ID, 10, 64); e == nil && n > j.lastID {
					j.lastID = n
				}
				j.append(entry)
			}
		}
		err = scanner.Err()
		_ = f.Close()
		if err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(j.path(0), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	j.file, j.size = f, info.Size()
	return j, nil
}
func (j *Journal) path(index int) string {
	if index == 0 {
		return filepath.Join(j.directory, "activity.jsonl")
	}
	return filepath.Join(j.directory, fmt.Sprintf("activity.%d.jsonl", index))
}
func (j *Journal) append(entry Entry) {
	j.entries = append(j.entries, entry)
	if len(j.entries) > j.capacity {
		copy(j.entries, j.entries[len(j.entries)-j.capacity:])
		j.entries = j.entries[:j.capacity]
	}
}
func (j *Journal) rotate() error {
	if err := j.file.Close(); err != nil {
		return err
	}
	if err := os.Remove(j.path(j.maxFiles - 1)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := j.maxFiles - 2; i >= 0; i-- {
		if err := os.Rename(j.path(i), j.path(i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	f, err := os.OpenFile(j.path(0), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	j.file, j.size = f, 0
	return nil
}
func (j *Journal) Emit(code string, fields Fields) error {
	if j == nil {
		return nil
	}
	info, ok := events[code]
	if !ok {
		code = "request.completed"
		info = events[code]
	}
	fields.Route = SafeRoute(fields.Route)
	reasons := map[string]bool{"EMAIL_VERIFICATION_REQUIRED": true, "SMTP_CONNECT": true, "SMTP_TLS": true, "SMTP_TLS_REQUIRED": true, "SMTP_GREETING": true, "SMTP_AUTH": true, "SMTP_RECIPIENT": true, "SMTP_DATA": true, "MAIL_CANCELED": true, "MAIL_UNAVAILABLE": true, "MAIL_EXPIRED": true, "MAIL_ACCOUNT_UNAVAILABLE": true, "MAIL_SUPERSEDED": true, "MAIL_CONFIG_CHANGED": true, "UPLOAD_STORAGE": true, "CLIENT_CANCELED": true, "FFPROBE_REQUIRED": true, "AUDIO_INVALID": true, "UPLOAD_LIMIT": true, "AUTH_INVALID": true, "NOT_FOUND": true, "VERSION_CONFLICT": true, "CSRF_DENIED": true, "ORIGIN_DENIED": true, "KEY_CONCURRENCY_LIMIT": true, "ADMIN_REQUIRED": true, "INVALID_UPLOAD": true, "STAGE_FAILED": true, "COMMIT_FAILED": true}
	if !reasons[fields.Reason] {
		fields.Reason = ""
	}
	if _, err := uuid.Parse(fields.UserID); len(fields.UserID) != 36 || err != nil {
		fields.UserID = ""
	}
	if _, err := hex.DecodeString(fields.RequestID); len(fields.RequestID) != 16 || err != nil {
		fields.RequestID = ""
	}
	switch fields.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		fields.Method = ""
	}
	timings := map[string]int64{}
	for _, key := range []string{"receive_ms", "wait_ms", "write_ms", "probe_ms", "metadata_ms", "persist_ms", "total_ms", "validation_ms"} {
		if value, ok := fields.Timings[key]; ok && value >= 0 {
			timings[key] = value
		}
	}
	fields.Timings = timings
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return os.ErrClosed
	}
	now := time.Now().UTC()
	id := now.UnixNano()
	if id <= j.lastID {
		id = j.lastID + 1
	}
	j.lastID = id
	entry := Entry{ID: strconv.FormatInt(id, 10), Time: now, Level: info.level, Category: info.category, Event: code, Message: info.message, Fields: fields}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	j.append(entry)
	if j.size+int64(len(raw)) > j.maxBytes {
		if err := j.rotate(); err != nil {
			j.storageFailed = true
			return err
		}
	}
	n, err := j.file.Write(raw)
	if err != nil {
		j.storageFailed = true
	}
	j.size += int64(n)
	return err
}

// SafeRoute never retains identifiers, arbitrary path segments or query strings.
func SafeRoute(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(strings.SplitN(path, "?", 2)[0], "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "v2" {
		return "/unknown"
	}
	groups := map[string]bool{"registration": true, "auth": true, "admin": true, "tracks": true, "playlists": true, "uploads": true, "sync": true, "media": true, "covers": true, "public": true, "subscriptions": true, "invitations": true, "setup": true, "health": true}
	if !groups[parts[2]] {
		return "/api/v2/unknown"
	}
	result := "/api/v2/" + parts[2]
	leaves := map[string]bool{"email": true, "verify": true, "challenge": true, "mail": true, "registration": true, "config": true, "storage": true, "release-email": true, "stage": true, "commit": true, "cover": true, "lyrics": true, "editor": true, "visibility": true, "tracks": true, "order": true, "login": true, "logout": true, "password": true, "keys": true, "sessions": true, "me": true, "activity": true, "users": true, "scan": true, "status": true, "cancel": true, "audit": true, "backups": true, "removed": true, "restore": true, "update": true, "check": true, "apply": true, "stats": true, "logs": true, "playlists": true, "invitations": true, "accept": true, "decline": true, "resend": true}
	for _, part := range parts[3:] {
		if leaves[part] {
			result += "/" + part
		} else {
			result += "/:id"
		}
		if len(result) > 160 {
			break
		}
	}
	return result
}
func (j *Journal) Query(q Query) Page {
	page := Page{Entries: []Entry{}, Retention: "最近 2000 条；磁盘日志最多 5 × 4 MiB"}
	if j == nil {
		return page
	}
	if q.Limit < 1 || q.Limit > 200 {
		q.Limit = 100
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.storageFailed {
		page.Warning = "日志文件写入失败，当前仅保留内存记录，请检查磁盘和目录权限。"
	}
	if q.After != "" && len(j.entries) > 0 && q.After < j.entries[0].ID {
		page.Truncated = true
	}
	matches := func(e Entry) bool {
		return (q.Level == "" || e.Level == q.Level) && (q.Category == "" || e.Category == q.Category) && (q.Before == "" || e.ID < q.Before) && (q.After == "" || e.ID > q.After) && (q.Search == "" || strings.Contains(strings.ToLower(e.Message+" "+e.Route+" "+e.RequestID+" "+e.UserID), strings.ToLower(q.Search)))
	}
	if q.After != "" {
		for _, e := range j.entries {
			if matches(e) {
				if len(page.Entries) == q.Limit {
					page.HasMore = true
					break
				}
				page.Entries = append(page.Entries, e)
			}
		}
		for a, b := 0, len(page.Entries)-1; a < b; a, b = a+1, b-1 {
			page.Entries[a], page.Entries[b] = page.Entries[b], page.Entries[a]
		}
	} else {
		for i := len(j.entries) - 1; i >= 0; i-- {
			e := j.entries[i]
			if matches(e) {
				if len(page.Entries) == q.Limit {
					page.HasMore = true
					break
				}
				page.Entries = append(page.Entries, e)
			}
		}
	}
	if len(page.Entries) > 0 {
		page.Newest = page.Entries[0].ID
		page.Oldest = page.Entries[len(page.Entries)-1].ID
	}
	return page
}
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return j.file.Close()
}
