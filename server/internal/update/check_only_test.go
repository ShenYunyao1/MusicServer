package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestCheckingNeverDownloadsOrBacksUp(t *testing.T) {
	payload := []byte("package must not be downloaded while checking")
	sum := sha256.Sum256(payload)
	manager, _ := testManager(t, "9.0.0", "sha256:"+hex.EncodeToString(sum[:]), payload)
	if err := manager.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Status().State != "available" {
		t.Fatalf("check installed/prepared instead of reporting availability: %+v", manager.Status())
	}
	files, err := os.ReadDir(manager.dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.IsDir() {
			t.Fatal("check created package download directory")
		}
	}
}
