package music

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRound3RestoreCleanupRecognizesSameDirectoryAlias(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	alias := filepath.Join(base, "alias")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", alias, real).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	} else if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(alias, "restored")
	stage := filepath.Join(alias, ".musicserver-restore-"+restoreTargetKey(target)+"-123")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeRestoreMarker(stage, target); err != nil {
		t.Fatal(err)
	}
	if owned, err := managedRestoreStage(real, filepath.Base(stage)); err != nil || !owned {
		t.Fatal("resolved same directory parent rejected", owned, err)
	}
	removed, active, err := CleanupRestoreStaging(alias)
	if err != nil || removed != 1 || active != 0 {
		t.Fatal("same directory alias not recognized", removed, active, err)
	}
}
