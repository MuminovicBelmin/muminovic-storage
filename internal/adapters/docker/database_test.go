package docker

import (
	"context"
	"fmt"
	"io"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/adapters/process"
	"nextcloud-updater/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Compose parsing is real and offline; only container operations are simulated.
func TestDatabaseReplacementChecksShutdownAndUpgradeBeforeStartingApp(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker CLI unavailable")
	}
	for _, clean := range []bool{true, false} {
		t.Run(fmt.Sprint("clean-shutdown-", clean), func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "compose.yml")
			original := "services:\n  db:\n    image: mariadb:lts # original\n    environment:\n      MYSQL_DATABASE: fixture\n    volumes:\n      - db:/var/lib/mysql\n  app:\n    image: nextcloud:35\nvolumes:\n  db:\n"
			os.WriteFile(source, []byte(original), 0600)
			d := domain.Deployment{Root: root, Source: source, Project: "offline-database-fixture", Compose: []string{"docker", "compose"}, DatabaseID: "old-db", DatabaseImage: "old-image", DatabaseName: "fixture"}
			stopped, replaced, checked, appStarted, maintenance := false, false, false, false, true
			recorder := &commands{}
			recorder.handle = func(c process.Command) ([]byte, error) {
				a := strings.Join(c.Args, " ")
				if strings.Contains(a, "config --format json") {
					return (process.Native{}).Run(context.Background(), c)
				}
				if strings.HasPrefix(a, "image inspect") {
					return data([]any{map[string]any{"Id": "new-image", "Config": map[string]any{"Env": []string{"MARIADB_VERSION=1:11.8.9+maria~ubu2404"}}}}), nil
				}
				if strings.HasPrefix(a, "ps ") {
					if strings.Contains(a, "service=db") {
						return []byte("new-db"), nil
					}
					return []byte("app"), nil
				}
				if a == "inspect old-db" {
					code := 0
					if !clean && stopped {
						code = 137
					}
					return data([]any{map[string]any{"Id": "old-db", "Image": "old-image", "State": map[string]any{"Running": !stopped, "ExitCode": code}}}), nil
				}
				if a == "inspect new-db" {
					return data([]any{map[string]any{"Id": "new-db", "Image": "new-image", "State": map[string]any{"Running": true}}}), nil
				}
				if a == "inspect app" {
					return data([]any{map[string]any{"Id": "app", "Image": "app-image", "State": map[string]any{"Running": appStarted}}}), nil
				}
				if strings.Contains(a, "SET GLOBAL innodb_fast_shutdown=0") {
					return nil, nil
				}
				if a == "stop --time -1 old-db" {
					stopped = true
					return nil, nil
				}
				if strings.Contains(a, "up -d --no-deps --pull never db") {
					if !stopped {
						t.Fatal("replacement before shutdown")
					}
					replaced = true
					return nil, nil
				}
				if strings.Contains(a, "SELECT VERSION()") {
					if !strings.Contains(a, "--protocol=tcp") || !replaced {
						t.Fatal("accepted temporary startup server")
					}
					return []byte("11.8.9-MariaDB"), nil
				}
				if strings.Contains(a, "mariadb-upgrade") {
					if !replaced || !strings.Contains(a, "--check-if-upgrade-is-needed") {
						t.Fatal(a)
					}
					return nil, nil
				}
				if strings.Contains(a, "mariadb-check") {
					checked = true
					return nil, nil
				}
				if a == "start app" {
					if !checked {
						t.Fatal("started app before database checks")
					}
					appStarted = true
					return nil, nil
				}
				if strings.Contains(a, "maintenance:mode --off") {
					maintenance = false
					return nil, nil
				}
				if strings.Contains(a, "status --output=json") {
					return data(domain.Status{Installed: true, Version: "35.0.2", Maintenance: maintenance}), nil
				}
				if strings.Contains(a, "background-job:list") {
					return []byte("[]"), nil
				}
				if strings.Contains(a, "php -r") {
					return nil, nil
				}
				return nil, fmt.Errorf("unexpected local fixture command: %s", c.Description)
			}
			r := Runtime{Commands: recorder, Timeout: time.Second}
			hash, _ := filesystem.Hash(source)
			updated, err := r.ApplyDatabase(context.Background(), d, domain.Image{Tag: "mariadb:11.8", ID: "new-image", Version: "11.8.9"}, hash)
			if !clean {
				if err == nil || replaced || appStarted {
					t.Fatal("unclean shutdown accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !appStarted || maintenance || updated.DatabaseID != "new-db" || updated.DatabaseImage != "new-image" {
				t.Fatal("database identity/health not verified")
			}
			out, _ := os.ReadFile(source)
			if !strings.Contains(string(out), "image: mariadb:11.8 # original") || !strings.Contains(string(out), "image: nextcloud:35") || !strings.Contains(string(out), "MARIADB_AUTO_UPGRADE: \"1\"") {
				t.Fatal(string(out))
			}
		})
	}
}

type dumpRecorder struct{ command process.Command }

func (d *dumpRecorder) Run(context.Context, process.Command) ([]byte, error) {
	return nil, fmt.Errorf("unexpected capture")
}
func (d *dumpRecorder) Stream(_ context.Context, c process.Command, w io.Writer) error {
	d.command = c
	_, err := io.WriteString(w, "SQL")
	return err
}
func TestDatabaseDumpIncludesSystemAccountsAndLocksAllTables(t *testing.T) {
	c := &dumpRecorder{}
	r := Runtime{Commands: c, Timeout: time.Minute}
	if err := r.DumpDatabase(context.Background(), domain.Deployment{DatabaseID: "db"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	s := strings.Join(c.command.Args, " ")
	for _, required := range []string{"--all-databases", "--lock-all-tables", "--routines", "--triggers", "--events"} {
		if !strings.Contains(s, required) {
			t.Fatal("missing dump option", required)
		}
	}
	if strings.Contains(s, "--single-transaction") {
		t.Fatal("system tables are not all transactional")
	}
}
