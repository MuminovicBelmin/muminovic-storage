//go:build windows

package filesystem

import (
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
)

func PrivateDirectory(path string) error {
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return e
	}
	descriptor, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if e != nil {
		return e
	}
	acl, _, e := descriptor.DACL()
	if e != nil {
		return e
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
func FreeBytes(path string) (int64, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, e
	}
	var available, total, free uint64
	e = windows.GetDiskFreeSpaceEx(p, &available, &total, &free)
	return int64(available), e
}
func preserveOwner(_ string, _ os.FileInfo) error { return nil }
func syncDirectory(_ string) error                { return nil }
func RequireRoot() error                          { return fmt.Errorf("the remote helper runs on Linux only") }
func ActionLock(_ string) (io.Closer, error) {
	return nil, fmt.Errorf("remote action locks require Linux")
}
