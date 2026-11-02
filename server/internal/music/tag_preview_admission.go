package music

import (
	"errors"
	"sync"
	"time"
)

var errTagPreviewBusy = errors.New("another tag preview is being calculated; retry shortly")
var errTagPreviewRate = errors.New("too many tag previews; retry after the current minute")

type tagPreviewAdmission struct {
	mu     sync.Mutex
	busy   bool
	window time.Time
	total  int
	owners map[string]int
}

// The rate budget survives task cancellation. The bounded owner map is cleared
// each minute; only admitted accounts enter it (at most 60 entries per window).
func (s *Store) acquireTagPreview(owner string) (func(), error) {
	a := &s.tagBatches.preview
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if now.Sub(a.window) >= time.Minute {
		a.window = now
		a.total = 0
		a.owners = map[string]int{}
	}
	if a.total >= 60 || a.owners[owner] >= 20 {
		return nil, errTagPreviewRate
	}
	if a.busy {
		return nil, errTagPreviewBusy
	}
	a.busy = true
	a.total++
	a.owners[owner]++
	var once sync.Once
	return func() { once.Do(func() { a.mu.Lock(); a.busy = false; a.mu.Unlock() }) }, nil
}
