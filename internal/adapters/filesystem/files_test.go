package filesystem

import (
	"nextcloud-updater/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestLockOwnershipAndStatePersistence(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".nextcloud-updater", "test")
	os.MkdirAll(dir, 0700)
	runs, e := NewRuns(root, dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = runs.Acquire(); e != nil {
		t.Fatal(e)
	}
	if e = runs.Acquire(); e == nil {
		t.Fatal("duplicate lock accepted")
	}
	runs.Save(domain.State{Phase: domain.Ready})
	s, e := runs.Load()
	if e != nil || s.Phase != domain.Ready {
		t.Fatal(s, e)
	}
	if e = runs.Release(); e != nil {
		t.Fatal(e)
	}
	if runs.RequireLock() == nil {
		t.Fatal("released lock accepted")
	}
}
func TestTraversalStateRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".nextcloud-updater", "test")
	os.MkdirAll(dir, 0700)
	runs, err := NewRuns(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	runs.Save(domain.State{Stage: filepath.Join(root, "unrelated")})
	if _, e := runs.Load(); e == nil {
		t.Fatal("unsafe stage accepted")
	}
}
func TestPrivateDirectoryAndFreeSpace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if e := PrivateDirectory(dir); e != nil {
		t.Fatal(e)
	}
	free, e := FreeBytes(dir)
	if e != nil || free <= 0 {
		t.Fatal(free, e)
	}
}
