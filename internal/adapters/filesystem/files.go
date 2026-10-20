package filesystem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"nextcloud-updater/internal/domain"
	"os"
	"path/filepath"
	"strings"
)

func Within(root, target string) bool {
	r, e := filepath.Rel(root, target)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(os.PathSeparator)) && !filepath.IsAbs(r)
}
func Hash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func AtomicWrite(path string, data []byte, mode os.FileMode, preserve bool) error {
	f, e := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if preserve {
		info, e := os.Stat(path)
		if e != nil {
			return e
		}
		if e = os.Chmod(tmp, info.Mode().Perm()); e != nil {
			return e
		}
		if e = preserveOwner(tmp, info); e != nil {
			return e
		}
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(path))
}
func WriteJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	return AtomicWrite(path, append(b, '\n'), 0600, false)
}
func ReadJSON(path string, value any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, value); e != nil {
		return fmt.Errorf("invalid JSON in %s", filepath.Base(path))
	}
	return nil
}

type Runs struct{ Root, Directory string }

func NewRuns(root, dir string) (*Runs, error) {
	r, e := filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	d, e := filepath.EvalSymlinks(dir)
	if e != nil {
		return nil, e
	}
	r, e = filepath.Abs(r)
	if e != nil {
		return nil, e
	}
	d, e = filepath.Abs(d)
	if e != nil {
		return nil, e
	}
	if filepath.Dir(d) != filepath.Join(r, ".nextcloud-updater") {
		return nil, fmt.Errorf("invalid remote working directory")
	}
	return &Runs{Root: r, Directory: d}, nil
}
func (r *Runs) Load() (domain.State, error) {
	var s domain.State
	e := ReadJSON(filepath.Join(r.Directory, "state.json"), &s)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e == nil && s.Stage != "" && (filepath.Dir(s.Stage) != r.Directory || !strings.HasPrefix(filepath.Base(s.Stage), "before-")) {
		e = fmt.Errorf("invalid backup stage in state")
	}
	return s, e
}
func (r *Runs) Save(s domain.State) error {
	return WriteJSON(filepath.Join(r.Directory, "state.json"), s)
}
func (r *Runs) lock() string { return filepath.Join(r.Root, ".nextcloud-updater.lock") }
func (r *Runs) Acquire() error {
	if e := os.Mkdir(r.lock(), 0700); e != nil {
		return fmt.Errorf("another or interrupted update owns the update lock; inspect it before retrying")
	}
	return os.WriteFile(filepath.Join(r.lock(), "owner"), []byte(r.Directory), 0600)
}
func (r *Runs) RequireLock() error {
	b, e := os.ReadFile(filepath.Join(r.lock(), "owner"))
	if e != nil || string(b) != r.Directory {
		return fmt.Errorf("this run does not own the update lock")
	}
	return nil
}
func (r *Runs) Release() error {
	if e := r.RequireLock(); e != nil {
		return e
	}
	if e := os.Remove(filepath.Join(r.lock(), "owner")); e != nil {
		return e
	}
	return os.Remove(r.lock())
}
func (r *Runs) Stage(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("invalid stage name")
	}
	p := filepath.Join(r.Directory, "before-"+name)
	return p, os.Mkdir(p, 0700)
}
