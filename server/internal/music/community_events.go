package music

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// A single coalescing signal wakes readers after a successful commit. The
// watermark avoids turning read transactions or session activity into events.
func (s *Store) notifyCommunityCommit() {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	if s.changeWatchers == 0 || s.changeClosed {
		return
	}
	var stamp string
	if s.db.QueryRow("SELECT (SELECT value FROM meta WHERE key='revision')||':'||(SELECT value FROM meta WHERE key='public_revision')||':'||(SELECT COALESCE(MAX(id),0) FROM audit)").Scan(&stamp) != nil {
		return
	}
	if stamp == s.changeStamp {
		return
	}
	s.changeStamp = stamp
	if s.changeSignal != nil {
		close(s.changeSignal)
	}
	s.changeSignal = make(chan struct{})
}
func (s *Store) watchCommunity() func() {
	s.changeMu.Lock()
	s.changeWatchers++
	if s.changeWatchers == 1 {
		_ = s.db.QueryRow("SELECT (SELECT value FROM meta WHERE key='revision')||':'||(SELECT value FROM meta WHERE key='public_revision')||':'||(SELECT COALESCE(MAX(id),0) FROM audit)").Scan(&s.changeStamp)
	}
	if s.changeSignal == nil {
		s.changeSignal = make(chan struct{})
	}
	s.changeMu.Unlock()
	return func() { s.changeMu.Lock(); s.changeWatchers--; s.changeMu.Unlock() }
}
func (s *Store) communitySignal() <-chan struct{} {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	return s.changeSignal
}
func (a *API) communityEvents(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request.Method != "GET" {
		writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var view realtimeView
	raw := request.URL.Query().Get("view")
	if raw == "" {
		raw = "{}"
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > 4096 || !json.Valid([]byte(raw)) || decoder.Decode(&view) != nil || !view.valid() {
		writeError(response, 400, "INVALID_REALTIME_VIEW", "invalid realtime view")
		return
	}
	auth := authFromRequest(request)
	a.realtimeMu.Lock()
	if a.realtimeStreams == nil {
		a.realtimeStreams = map[string]int{}
	}
	total := 0
	for _, n := range a.realtimeStreams {
		total += n
	}
	if total >= 128 || a.realtimeStreams[auth.userID] >= 8 {
		a.realtimeMu.Unlock()
		response.Header().Set("Retry-After", "60")
		writeError(response, 429, "REALTIME_LIMIT", "too many realtime connections")
		return
	}
	a.realtimeStreams[auth.userID]++
	a.realtimeMu.Unlock()
	defer func() {
		a.realtimeMu.Lock()
		a.realtimeStreams[auth.userID]--
		if a.realtimeStreams[auth.userID] == 0 {
			delete(a.realtimeStreams, auth.userID)
		}
		a.realtimeMu.Unlock()
	}()
	unwatch := a.store.watchCommunity()
	defer unwatch()
	signal := a.store.communitySignal()
	snapshot, err := a.store.realtimeSnapshot(auth, view)
	if err != nil {
		writeError(response, 500, "REALTIME_FAILED", "unable to observe view")
		return
	}
	_, digest := indexRealtime(snapshot)
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(response)
	send := func(event string) error {
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		defer controller.SetWriteDeadline(time.Time{})
		if event == "" {
			_, err = fmt.Fprint(response, ": heartbeat\n\n")
		} else {
			_, err = fmt.Fprintf(response, "event: %s\ndata: {}\n\n", event)
		}
		if err != nil {
			return err
		}
		return controller.Flush()
	}
	if send("ready") != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		changed := false
		select {
		case <-request.Context().Done():
			return
		case <-heartbeat.C:
		case <-signal:
			// Merge adjacent transactions and keep at most one pending wakeup.
			timer := time.NewTimer(150 * time.Millisecond)
			select {
			case <-request.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			signal = a.store.communitySignal()
			changed = true
		}
		current, valid := a.authenticate(request)
		if !valid || !current.session || current.mustChangePassword || realtimeBinding(current) != realtimeBinding(auth) {
			_ = send("expired")
			return
		}
		if a.updating != nil && a.updating() {
			_ = send("reconnect")
			return
		}
		if changed {
			snapshot, err = a.store.realtimeSnapshot(current, view)
			if err != nil {
				return
			}
			_, next := indexRealtime(snapshot)
			if next != digest {
				digest = next
				if send("change") != nil {
					return
				}
			}
		} else if send("") != nil {
			return
		}
	}
}
