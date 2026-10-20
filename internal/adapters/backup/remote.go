package backup

import (
	"compress/gzip"
	"context"
	"fmt"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/application"
	"nextcloud-updater/internal/domain"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
)

type Remote struct {
	Dumper       application.DatabaseDumper
	RunDirectory string
}

func (r *Remote) checkStage(stage string) error {
	if filepath.Dir(stage) != r.RunDirectory {
		return fmt.Errorf("backup stage is outside this run")
	}
	return nil
}
func (r *Remote) CheckSpace(stage string, input domain.BackupInput) error {
	if e := r.checkStage(stage); e != nil {
		return e
	}
	var size int64
	for _, root := range input.Paths {
		e := filepath.Walk(root.Source, func(_ string, i os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			if i.Mode().IsRegular() {
				size += i.Size()
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	free, e := filesystem.FreeBytes(stage)
	if e != nil {
		return e
	}
	if free < 2*(size+input.EstimatedDatabaseBytes)+(256<<20) {
		return fmt.Errorf("insufficient server staging space for SQL/configuration backups")
	}
	return nil
}
func (r *Remote) Create(ctx context.Context, d domain.Deployment, input domain.BackupInput, stage, from, target string) (domain.Backup, error) {
	result := domain.Backup{Directory: stage, Manifest: domain.Manifest{Kind: input.Kind, FromVersion: from, TargetVersion: target, Scope: domain.BackupScope, Artifacts: []domain.Artifact{}}}
	if e := r.checkStage(stage); e != nil {
		return result, e
	}
	sql := filepath.Join(stage, "database.sql.gz")
	f, e := os.OpenFile(sql, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return result, e
	}
	gz := gzip.NewWriter(f)
	dumpErr := r.Dumper.DumpDatabase(ctx, d, gz)
	zipErr := gz.Close()
	syncErr := f.Sync()
	closeErr := f.Close()
	for _, err := range []error{dumpErr, zipErr, syncErr, closeErr} {
		if err != nil {
			return result, err
		}
	}
	if e = ValidateSQL(sql); e != nil {
		return result, e
	}
	metadata := filepath.Join(stage, "recovery.json")
	if e = filesystem.WriteJSON(metadata, input.Metadata); e != nil {
		return result, e
	}
	paths := append([]domain.ArchivePath{}, input.Paths...)
	paths = append(paths, domain.ArchivePath{Source: d.Source, Archive: "deployment/original-compose.yml"}, domain.ArchivePath{Source: metadata, Archive: "deployment/recovery.json"})
	if info, e := os.Stat(filepath.Join(d.Root, ".env")); e == nil && info.Mode().IsRegular() {
		source, e := filepath.EvalSymlinks(filepath.Join(d.Root, ".env"))
		if e != nil {
			return result, e
		}
		paths = append(paths, domain.ArchivePath{Source: source, Archive: "deployment/dotenv"})
	}
	if e = WriteArchive(ctx, filepath.Join(stage, "settings.tar.gz"), paths); e != nil {
		return result, e
	}
	for _, name := range []string{"database.sql.gz", "settings.tar.gz", "recovery.json"} {
		p := filepath.Join(stage, name)
		info, e := os.Stat(p)
		if e != nil {
			return result, e
		}
		hash, e := filesystem.Hash(p)
		if e != nil {
			return result, e
		}
		result.Manifest.Artifacts = append(result.Manifest.Artifacts, domain.Artifact{Name: name, Bytes: info.Size(), SHA256: hash})
	}
	p := filepath.Join(stage, "manifest.json")
	if e = filesystem.WriteJSON(p, result.Manifest); e != nil {
		return result, e
	}
	result.SHA256, e = filesystem.Hash(p)
	return result, e
}

var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidateManifest(m domain.Manifest) error {
	expected := map[string]bool{"database.sql.gz": false, "settings.tar.gz": false, "recovery.json": false}
	for _, a := range m.Artifacts {
		seen, ok := expected[a.Name]
		if !ok || seen || a.Bytes < 0 || !hashPattern.MatchString(a.SHA256) {
			return fmt.Errorf("invalid backup artifact manifest")
		}
		expected[a.Name] = true
	}
	for _, found := range expected {
		if !found {
			return fmt.Errorf("backup manifest is incomplete")
		}
	}
	return nil
}
func VerifyFiles(directory string, expected domain.Backup) error {
	if !hashPattern.MatchString(expected.SHA256) {
		return fmt.Errorf("invalid manifest receipt")
	}
	hash, e := filesystem.Hash(filepath.Join(directory, "manifest.json"))
	if e != nil {
		return e
	}
	if hash != expected.SHA256 {
		return fmt.Errorf("backup manifest checksum mismatch")
	}
	var actual domain.Manifest
	if e = filesystem.ReadJSON(filepath.Join(directory, "manifest.json"), &actual); e != nil {
		return e
	}
	if !reflect.DeepEqual(actual, expected.Manifest) {
		return fmt.Errorf("backup manifest changed")
	}
	if e = ValidateManifest(actual); e != nil {
		return e
	}
	for _, a := range actual.Artifacts {
		p := filepath.Join(directory, a.Name)
		info, e := os.Stat(p)
		if e != nil {
			return e
		}
		hash, e := filesystem.Hash(p)
		if e != nil {
			return e
		}
		if info.Size() != a.Bytes || hash != a.SHA256 {
			return fmt.Errorf("backup size/checksum mismatch: %s", a.Name)
		}
	}
	return nil
}
func (r *Remote) Verify(b domain.Backup) error {
	if e := r.checkStage(b.Directory); e != nil {
		return e
	}
	return VerifyFiles(b.Directory, b)
}
func (r *Remote) Clear(stage string) error {
	if stage == "" {
		return nil
	}
	if e := r.checkStage(stage); e != nil {
		return e
	}
	for _, name := range []string{"database.sql.gz", "settings.tar.gz", "recovery.json", "manifest.json"} {
		if e := os.Remove(filepath.Join(stage, name)); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return nil
}
