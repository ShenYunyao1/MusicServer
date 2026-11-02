package music

import (
	"errors"
	"io/fs"
	"testing"
)

type disappearingEntry struct{ err error }

func (disappearingEntry) Name() string                 { return "removed.partial" }
func (disappearingEntry) IsDir() bool                  { return false }
func (disappearingEntry) Type() fs.FileMode            { return 0 }
func (e disappearingEntry) Info() (fs.FileInfo, error) { return nil, e.err }

func TestDirectoryBytesToleratesConcurrentRemoval(t *testing.T) {
	for _, failure := range []error{fs.ErrNotExist, fs.ErrPermission} {
		_, err := directoryBytesWithWalker("unused", func(_ string, fn fs.WalkDirFunc) error {
			return fn("unused/removed.partial", disappearingEntry{failure}, nil)
		})
		if errors.Is(failure, fs.ErrNotExist) && err != nil {
			t.Fatal("concurrent removal aborted summary", err)
		}
		if errors.Is(failure, fs.ErrPermission) && !errors.Is(err, failure) {
			t.Fatal("real filesystem error hidden", err)
		}
	}
}
