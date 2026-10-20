package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/domain"
	"path/filepath"
	"time"
)

type Local struct{}

func (Local) Begin(root string) (string, error) {
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	dir, e := filepath.Abs(filepath.Join(root, time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(b)))
	if e != nil {
		return "", e
	}
	return dir, filesystem.PrivateDirectory(dir)
}
func (Local) CheckSpace(dir string, required int64) error {
	if required < 0 {
		return fmt.Errorf("invalid backup size")
	}
	free, e := filesystem.FreeBytes(dir)
	if e != nil {
		return e
	}
	if free < required {
		return fmt.Errorf("insufficient free space on the local backup disk")
	}
	return nil
}
func (l Local) Receive(ctx context.Context, root string, b domain.Backup, download func(context.Context, string, string) error) (string, error) {
	if e := ValidateManifest(b.Manifest); e != nil {
		return "", e
	}
	version, e := domain.ParseVersion(b.Manifest.TargetVersion)
	if e != nil {
		return "", e
	}
	dir := filepath.Join(root, fmt.Sprintf("before-%d", version[0]))
	if b.Manifest.Kind == "mariadb" {
		if _, e = domain.DatabaseImage("mariadb:" + domain.DatabaseSeries(version)); e != nil {
			return "", e
		}
		dir = filepath.Join(root, "before-mariadb-"+domain.DatabaseSeries(version))
	} else if b.Manifest.Kind != "" && b.Manifest.Kind != "nextcloud" {
		return "", fmt.Errorf("unknown backup kind")
	}
	if e = filesystem.PrivateDirectory(dir); e != nil {
		return "", e
	}
	required := int64(64 << 20)
	for _, a := range b.Manifest.Artifacts {
		if a.Bytes > math.MaxInt64-required {
			return "", fmt.Errorf("invalid backup sizes")
		}
		required += a.Bytes
	}
	if e = l.CheckSpace(dir, required); e != nil {
		return "", e
	}
	if e = download(ctx, b.Directory+"/manifest.json", filepath.Join(dir, "manifest.json")); e != nil {
		return "", e
	}
	for _, a := range b.Manifest.Artifacts {
		if e = download(ctx, b.Directory+"/"+a.Name, filepath.Join(dir, a.Name)); e != nil {
			return "", e
		}
	}
	if e = VerifyFiles(dir, b); e != nil {
		return "", e
	}
	if e = ValidateSQL(filepath.Join(dir, "database.sql.gz")); e != nil {
		return "", e
	}
	if e = ValidateArchive(filepath.Join(dir, "settings.tar.gz")); e != nil {
		return "", e
	}
	e = filesystem.WriteJSON(filepath.Join(dir, "BACKUP-VERIFIED.json"), map[string]string{"manifest_sha256": b.SHA256, "verified_utc": time.Now().UTC().Format(time.RFC3339)})
	return b.SHA256, e
}
