//go:build linux

package filesystem

import (
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"syscall"
)

func PrivateDirectory(path string) error {
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	return os.Chmod(path, 0700)
}
func FreeBytes(path string) (int64, error) {
	var s unix.Statfs_t
	e := unix.Statfs(path, &s)
	return int64(s.Bavail) * s.Bsize, e
}
func preserveOwner(path string, info os.FileInfo) error {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot preserve source ownership")
	}
	return os.Chown(path, int(s.Uid), int(s.Gid))
}
func RequireRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("the remote helper requires root access to the persistent configuration and keys")
	}
	return nil
}
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

type actionLock struct{ *os.File }

func (l actionLock) Close() error { _ = unix.Flock(int(l.Fd()), unix.LOCK_UN); return l.File.Close() }
func ActionLock(path string) (io.Closer, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("a previous remote action is still running; wait before recovering")
	}
	return actionLock{f}, nil
}
