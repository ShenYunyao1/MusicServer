//go:build windows

package music

import (
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func mailSecretDescriptor(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	flags := ""
	if directory {
		flags = "OICI"
	}
	return windows.SecurityDescriptorFromString("D:P(A;" + flags + ";FA;;;" + user.User.Sid.String() + ")(A;" + flags + ";FA;;;SY)(A;" + flags + ";FA;;;BA)")
}

func setMailSecretDACL(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory {
		return errors.New("invalid mail secret path")
	}
	descriptor, err := mailSecretDescriptor(directory)
	if err != nil {
		return err
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	runtime.KeepAlive(descriptor)
	return err
}

func ensureMailSecretDirectory(path string) error {
	descriptor, err := mailSecretDescriptor(true)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	// Apply a protected descriptor during creation so no secret is ever written
	// into a directory inheriting the workspace's Users/Authenticated Users ACEs.
	err = windows.CreateDirectory(name, &attributes)
	runtime.KeepAlive(descriptor)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	return setMailSecretDACL(path, true)
}

func protectMailSecretFile(path string) error { return setMailSecretDACL(path, false) }
