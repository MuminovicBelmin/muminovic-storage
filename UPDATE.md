# Updating Muminovic Cloud

This updater is specific to this repository's Docker Compose stack. It upgrades the
`app` and `cron` services together through Nextcloud 33, 34, and 35, and can advance
MariaDB through its reviewed LTS path. It is deliberately not compatible with the
separate `NextCloud_server` deployment.

Build the coordinator and Linux helpers on Windows:

```powershell
go run ./cmd/build
```

Set the deployment connection values for the current shell. They are intentionally
not stored in the repository or the launcher:

```powershell
$env:MUMINOVIC_CLOUD_HOST = 'server.example'
$env:MUMINOVIC_CLOUD_COMPOSE_DIR = '/path/to/muminovic_cloud'
# Optional; defaults are root and port 22.
$env:MUMINOVIC_CLOUD_USER = 'root'
$env:MUMINOVIC_CLOUD_PORT = '22'
```

Run a non-mutating preflight first:

```powershell
.\update-remote.bat --check-only
```

Only after reviewing the plan and ensuring local backup space is available, run the
approved update:

```powershell
.\update-remote.bat --approve-update
```

The default target is Nextcloud 35. To stop at an earlier supported release, use
`--target-major 34`. `--backup-root 'E:\Nextcloud-backups'` selects a different
local backup location.

## Required deployed layout

The remote Compose project must have exactly these services: `app`, `db`, `cron`,
`valkey`, and `tmp_init`. Its `.env` must contain single-line
`NEXTCLOUD_IMAGE=` and `MARIADB_IMAGE=` assignments, because the updater changes
those variables rather than replacing `docker-compose.yml`. The runtime Compose
configuration and persistent mounts must match the running containers.

The MariaDB command must remain the reviewed command in this repository. Other
database startup commands, entrypoints, extra database mounts, replication, binary
logging, recovery mode, unsupported storage engines, and extra writers are refused
for manual review.

## Backup and service behavior

Before every Nextcloud or MariaDB release change, the updater enables maintenance
mode, stops `cron` and `app`, then creates a locked all-databases SQL dump and a
settings archive. The archive includes Nextcloud configuration, custom apps, themes,
encryption keys where present, credential files mounted into the application or
database, and recovery metadata. It is copied to the local backup directory and
must pass integrity validation before an image variable is changed.

S3 object contents are not downloaded or manipulated. A SQL/configuration backup
cannot restore deleted or corrupted S3 objects. Do not run competing cron, storage,
or update tasks during the maintenance window.

For a Nextcloud update, the updater changes `NEXTCLOUD_IMAGE`, recreates `app`,
verifies migrations and local health, and only then recreates `cron` with the same
image. For a MariaDB update, it pins `MARIADB_IMAGE` to the reviewed series,
performs a clean shutdown, and uses the official image's system-table upgrade.

If a migration has started and fails, the updater leaves maintenance mode and its
state directory in place; it does not automatically downgrade. Keep the local
backup and inspect the recorded remote run state before attempting recovery.
