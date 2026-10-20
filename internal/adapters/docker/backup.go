package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"nextcloud-updater/internal/adapters/backup"
	"nextcloud-updater/internal/domain"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const databaseAuth = `set -eu
if [ -n "${MARIADB_ROOT_PASSWORD_FILE:-${MYSQL_ROOT_PASSWORD_FILE:-}}" ]; then
    MYSQL_PWD=$(cat "${MARIADB_ROOT_PASSWORD_FILE:-$MYSQL_ROOT_PASSWORD_FILE}")
else
    MYSQL_PWD=${MARIADB_ROOT_PASSWORD:-${MYSQL_ROOT_PASSWORD:-}}
fi
export MYSQL_PWD
`

func (r *Runtime) databaseCommand(d domain.Deployment, shell string) []string {
	return []string{"docker", "exec", "-e", "NEXTCLOUD_BACKUP_DATABASE=" + d.DatabaseName, d.DatabaseID, "sh", "-ec", databaseAuth + shell}
}
func (r *Runtime) databaseSize(ctx context.Context, d domain.Deployment) (int64, error) {
	shell := `client=$(command -v mariadb || command -v mysql)
exec "$client" --user=root --batch --skip-column-names --execute='SELECT COALESCE(SUM(data_length + index_length), 0) FROM information_schema.tables'
`
	b, e := r.run(ctx, r.databaseCommand(d, shell), "estimate database backup size")
	if e != nil {
		return 0, e
	}
	size, e := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if e != nil || size < 0 {
		return 0, fmt.Errorf("cannot estimate database backup space")
	}
	return size, nil
}
func (r *Runtime) DumpDatabase(ctx context.Context, d domain.Deployment, out io.Writer) error {
	shell := `dump=$(command -v mariadb-dump || command -v mysqldump)
exec "$dump" --user=root --lock-all-tables --routines --triggers --events --hex-blob --no-tablespaces --all-databases --add-drop-database
`
	return r.Commands.Stream(ctx, r.command(r.databaseCommand(d, shell), "dump all MariaDB databases and system accounts", true), out)
}
func hostPath(c container, target string) (string, error) {
	if !path.IsAbs(target) {
		return "", fmt.Errorf("configuration/key path must be absolute")
	}
	target = path.Clean(target)
	best := ""
	longest := -1
	for _, m := range c.Mounts {
		base := path.Clean(m.Destination)
		if target == base || strings.HasPrefix(target, base+"/") {
			if len(base) > longest {
				relative := strings.TrimPrefix(strings.TrimPrefix(target, base), "/")
				best = filepath.Join(m.Source, filepath.FromSlash(relative))
				longest = len(base)
			}
		}
	}
	if longest < 0 {
		return "", fmt.Errorf("required configuration/key path is outside persistent mounts")
	}
	return best, nil
}
func (r *Runtime) settings(ctx context.Context, d domain.Deployment, app, database container, system map[string]any) ([]domain.ArchivePath, error) {
	paths := []domain.ArchivePath{}
	add := func(target, archive string, required bool) error {
		source, e := hostPath(app, target)
		if e != nil {
			return e
		}
		_, e = os.Lstat(source)
		if os.IsNotExist(e) && !required {
			return nil
		}
		if e != nil {
			return e
		}
		paths = append(paths, domain.ArchivePath{Source: source, Archive: archive})
		return nil
	}
	for _, name := range []string{"config", "custom_apps", "themes"} {
		if e := add("/var/www/html/"+name, "nextcloud/"+name, name == "config"); e != nil {
			return nil, e
		}
	}
	data, e := hostPath(app, text(system["datadirectory"]))
	if e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(data)
	if e != nil {
		return nil, fmt.Errorf("cannot inspect persistent data directory for keys")
	}
	for _, name := range []string{".ocdata", ".htaccess", "index.html", "files_encryption"} {
		p := filepath.Join(data, name)
		if _, e := os.Lstat(p); e == nil {
			prefix := "data-metadata/"
			if name == "files_encryption" {
				prefix = "encryption-keys/data/"
			}
			paths = append(paths, domain.ArchivePath{Source: p, Archive: prefix + name})
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			p := filepath.Join(data, entry.Name(), "files_encryption")
			if _, e := os.Lstat(p); e == nil {
				paths = append(paths, domain.ArchivePath{Source: p, Archive: "encryption-keys/data/" + entry.Name() + "/files_encryption"})
			} else if !os.IsNotExist(e) {
				return nil, e
			}
		}
	}
	var encryption struct {
		Enabled bool `json:"enabled"`
	}
	enabled := r.occJSON(ctx, d, &encryption, "encryption:status", "--output=json") == nil && encryption.Enabled
	keyOutput, keyErr := r.occ(ctx, d, "encryption:show-key-storage-root")
	if enabled || keyErr == nil {
		_, keyRoot, ok := strings.Cut(string(keyOutput), "Current key storage root:")
		keyRoot = strings.TrimSpace(keyRoot)
		if !ok || keyRoot == "" {
			return nil, fmt.Errorf("cannot identify encryption-key storage root")
		}
		if !strings.Contains(keyRoot, "default storage location") {
			p, e := hostPath(app, keyRoot)
			if e != nil {
				return nil, e
			}
			if p != data {
				info, e := os.Stat(p)
				if e != nil || !info.IsDir() {
					return nil, fmt.Errorf("custom encryption-key root is inaccessible")
				}
				paths = append(paths, domain.ArchivePath{Source: p, Archive: "encryption-keys/custom-root"})
			}
		}
	}
	for service, c := range map[string]container{"app": app, "database": database} {
		for index, value := range c.Config.Env {
			key, target, ok := strings.Cut(value, "=")
			if ok && strings.HasSuffix(key, "_FILE") && target != "" {
				p, e := hostPath(c, target)
				if e != nil {
					return nil, e
				}
				info, e := os.Stat(p)
				if e != nil || !info.Mode().IsRegular() {
					return nil, fmt.Errorf("a configured credential file cannot be backed up")
				}
				paths = append(paths, domain.ArchivePath{Source: p, Archive: fmt.Sprintf("deployment/credential-files/%s-%d", service, index)})
			}
		}
	}
	return paths, backup.SafePaths(paths)
}
func (r *Runtime) BackupInput(ctx context.Context, d domain.Deployment) (domain.BackupInput, error) {
	var result domain.BackupInput
	app, e := r.app(ctx, d)
	if e != nil {
		return result, e
	}
	db, e := r.inspect(ctx, d.DatabaseID)
	if e != nil {
		return result, e
	}
	var exported map[string]any
	if e = r.occJSON(ctx, d, &exported, "config:list", "--private"); e != nil {
		return result, e
	}
	system := object(exported["system"])
	if !strings.HasSuffix(text(object(system["objectstore"])["class"]), `\S3`) || text(system["dbname"]) != d.DatabaseName {
		return result, fmt.Errorf("the primary store or database changed during this run")
	}
	paths, e := r.settings(ctx, d, app, db, system)
	if e != nil {
		return result, e
	}
	size, e := r.databaseSize(ctx, d)
	if e != nil {
		return result, e
	}
	effective, e := r.resolved(ctx, d, "")
	if e != nil {
		return result, e
	}
	var inspections json.RawMessage
	if e = r.json(ctx, []string{"docker", "inspect", app.ID, db.ID}, "record recovery containers", &inspections); e != nil {
		return result, e
	}
	var images json.RawMessage
	if e = r.json(ctx, []string{"docker", "image", "inspect", app.Image, d.DatabaseImage}, "record recovery image digests", &images); e != nil {
		return result, e
	}
	result.Paths = paths
	result.EstimatedDatabaseBytes = size
	result.Metadata = map[string]any{"scope": domain.BackupScope, "containers": inspections, "images": images, "effective_compose": effective, "nextcloud_config": exported, "settings_paths": paths}
	return result, nil
}
