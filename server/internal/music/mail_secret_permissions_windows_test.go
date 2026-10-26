package music

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMailSecretWindowsProtectedDACL(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "secrets")
	if err := ensureMailSecretDirectory(directory); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "credential.json")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := protectMailSecretFile(file); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, file} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		control, _, err := sd.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("secret ACL inherits public permissions: %s %v", sd.String(), err)
		}
		sddl := sd.String()
		if !strings.Contains(sddl, user.User.Sid.String()) || !strings.Contains(sddl, ";;;SY)") || !strings.Contains(sddl, ";;;BA)") {
			t.Fatal("expected owner/system/admin ACL", sddl)
		}
		for _, principal := range []string{";;;AU)", ";;;BU)", ";;;WD)", ";;;AN)"} {
			if strings.Contains(sddl, principal) {
				t.Fatal("broad secret ACL", sddl)
			}
		}
		if strings.Count(sddl, "(A;") != 3 {
			t.Fatal("unexpected secret ACL entries", sddl)
		}
	}
}
