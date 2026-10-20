package docker

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/domain"
	"strings"
	"time"
)

var reviewedDatabaseCommand = []string{
	"--transaction-isolation=READ-COMMITTED",
	"--binlog-format=ROW",
	"--character-set-server=utf8mb4",
	"--collation-server=utf8mb4_unicode_ci",
	"--skip-name-resolve",
}

func isReviewedDatabaseCommand(value any) bool {
	command := list(value)
	if len(command) != len(reviewedDatabaseCommand) {
		return false
	}
	for i, expected := range reviewedDatabaseCommand {
		if text(command[i]) != expected {
			return false
		}
	}
	return true
}

func (r *Runtime) DatabaseVersion(ctx context.Context, d domain.Deployment) (domain.Version, error) {
	shell := `client=$(command -v mariadb || command -v mysql)
exec "$client" --user=root --batch --skip-column-names --execute='SELECT VERSION()'
`
	b, err := r.run(ctx, r.databaseCommand(d, shell), "read MariaDB server version")
	if err != nil {
		return domain.Version{}, err
	}
	return domain.DatabaseVersion(strings.TrimSpace(string(b)))
}
func (r *Runtime) ValidateDatabaseImage(ctx context.Context, d domain.Deployment, tag string) error {
	if _, err := domain.DatabaseImage(tag); err != nil {
		return err
	}
	config, err := r.resolved(ctx, d, "")
	if err != nil {
		return err
	}
	service := object(object(config["services"])[databaseService])
	if !isReviewedDatabaseCommand(service["command"]) || service["entrypoint"] != nil {
		return fmt.Errorf("MariaDB startup options differ from the reviewed muminovic_cloud configuration")
	}
	c, err := r.inspect(ctx, d.DatabaseID)
	if err != nil {
		return err
	}
	if env(c.Config.Env)["MARIADB_DISABLE_UPGRADE_BACKUP"] != "" {
		return fmt.Errorf("MariaDB system-table upgrade backups must remain enabled")
	}
	// Custom database configuration and replication/cluster settings are outside
	// the reviewed single-server path. Refuse rather than silently losing them.
	for _, m := range c.Mounts {
		if m.Destination != "/var/lib/mysql" && !strings.HasPrefix(m.Destination, "/run/secrets/") {
			return fmt.Errorf("custom MariaDB mounts require manual upgrade review")
		}
	}
	shell := `client=$(command -v mariadb || command -v mysql)
exec "$client" --user=root --batch --skip-column-names --execute="SELECT @@global.wsrep_on, @@global.innodb_force_recovery, @@global.log_bin; SHOW ALL SLAVES STATUS; SELECT COUNT(*) FROM information_schema.tables WHERE table_schema NOT IN ('information_schema','performance_schema','sys') AND engine NOT IN ('InnoDB','Aria','MyISAM','MEMORY','CSV')"
`
	b, err := r.run(ctx, r.databaseCommand(d, shell), "check standalone MariaDB upgrade prerequisites")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(b))
	if len(fields) != 4 || fields[0] != "0" || fields[1] != "0" || fields[2] != "0" || fields[3] != "0" {
		return fmt.Errorf("replication, binary logging, recovery mode or unreviewed storage engines require manual assessment")
	}
	_, err = r.serviceCandidate(ctx, d, tag, true)
	return err
}
func (r *Runtime) databaseImage(ctx context.Context, tag string) (domain.Image, error) {
	series, err := domain.DatabaseImage(tag)
	if err != nil {
		return domain.Image{}, err
	}
	var rows []struct {
		ID     string `json:"Id"`
		Config struct{ Env []string }
	}
	if err = r.json(ctx, []string{"docker", "image", "inspect", tag}, "inspect MariaDB image", &rows); err != nil {
		return domain.Image{}, err
	}
	if len(rows) != 1 {
		return domain.Image{}, fmt.Errorf("expected one MariaDB image")
	}
	raw := env(rows[0].Config.Env)["MARIADB_VERSION"]
	// Debian package versions include an epoch, e.g. 1:11.8.9+maria~ubu2404.
	if _, remainder, ok := strings.Cut(raw, ":"); ok {
		raw = remainder
	}
	v, err := domain.DatabaseVersion(raw + "-MariaDB")
	if err != nil || domain.DatabaseSeries(v) != series {
		return domain.Image{}, fmt.Errorf("image does not identify the intended MariaDB release")
	}
	return domain.Image{Tag: tag, ID: rows[0].ID, Version: v.String()}, nil
}
func (r *Runtime) PullDatabase(ctx context.Context, tag string) (domain.Image, error) {
	if _, err := domain.DatabaseImage(tag); err != nil {
		return domain.Image{}, err
	}
	if _, err := r.Commands.Run(ctx, r.command([]string{"docker", "pull", tag}, "pull reviewed MariaDB image", true)); err != nil {
		return domain.Image{}, err
	}
	return r.databaseImage(ctx, tag)
}
func (r *Runtime) ApplyDatabase(ctx context.Context, d domain.Deployment, image domain.Image, expectedHash string) (domain.Deployment, error) {
	if err := r.CheckDatabase(ctx, d); err != nil {
		return d, err
	}
	hash, err := r.SourceHash(d)
	if err != nil {
		return d, err
	}
	if hash != expectedHash {
		return d, fmt.Errorf("Compose source changed after database backup")
	}
	actual, err := r.databaseImage(ctx, image.Tag)
	if err != nil {
		return d, err
	}
	if actual.ID != image.ID || actual.Version != image.Version {
		return d, fmt.Errorf("MariaDB image changed after backup")
	}
	app, err := r.app(ctx, d)
	if err != nil {
		return d, err
	}
	if app.State.Running {
		return d, fmt.Errorf("Nextcloud writers must be stopped before MariaDB replacement")
	}
	candidate, err := r.serviceCandidate(ctx, d, image.Tag, true)
	if err != nil {
		return d, err
	}
	// A full InnoDB shutdown avoids carrying recovery logs across engine versions.
	shell := `client=$(command -v mariadb || command -v mysql)
exec "$client" --user=root --execute='SET GLOBAL innodb_fast_shutdown=0'
`
	if _, err = r.run(ctx, r.databaseCommand(d, shell), "prepare clean MariaDB shutdown"); err != nil {
		return d, err
	}
	if _, err = r.Commands.Run(ctx, r.command([]string{"docker", "stop", "--time", "-1", d.DatabaseID}, "cleanly stop MariaDB", true)); err != nil {
		return d, err
	}
	stopped, err := r.inspect(ctx, d.DatabaseID)
	if err != nil {
		return d, err
	}
	if stopped.State.Running || stopped.State.ExitCode != 0 || stopped.State.OOMKilled {
		return d, fmt.Errorf("MariaDB did not shut down cleanly; refusing replacement")
	}
	target := d.Environment
	if target == "" {
		target = d.Source
	}
	if err = filesystem.AtomicWrite(target, candidate, 0600, true); err != nil {
		return d, err
	}
	if _, err = r.Commands.Run(ctx, r.command(r.compose(d, "", "up", "-d", "--no-deps", "--pull", "never", databaseService), "replace MariaDB only and upgrade system tables", true)); err != nil {
		return d, err
	}
	id, err := r.find(ctx, d.Project, databaseService, false)
	if err != nil {
		return d, err
	}
	d.DatabaseID, d.DatabaseImage = id, image.ID
	err = wait(ctx, r.Timeout, 5*time.Second, func(ctx context.Context) (bool, error) {
		if r.CheckDatabase(ctx, d) != nil {
			return false, nil
		}
		// The official entrypoint uses a socket-only temporary server while
		// upgrading. Require TCP so this cannot accept that temporary server.
		shell := `client=$(command -v mariadb || command -v mysql)
exec "$client" --protocol=tcp --host=127.0.0.1 --user=root --batch --skip-column-names --execute='SELECT VERSION()'
`
		b, err := r.run(ctx, r.databaseCommand(d, shell), "wait for upgraded MariaDB TCP service")
		if err != nil {
			return false, nil
		}
		v, err := domain.DatabaseVersion(strings.TrimSpace(string(b)))
		return err == nil && v.String() == image.Version, nil
	})
	if err != nil {
		return d, err
	}
	shell = `exec mariadb-upgrade --user=root --check-if-upgrade-is-needed`
	// Exit 0 means an upgrade is still needed; exit 1 means it is current.
	shell = "set +e\n" + strings.TrimPrefix(shell, "exec ") + "\ncode=$?\n[ \"$code\" -eq 1 ]\n"
	if _, err = r.run(ctx, r.databaseCommand(d, shell), "verify MariaDB system-table upgrade completion"); err != nil {
		return d, err
	}
	if _, err = r.Commands.Run(ctx, r.command(r.databaseCommand(d, `exec mariadb-check --user=root --all-databases --check-upgrade`), "check all MariaDB tables after engine upgrade", true)); err != nil {
		return d, err
	}
	if err = r.StartOriginal(ctx, d); err != nil {
		return d, err
	}
	status, appID, err := r.Status(ctx, d)
	if err != nil {
		return d, err
	}
	if !status.Healthy() || appID != app.Image {
		return d, fmt.Errorf("Nextcloud health or image changed during database upgrade")
	}
	_, err = r.Verify(ctx, d, domain.Image{ID: app.Image, Version: status.Version})
	return d, err
}
