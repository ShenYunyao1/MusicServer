package music

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

type apiCallStats struct {
	UserID          string    `json:"user_id,omitempty"`
	AccountEnabled  bool      `json:"account_enabled"`
	KeyID           string    `json:"key_id"`
	KeyName         string    `json:"key_name,omitempty"`
	TotalRequests   int64     `json:"total_requests"`
	SuccessRequests int64     `json:"success_requests"`
	FailureRequests int64     `json:"failure_requests"`
	TotalBytes      int64     `json:"total_bytes"`
	TotalDurationMS int64     `json:"total_duration_ms"`
	LastCalledAt    time.Time `json:"last_called_at,omitzero"`
	LastStatus      int       `json:"last_status"`
	ActiveRequests  int       `json:"active_requests"`
}

type apiStatsResponse struct {
	Keys  []apiCallStats `json:"keys"`
	Total apiCallStats   `json:"total"`
	err   error
}

// Reconcile persisted statistics with current, non-revoked player keys.
func syncCallStats(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM api_call_stats WHERE key_id NOT IN
        (SELECT id FROM api_keys WHERE json_extract(payload,'$.revoked_at') IS NULL)`); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO api_call_stats(key_id)
        SELECT id FROM api_keys WHERE json_extract(payload,'$.revoked_at') IS NULL
        ON CONFLICT(key_id) DO NOTHING`)
	return err
}

func (a *API) recordCall(keyID string, status int, bytes int64, duration time.Duration) {
	if a == nil || a.store == nil || a.store.db == nil || keyID == "" {
		return
	}
	ms := duration.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	success := 0
	if status >= 200 && status < 400 {
		success = 1
	}
	_, err := a.store.db.Exec(`
INSERT INTO api_call_stats
    (key_id,total_requests,success_requests,failure_requests,total_bytes,total_duration_ms,last_called_at,last_status)
SELECT id,1,?,?,?,?,?,? FROM api_keys
WHERE id=? AND json_extract(payload,'$.revoked_at') IS NULL
ON CONFLICT(key_id) DO UPDATE SET
    total_requests = total_requests + 1,
    success_requests = success_requests + excluded.success_requests,
    failure_requests = failure_requests + excluded.failure_requests,
    total_bytes = total_bytes + excluded.total_bytes,
    total_duration_ms = total_duration_ms + excluded.total_duration_ms,
    last_called_at = excluded.last_called_at,
    last_status = excluded.last_status`, success, 1-success, bytes, ms, now, status, keyID)
	if err != nil {
		log.Printf("record API call statistics: %v", err)
	}
}

func (a *API) readCallStats(userIDs ...string) apiStatsResponse {
	result := apiStatsResponse{Keys: []apiCallStats{}}
	if a == nil || a.store == nil || a.store.db == nil {
		result.err = errors.New("statistics database is not available")
		return result
	}
	userID := ""
	if len(userIDs) > 0 {
		userID = userIDs[0]
	}
	rows, err := a.store.db.Query(`SELECT k.id,json_extract(k.payload,'$.name'),k.user_id,u.enabled,
        COALESCE(s.total_requests,0),COALESCE(s.success_requests,0),COALESCE(s.failure_requests,0),
        COALESCE(s.total_bytes,0),COALESCE(s.total_duration_ms,0),COALESCE(s.last_called_at,''),COALESCE(s.last_status,0)
        FROM api_keys k JOIN users u ON u.id=k.user_id LEFT JOIN api_call_stats s ON s.key_id=k.id
        WHERE json_extract(k.payload,'$.revoked_at') IS NULL AND (?='' OR k.user_id=?) ORDER BY k.id`, userID, userID)
	if err != nil {
		result.err = err
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var value apiCallStats
		var last string
		if err := rows.Scan(&value.KeyID, &value.KeyName, &value.UserID, &value.AccountEnabled, &value.TotalRequests, &value.SuccessRequests, &value.FailureRequests, &value.TotalBytes, &value.TotalDurationMS, &last, &value.LastStatus); err != nil {
			result.err = err
			return result
		}
		if last != "" {
			value.LastCalledAt, _ = time.Parse(time.RFC3339Nano, last)
		}
		value.ActiveRequests = a.activeRequests(value.KeyID)
		result.Keys = append(result.Keys, value)
		result.Total.TotalRequests += value.TotalRequests
		result.Total.SuccessRequests += value.SuccessRequests
		result.Total.FailureRequests += value.FailureRequests
		result.Total.TotalBytes += value.TotalBytes
		result.Total.TotalDurationMS += value.TotalDurationMS
		result.Total.ActiveRequests += value.ActiveRequests
		if value.LastCalledAt.After(result.Total.LastCalledAt) {
			result.Total.LastCalledAt = value.LastCalledAt
			result.Total.LastStatus = value.LastStatus
		}
	}
	result.err = rows.Err()
	result.Total.KeyID = "total"
	return result
}
