package music

import (
	"io"
	"time"
)

// Bound space probes by bytes/time instead of each gzip fragment. Final archive
// sync stays mandatory; private frozen scratch files need no per-file fsync.
type backupSpaceWriter struct {
	store   *Store
	path    string
	writer  io.Writer
	left    int64
	checked time.Time
}

func (w *backupSpaceWriter) Write(data []byte) (int, error) {
	w.store.storageWriteMu.Lock()
	defer w.store.storageWriteMu.Unlock()
	if w.left < int64(len(data)) || time.Since(w.checked) > time.Second {
		budget := max(int64(1<<20), int64(len(data)))
		if err := w.store.checkSpace(w.path, budget); err != nil {
			return 0, err
		}
		w.left = budget
		w.checked = time.Now()
	}
	n, err := w.writer.Write(data)
	w.left -= int64(n)
	return n, err
}
