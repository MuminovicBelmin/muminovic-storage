//go:build windows

package filesystem

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"testing"
)

func TestBackupDirectoryDisablesInheritedAccess(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private-backup")
	if err := PrivateDirectory(directory); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("backup ACL still inherits access")
	}
	acl, _, err := descriptor.DACL()
	if err != nil || acl == nil {
		t.Fatal("private backup ACL missing", err)
	}
}
