package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/domain"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func SafePaths(paths []domain.ArchivePath) error {
	for _, selected := range paths {
		info, e := os.Lstat(selected.Source)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup roots must not be symlinks")
		}
		e = filepath.WalkDir(selected.Source, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Type()&os.ModeSymlink == 0 {
				return nil
			}
			target, e := filepath.EvalSymlinks(p)
			if e != nil {
				return e
			}
			for _, root := range paths {
				if filesystem.Within(root.Source, target) {
					return nil
				}
			}
			return fmt.Errorf("a backup symlink points outside the selected configuration/key paths")
		})
		if e != nil {
			return e
		}
	}
	return nil
}
func archiveNameOK(name string) bool {
	return name != "" && !path.IsAbs(name) && path.Clean(name) != ".." && !strings.HasPrefix(path.Clean(name), "../") && !strings.Contains(name, "\\")
}
func WriteArchive(ctx context.Context, destination string, paths []domain.ArchivePath) (err error) {
	if err = SafePaths(paths); err != nil {
		return err
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if e := f.Close(); err == nil {
			err = e
		}
	}()
	gz := gzip.NewWriter(f)
	defer func() {
		if e := gz.Close(); err == nil {
			err = e
		}
	}()
	tw := tar.NewWriter(gz)
	defer func() {
		if e := tw.Close(); err == nil {
			err = e
		}
	}()
	for _, root := range paths {
		err = filepath.WalkDir(root.Source, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if e := ctx.Err(); e != nil {
				return e
			}
			rel, e := filepath.Rel(root.Source, p)
			if e != nil {
				return e
			}
			name := root.Archive
			if rel != "." {
				name = path.Join(name, filepath.ToSlash(rel))
			}
			if !archiveNameOK(name) {
				return fmt.Errorf("unsafe archive entry")
			}
			info, e := os.Lstat(p)
			if e != nil {
				return e
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, e = os.Readlink(p)
				if e != nil {
					return e
				}
				if filepath.IsAbs(link) {
					return fmt.Errorf("absolute symlinks require an explicit restore mapping")
				}
			}
			if !info.Mode().IsRegular() && !info.IsDir() && link == "" {
				return fmt.Errorf("unsupported special file in configuration backup")
			}
			h, e := tar.FileInfoHeader(info, link)
			if e != nil {
				return e
			}
			h.Name = name
			if e = tw.WriteHeader(h); e != nil {
				return e
			}
			if info.Mode().IsRegular() {
				source, e := os.Open(p)
				if e != nil {
					return e
				}
				_, e = io.Copy(tw, source)
				closeErr := source.Close()
				if e != nil {
					return e
				}
				return closeErr
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func ValidateSQL(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	n, e := io.Copy(io.Discard, gz)
	if e != nil {
		return e
	}
	if n == 0 {
		return fmt.Errorf("empty SQL dump")
	}
	return nil
}
func ValidateArchive(file string) error {
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	count := 0
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if !archiveNameOK(h.Name) {
			return fmt.Errorf("unsafe backup archive path")
		}
		if _, e = io.Copy(io.Discard, tr); e != nil {
			return e
		}
		count++
	}
	if _, e = io.Copy(io.Discard, gz); e != nil {
		return e
	}
	if count == 0 {
		return fmt.Errorf("empty settings archive")
	}
	return nil
}
