package application_test

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/adapters/backup"
	"nextcloud-updater/internal/application"
	"nextcloud-updater/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseAndNextcloudWorkflowWithIndependentBackups(t *testing.T) {
	d, remote, runs := setup(t, 35)
	d.dbVersion = domain.Version{10, 6, 22}
	root := t.TempDir()
	service := application.LocalService{Remote: &bridge{service: remote}, Backups: backup.Local{}, Log: func(string) {}}
	if err := service.Run(context.Background(), application.LocalOptions{Approved: true, BackupRoot: root}); err != nil {
		t.Fatal(err)
	}
	if d.dbApplied != 3 || d.applied != 6 || d.dumps != 9 || d.dbVersion != (domain.Version{11, 8, 9}) {
		t.Fatal(d.dbApplied, d.applied, d.dumps, d.dbVersion)
	}
	for _, series := range []string{"10.11", "11.4", "11.8"} {
		matches, _ := filepath.Glob(filepath.Join(root, "*", "before-mariadb-"+series, "BACKUP-VERIFIED.json"))
		if len(matches) != 1 {
			t.Fatal("missing database backup", series)
		}
	}
	state, _ := runs.Load()
	if state.Deployment.DatabaseImage != "mariadb:11.8-image" {
		t.Fatal("database identity not rebased", state.Deployment)
	}
}
func TestCorruptDatabaseBackupNeverReplacesDatabase(t *testing.T) {
	d, remote, _ := setup(t, 35)
	d.dbVersion = domain.Version{10, 11, 8}
	service := application.LocalService{Remote: &bridge{service: remote, corrupt: true}, Backups: backup.Local{}, Log: func(string) {}}
	if err := service.Run(context.Background(), application.LocalOptions{Approved: true, BackupRoot: t.TempDir()}); err == nil {
		t.Fatal("corruption ignored")
	}
	if d.dbApplied != 0 || d.applied != 0 || d.restarted != 1 {
		t.Fatal("unsafe backup failure")
	}
}
func TestDatabaseFailureRetainsLockAndDoesNotRestartOldEngine(t *testing.T) {
	d, remote, runs := setup(t, 35)
	d.dbVersion = domain.Version{10, 11, 8}
	d.verifyError = fmt.Errorf("engine migration failed")
	service := application.LocalService{Remote: &bridge{service: remote}, Backups: backup.Local{}, Log: func(string) {}}
	if err := service.Run(context.Background(), application.LocalOptions{Approved: true, BackupRoot: t.TempDir()}); err == nil {
		t.Fatal("migration failure ignored")
	}
	state, _ := runs.Load()
	if state.Kind != "mariadb" || state.Phase != domain.DeploymentStarted || runs.RequireLock() != nil || d.restarted != 0 || d.applied != 0 || !d.stopped {
		t.Fatal("unsafe recovery", state.Phase)
	}
	if _, err := os.Stat(filepath.Join(state.Stage, "manifest.json")); err != nil {
		t.Fatal("remote recovery backup lost", err)
	}
}
func TestDatabaseDirectPrepareRequiresApprovalAndCompatibleStep(t *testing.T) {
	d, remote, _ := setup(t, 35)
	ctx := context.Background()
	if _, err := remote.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.PrepareDatabase(ctx, "11.4", false); err == nil {
		t.Fatal("approval bypass")
	}
	if _, err := remote.PrepareDatabase(ctx, "11.8", true); err == nil {
		t.Fatal("incompatible database upgrade accepted")
	}
	if d.dbApplied+d.dumps != 0 {
		t.Fatal("database changed")
	}
}
