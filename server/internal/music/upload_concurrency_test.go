package music

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type gatedUploadReader struct {
	reader           io.Reader
	started, release chan struct{}
	first            bool
}

func (r *gatedUploadReader) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		close(r.started)
		<-r.release
	}
	return r.reader.Read(p)
}
func TestActiveUploadDoesNotBlockCleanupOrLoseItsFile(t *testing.T) {
	api, _ := testAPI(t)
	reader := &gatedUploadReader{reader: bytes.NewReader(validTestAudio(t)), started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := api.store.StageAudio("concurrent.wav", reader, nil); done <- err }()
	<-reader.started
	released := false
	defer func() {
		if !released {
			close(reader.release)
			<-done
		}
	}()
	cleanup := make(chan error, 1)
	go func() { cleanup <- api.store.CleanupUploadStages() }()
	select {
	case err := <-cleanup:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("upload file copy held the stage mutex and blocked cleanup")
	}
	files, err := filepath.Glob(filepath.Join(api.store.mediaRoot, ".staging", "*.wav"))
	if err != nil || len(files) != 1 {
		t.Fatal("cleanup removed a live upload")
	}
	if _, err := os.Stat(files[0]); err != nil {
		t.Fatal(err)
	}
	close(reader.release)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
