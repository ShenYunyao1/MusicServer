package music

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func mailTestACLPrincipals(sd *windows.SECURITY_DESCRIPTOR, owner *windows.SID) error {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 3 {
		return fmt.Errorf("expected three secret ACL entries: %s (%v)", sd.String(), err)
	}
	// Compare SID values rather than SDDL text: Windows may render an account
	// SID as LA (built-in administrator) or SY (SYSTEM). Keep duplicate entries
	// meaningful when the service itself runs as SYSTEM.
	expected := []*windows.SID{owner, system, administrators}
	matched := [3]bool{}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		const fileFullAccess = 0x1f01ff
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != fileFullAccess || ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			return fmt.Errorf("unexpected secret ACE permissions: %s", sd.String())
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		found := false
		for j, principal := range expected {
			if !matched[j] && sid.Equals(principal) {
				matched[j], found = true, true
				break
			}
		}
		if !found {
			return fmt.Errorf("unexpected secret ACL principal: %s", sd.String())
		}
	}
	return nil
}

func TestMailSecretWindowsPrincipalAliases(t *testing.T) {
	owner, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	if err := mailTestACLPrincipals(sd, owner); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(sd.String(), ";;;SY)", ";;;WD)", 1),
		strings.Replace(sd.String(), "(A;", "(D;", 1),
		strings.Replace(sd.String(), ";FA;", ";GR;", 1),
		sd.String() + "(A;;FA;;;BA)",
	} {
		candidate, err := windows.SecurityDescriptorFromString(invalid)
		if err != nil {
			t.Fatal(err)
		}
		if mailTestACLPrincipals(candidate, owner) == nil {
			t.Fatalf("accepted invalid secret ACL: %s", invalid)
		}
	}
}

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
		if err := mailTestACLPrincipals(sd, user.User.Sid); err != nil {
			t.Fatal(err)
		}
	}
}
