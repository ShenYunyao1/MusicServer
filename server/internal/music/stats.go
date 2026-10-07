package music

import (
	"errors"
	"log"
	"time"
)

type apiCallStats struct {
	KeyID           string    `json:"key_id"`
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
VALUES (?,1,?,?,?, ?, ?, ?)
ON CONFLICT(key_id) DO UPDATE SET
    total_requests = total_requests + 1,
    success_requests = success_requests + excluded.success_requests,
    failure_requests = failure_requests + excluded.failure_requests,
    total_bytes = total_bytes + excluded.total_bytes,
    total_duration_ms = total_duration_ms + excluded.total_duration_ms,
    last_called_at = excluded.last_called_at,
    last_status = excluded.last_status`, keyID, success, 1-success, bytes, ms, now, status)
	if err != nil {
		log.Printf("record API call statistics: %v", err)
	}
}

func (a *API) readCallStats() apiStatsResponse {
	result := apiStatsResponse{Keys: []apiCallStats{}}
	if a == nil || a.store == nil || a.store.db == nil {
		result.err = errors.New("statistics database is not available")
		return result
	}
	rows, err := a.store.db.Query(`SELECT key_id,total_requests,success_requests,failure_requests,total_bytes,total_duration_ms,last_called_at,last_status FROM api_call_stats ORDER BY key_id`)
	if err != nil {
		result.err = err
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var value apiCallStats
		var last string
		if err := rows.Scan(&value.KeyID, &value.TotalRequests, &value.SuccessRequests, &value.FailureRequests, &value.TotalBytes, &value.TotalDurationMS, &last, &value.LastStatus); err != nil {
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
	seen := map[string]bool{}
	for _, key := range result.Keys {
		seen[key.KeyID] = true
	}
	a.concurrencyMu.Lock()
	for id, count := range a.active {
		if !seen[id] {
			result.Keys = append(result.Keys, apiCallStats{KeyID: id, ActiveRequests: count})
			result.Total.ActiveRequests += count
		}
	}
	a.concurrencyMu.Unlock()
	result.Total.KeyID = "total"
	return result
}
