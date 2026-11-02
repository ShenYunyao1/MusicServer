package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type listeningError struct {
	status        int
	code, message string
}

func (e *listeningError) Error() string { return e.message }
func listenError(status int, code, message string) error {
	return &listeningError{status, code, message}
}
func listeningIdentity(reader sqlReader, auth authContext) (authContext, error) {
	if auth.session {
		if err := browseIdentity(reader, auth); err != nil {
			if errors.Is(err, errResourceHidden) {
				return auth, listenError(401, "AUTH_REQUIRED", "login is no longer valid")
			}
			return auth, err
		}
		return auth, nil
	}
	current, err := currentKeyAuth(reader, auth)
	if err != nil {
		return auth, listenError(401, "AUTH_REQUIRED", "player key is no longer valid")
	}
	if !hasScope(current, ScopeMediaRead) {
		return auth, listenError(403, "SCOPE_DENIED", "media:read is required")
	}
	return current, nil
}

// Tombstoned accounts retain their user row, so soft deletion clears private
// listening state explicitly; permanent deletion is covered by foreign keys.
func deleteAccountListeningRows(tx *sql.Tx, userID string) error {
	var exists bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='listening_sessions')").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, table := range []string{"listening_sessions", "listening_totals", "listening_daily"} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE user_id=?", userID); err != nil {
			return err
		}
	}
	return nil
}
func listeningTrackAllowed(reader sqlReader, auth authContext, id string) bool {
	if auth.session {
		return true
	}
	args := []any{id}
	for pid, allowed := range auth.allowedPlaylists {
		if allowed {
			args = append(args, pid)
		}
	}
	if len(args) == 1 {
		return false
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)-1), ",")
	args = append(args, auth.userID, auth.userID, auth.userID)
	var allowed bool
	err := reader.QueryRow("SELECT EXISTS(SELECT 1 FROM playlist_tracks m JOIN playlists p ON p.id=m.playlist_id LEFT JOIN users u ON u.id=p.owner_id WHERE m.track_id=? AND m.playlist_id IN ("+placeholders+") AND (p.system=1 OR p.owner_id=? OR (u.enabled=1 AND EXISTS(SELECT 1 FROM subscriptions s WHERE s.user_id=? AND s.playlist_id=p.id) AND (p.visibility='public' OR (p.visibility='shared' AND EXISTS(SELECT 1 FROM playlist_invitations i WHERE i.playlist_id=p.id AND i.inviter_id=p.owner_id AND i.recipient_id=? AND i.state='accepted'))))))", args...).Scan(&allowed)
	return err == nil && allowed
}

func (s *Store) listeningEnabled() bool {
	schema, err := sqliteSchemaIdentity(s.db)
	return err == nil && schema >= 9
}

func (a *API) listening(w http.ResponseWriter, r *http.Request) {
	if !a.store.listeningEnabled() {
		writeError(w, http.StatusNotImplemented, "LISTENING_DISABLED", "listening statistics require database schema 9")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/listening/")
	var result any
	var err error
	switch path {
	case "sessions":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, 405, "METHOD_NOT_ALLOWED", "POST required")
			return
		}
		var body struct {
			SessionID string `json:"session_id"`
			TrackID   string `json:"track_id"`
			PlayedMS  int64  `json:"played_ms"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || !listeningUUID(body.SessionID) || !listeningUUID(body.TrackID) || body.PlayedMS < 0 || body.PlayedMS > int64((24*time.Hour)/time.Millisecond) {
			writeError(w, 400, "INVALID_LISTENING", "invalid listening session")
			return
		}
		result, err = a.store.reportListening(authFromRequest(r), body.SessionID, body.TrackID, body.PlayedMS, time.Now().UTC())
	case "tracks":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, 405, "METHOD_NOT_ALLOWED", "GET required")
			return
		}
		query, e := parseListeningQuery(r)
		if e != nil {
			writeError(w, 400, "INVALID_LISTENING_QUERY", e.Error())
			return
		}
		result, err = a.store.listListening(authFromRequest(r), query, time.Now().UTC())
	default:
		writeError(w, 404, "NOT_FOUND", "listening endpoint not found")
		return
	}
	if err != nil {
		var e *listeningError
		if errors.As(err, &e) {
			if e.status == 429 {
				w.Header().Set("Retry-After", "60")
			}
			writeError(w, e.status, e.code, e.message)
		} else if errors.Is(err, sql.ErrNoRows) || errors.Is(err, errResourceHidden) {
			writeError(w, 404, "NOT_FOUND", "track unavailable")
		} else {
			writeError(w, 503, "LISTENING_UNAVAILABLE", "listening statistics unavailable")
		}
		return
	}
	writeJSON(w, 200, result)
}
func listeningUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

type listeningReceipt struct {
	Counted   bool  `json:"counted"`
	MinimumMS int64 `json:"minimum_ms"`
}

func (s *Store) reportListening(auth authContext, sid, trackID string, played int64, now time.Time) (listeningReceipt, error) {
	result := listeningReceipt{}
	err := s.transaction(func(tx *sql.Tx) error {
		var err error
		auth, err = listeningIdentity(tx, auth)
		if err != nil {
			return err
		}
		if !listeningTrackAllowed(tx, auth, trackID) {
			return errResourceHidden
		}
		var removed, missing bool
		var duration int64
		if err := tx.QueryRow("SELECT removed,missing,COALESCE(json_extract(payload,'$.duration_ms'),0) FROM tracks WHERE id=?", trackID).Scan(&removed, &missing, &duration); err != nil {
			return err
		}
		if removed || missing {
			return errResourceHidden
		}
		stamp := now.UnixMilli()
		var storedTrack string
		var start, prior, minimum int64
		var counted bool
		err = tx.QueryRow("SELECT track_id,started_at,played_ms,minimum_ms,counted FROM listening_sessions WHERE user_id=? AND session_id=?", auth.userID, sid).Scan(&storedTrack, &start, &prior, &minimum, &counted)
		if errors.Is(err, sql.ErrNoRows) {
			if played != 0 {
				return listenError(409, "LISTENING_START_REQUIRED", "start session with played_ms=0 before progress")
			}
			// Bounded retention and admission apply per account, never erase another user's state.
			if _, err = tx.Exec("DELETE FROM listening_sessions WHERE user_id=? AND started_at<?", auth.userID, now.Add(-24*time.Hour).UnixMilli()); err != nil {
				return err
			}
			if _, err = tx.Exec("DELETE FROM listening_daily WHERE user_id=? AND day<?", auth.userID, now.AddDate(0, 0, -90).Format("2006-01-02")); err != nil {
				return err
			}
			var total, recent int
			if err = tx.QueryRow("SELECT count(*),COALESCE(sum(started_at>=?),0) FROM listening_sessions WHERE user_id=?", stamp-60000, auth.userID).Scan(&total, &recent); err != nil {
				return err
			}
			if total >= 3000 || recent >= 120 {
				return listenError(429, "LISTENING_LIMIT", "listening session limit reached")
			}
			minimum = 30000
			if duration > 0 {
				minimum = max(int64(1), min(minimum, duration/2))
			}
			_, err = tx.Exec("INSERT INTO listening_sessions(user_id,session_id,track_id,started_at,played_ms,minimum_ms,counted,last_seen_at) VALUES(?,?,?,?,0,?,0,?)", auth.userID, sid, trackID, stamp, minimum, stamp)
			result.MinimumMS = minimum
			return err
		}
		if err != nil {
			return err
		}
		if storedTrack != trackID {
			return listenError(409, "LISTENING_SESSION_CONFLICT", "session is bound to another track")
		}
		result = listeningReceipt{Counted: counted, MinimumMS: minimum}
		if stamp-start > int64((24*time.Hour)/time.Millisecond) {
			return listenError(409, "LISTENING_SESSION_EXPIRED", "start a new listening session")
		}
		if played <= prior {
			return nil
		} // Retries and delayed cumulative reports cannot reset or duplicate a count.
		if played > max(int64(0), stamp-start)+2000 {
			return listenError(400, "LISTENING_ELAPSED", "played time exceeds elapsed session time")
		}
		_, err = tx.Exec("UPDATE listening_sessions SET played_ms=?,last_seen_at=? WHERE user_id=? AND session_id=?", played, stamp, auth.userID, sid)
		if err != nil {
			return err
		}
		if counted || played < minimum {
			return nil
		}
		for _, statement := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO listening_totals(user_id,track_id,play_count,last_played_at) VALUES(?,?,1,?) ON CONFLICT(user_id,track_id) DO UPDATE SET play_count=play_count+1,last_played_at=excluded.last_played_at", []any{auth.userID, trackID, stamp}},
			{"INSERT INTO listening_daily(user_id,track_id,day,play_count,last_played_at) VALUES(?,?,?,1,?) ON CONFLICT(user_id,track_id,day) DO UPDATE SET play_count=play_count+1,last_played_at=excluded.last_played_at", []any{auth.userID, trackID, now.Format("2006-01-02"), stamp}},
			{"UPDATE listening_sessions SET counted=1 WHERE user_id=? AND session_id=?", []any{auth.userID, sid}},
		} {
			if _, err = tx.Exec(statement.sql, statement.args...); err != nil {
				return err
			}
		}
		result.Counted = true
		return nil
	})
	return result, err
}

type listeningQuery struct {
	period, search string
	offset, limit  int
}

func parseListeningQuery(r *http.Request) (listeningQuery, error) {
	q := listeningQuery{period: r.URL.Query().Get("range"), search: strings.TrimSpace(r.URL.Query().Get("q")), limit: 20}
	if q.period == "" {
		q.period = "all"
	}
	if q.period != "all" && q.period != "7d" && q.period != "30d" {
		return q, errors.New("range must be all, 7d or 30d")
	}
	for name, target := range map[string]*int{"offset": &q.offset, "limit": &q.limit} {
		if raw, ok := r.URL.Query()[name]; ok {
			if len(raw) != 1 {
				return q, errors.New("invalid " + name)
			}
			n, e := strconv.Atoi(raw[0])
			if e != nil || n < 0 {
				return q, errors.New("invalid " + name)
			}
			*target = n
		}
	}
	if q.limit != 20 && q.limit != 30 && q.limit != 50 || q.offset > 1000000 || len(q.search) > 1024 {
		return q, errors.New("invalid page or search")
	}
	return q, nil
}
func (s *Store) listListening(auth authContext, q listeningQuery, now time.Time) (map[string]any, error) {
	result := map[string]any{"items": []map[string]any{}, "total": 0, "offset": q.offset, "limit": q.limit}
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	err = func() error {
		var err error
		auth, err = listeningIdentity(tx, auth)
		if err != nil {
			return err
		}
		// Native clients only report listens. The personal browsing endpoint remains session-only.
		if !auth.session {
			return listenError(403, "SESSION_REQUIRED", "personal listening statistics require account login")
		}
		source := "SELECT track_id,play_count,last_played_at FROM listening_totals WHERE user_id=?"
		args := []any{auth.userID}
		if q.period != "all" {
			days := 7
			if q.period == "30d" {
				days = 30
			}
			source = "SELECT track_id,sum(play_count) AS play_count,max(last_played_at) AS last_played_at FROM listening_daily WHERE user_id=? AND day>=? GROUP BY track_id"
			args = append(args, now.AddDate(0, 0, -days+1).Format("2006-01-02"))
		}
		from := " FROM (" + source + ") l JOIN tracks t ON t.id=l.track_id WHERE t.removed=0 AND t.missing=0"
		if q.search != "" {
			from += " AND " + browseTrackSearch
			args = append(args, q.search)
		}
		var total int
		if err = tx.QueryRow("SELECT count(*)"+from, args...).Scan(&total); err != nil {
			return err
		}
		result["total"] = total
		rows, err := tx.Query("SELECT t.id,t.title,json_extract(t.payload,'$.artist','$.album','$.duration_ms','$.cover_id'),l.play_count,l.last_played_at"+from+" ORDER BY l.play_count DESC,l.last_played_at DESC,t.id LIMIT ? OFFSET ?", append(args, q.limit, q.offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, title, raw string
			var count, stamp int64
			if err = rows.Scan(&id, &title, &raw, &count, &stamp); err != nil {
				return err
			}
			var values []any
			if err = json.Unmarshal([]byte(raw), &values); err != nil {
				return err
			}
			if len(values) != 4 {
				return errors.New("invalid listening projection")
			}
			item := map[string]any{"id": id, "title": title, "artist": values[0], "album": values[1], "duration_ms": values[2], "cover_id": values[3]}
			item["detail_loaded"] = false
			item["play_count"] = count
			item["last_played_at"] = time.UnixMilli(stamp).UTC().Format(time.RFC3339Nano)
			items = append(items, item)
		}
		result["items"] = items
		return rows.Err()
	}()
	if err == nil {
		err = tx.Commit()
	}
	return result, err
}

// Maintenance is global, so inactive accounts do not retain expired sessions
// forever. Counted totals remain; only replay guards and old daily buckets age.
func (s *Store) CleanupListeningStatistics() error {
	schema, err := sqliteSchemaIdentity(s.db)
	if err != nil || schema < 9 {
		return err
	}
	return s.transaction(func(tx *sql.Tx) error {
		now := time.Now().UTC()
		if _, err := tx.Exec("DELETE FROM listening_sessions WHERE started_at<?", now.Add(-24*time.Hour).UnixMilli()); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM listening_daily WHERE day<?", now.AddDate(0, 0, -90).Format("2006-01-02"))
		return err
	})
}
