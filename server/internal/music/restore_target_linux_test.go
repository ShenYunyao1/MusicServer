package music

import "testing"

func TestRestoreMountDetectionLinuxRoot(t *testing.T) {
	mounted, err := restoreDestinationMount("/")
	if err != nil || !mounted {
		t.Fatal("root mount not detected", mounted, err)
	}
}
