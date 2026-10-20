package application_test

import (
	"context"
	"fmt"
	"io"
	"nextcloud-updater/internal/adapters/backup"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/application"
	"nextcloud-updater/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These fakes replace deployment/transport only. SQL compression, archives, hashes,
// receipt validation and durable run state use the real filesystem adapters.
type deployment struct {
	info                      domain.Deployment
	status                    domain.Status
	image                     string
	stopped                   bool
	applied, restarted, dumps int
	verifyError               error
	disabled                  bool
	events                    []string
	dbVersion                 domain.Version
	dbApplied                 int
}

func (d *deployment) Discover(context.Context, string) (domain.Deployment, error) { return d.info, nil }
func (d *deployment) Status(context.Context, domain.Deployment) (domain.Status, string, error) {
	return d.status, d.image, nil
}
func (d *deployment) CheckDatabase(context.Context, domain.Deployment) error { return nil }
func (d *deployment) DatabaseVersion(context.Context, domain.Deployment) (domain.Version, error) {
	if d.dbVersion == (domain.Version{}) {
		return domain.Version{11, 4, 9}, nil
	}
	return d.dbVersion, nil
}
func (d *deployment) ValidateDatabaseImage(context.Context, domain.Deployment, string) error {
	return nil
}
func (d *deployment) PullDatabase(_ context.Context, tag string) (domain.Image, error) {
	series, err := domain.DatabaseImage(tag)
	if err != nil {
		return domain.Image{}, err
	}
	return domain.Image{Tag: tag, ID: tag + "-image", Version: series + ".9"}, nil
}
func (d *deployment) ApplyDatabase(_ context.Context, info domain.Deployment, image domain.Image, _ string) (domain.Deployment, error) {
	if !d.stopped || !d.status.Maintenance {
		return info, fmt.Errorf("database changed while app was writing")
	}
	d.dbApplied++
	d.events = append(d.events, "database-apply")
	if d.verifyError != nil {
		return info, d.verifyError
	}
	d.dbVersion, _ = domain.ParseVersion(image.Version)
	info.DatabaseID, info.DatabaseImage = image.ID+"-container", image.ID
	d.stopped, d.status.Maintenance = false, false
	return info, nil
}
func (d *deployment) SourceHash(info domain.Deployment) (string, error) {
	return filesystem.Hash(info.Source)
}
func (d *deployment) ValidateImage(context.Context, domain.Deployment, string) error { return nil }
func (d *deployment) Pull(_ context.Context, tag string) (domain.Image, error) {
	major, _ := domain.ImageMajor(tag)
	return domain.Image{Tag: tag, ID: tag + "-image", Version: fmt.Sprintf("%d.0.17", major)}, nil
}
func (d *deployment) EnabledApps(context.Context, domain.Deployment) ([]string, error) {
	if d.disabled && d.applied > 0 {
		return []string{}, nil
	}
	return []string{"testapp"}, nil
}
func (d *deployment) BackupInput(context.Context, domain.Deployment) (domain.BackupInput, error) {
	return domain.BackupInput{Paths: []domain.ArchivePath{{Source: filepath.Join(d.info.Root, "config"), Archive: "nextcloud/config"}}, EstimatedDatabaseBytes: 1024, Metadata: map[string]string{"scope": "database/settings; no Swift files"}}, nil
}
func (d *deployment) SetMaintenance(_ context.Context, _ domain.Deployment, on bool) error {
	d.status.Maintenance = on
	d.events = append(d.events, fmt.Sprint("maintenance:", on))
	return nil
}
func (d *deployment) StopApp(context.Context, domain.Deployment) error {
	d.stopped = true
	d.events = append(d.events, "stop")
	return nil
}
func (d *deployment) StartOriginal(context.Context, domain.Deployment) error {
	d.restarted++
	d.stopped = false
	d.status.Maintenance = false
	return nil
}
func (d *deployment) Apply(_ context.Context, _ domain.Deployment, image domain.Image, _ string) error {
	d.applied++
	d.image = image.ID
	d.status.Version = image.Version
	d.stopped = false
	d.events = append(d.events, "apply")
	return nil
}
func (d *deployment) Verify(context.Context, domain.Deployment, domain.Image) (domain.Status, error) {
	if d.verifyError != nil {
		return d.status, d.verifyError
	}
	d.status.Maintenance = false
	return d.status, nil
}
func (d *deployment) DumpDatabase(_ context.Context, _ domain.Deployment, w io.Writer) error {
	if !d.stopped || !d.status.Maintenance {
		return fmt.Errorf("writers were not quiesced before dump")
	}
	d.events = append(d.events, "dump")
	d.dumps++
	_, e := io.WriteString(w, "-- SQL fixture\nCREATE TABLE test (id INT);\n")
	return e
}

type bridge struct {
	service *application.RemoteService
	corrupt bool
}

func (b *bridge) Open(context.Context, string, map[string]string) error { return nil }
func (b *bridge) Plan(ctx context.Context) (domain.Plan, error)         { return b.service.Plan(ctx) }
func (b *bridge) Prepare(ctx context.Context, n int) (domain.Step, error) {
	return b.service.Prepare(ctx, n, true)
}
func (b *bridge) PrepareDatabase(ctx context.Context, series string) (domain.Step, error) {
	return b.service.PrepareDatabase(ctx, series, true)
}
func (b *bridge) Backup(ctx context.Context) (domain.Backup, error) {
	return b.service.Backup(ctx, true)
}
func (b *bridge) Deploy(ctx context.Context, receipt string) (domain.Result, error) {
	return b.service.Deploy(ctx, receipt, true)
}
func (b *bridge) Finish(context.Context) error { return b.service.Finish() }
func (b *bridge) Recover(ctx context.Context) (domain.Recovery, error) {
	return b.service.Recover(ctx, true)
}
func (b *bridge) Download(_ context.Context, remote, local string) error {
	data, e := os.ReadFile(remote)
	if e != nil {
		return e
	}
	if b.corrupt && strings.HasSuffix(remote, "database.sql.gz") {
		data = []byte("corrupt transfer")
	}
	return os.WriteFile(local, data, 0600)
}
func setup(t *testing.T, target int) (*deployment, *application.RemoteService, *filesystem.Runs) {
	t.Helper()
	root := t.TempDir()
	run := filepath.Join(root, ".nextcloud-updater", "test")
	os.MkdirAll(run, 0700)
	os.Mkdir(filepath.Join(root, "config"), 0700)
	os.WriteFile(filepath.Join(root, "config", "config.php"), []byte("private configuration"), 0600)
	source := filepath.Join(root, "compose.yml")
	os.WriteFile(source, []byte("services:\n  nextcloud:\n    image: nextcloud:30\n"), 0600)
	runs, e := filesystem.NewRuns(root, run)
	if e != nil {
		t.Fatal(e)
	}
	d := &deployment{info: domain.Deployment{Root: root, Source: source, DatabaseImage: "mariadb:11.4-image"}, status: domain.Status{Version: "30.0.8.1", Installed: true}, image: "original-image"}
	images := map[string]string{}
	for n := 30; n <= target; n++ {
		images[fmt.Sprint(n)] = fmt.Sprintf("nextcloud:%d", n)
	}
	s := &application.RemoteService{Deployment: d, Backups: &backup.Remote{Dumper: d, RunDirectory: run}, Runs: runs, Root: root, Target: target, Images: images}
	return d, s, runs
}
func TestCompleteWorkflowKeepsIndependentVerifiedLocalBackups(t *testing.T) {
	d, s, runs := setup(t, 31)
	b := &bridge{service: s}
	localRoot := t.TempDir()
	service := application.LocalService{Remote: b, Backups: backup.Local{}, Log: func(string) {}}
	if e := service.Run(context.Background(), application.LocalOptions{Approved: true, BackupRoot: localRoot}); e != nil {
		t.Fatal(e)
	}
	if d.dumps != 2 || d.applied != 2 || d.restarted != 0 {
		t.Fatal(d.dumps, d.applied, d.restarted)
	}
	state, e := runs.Load()
	if e != nil || state.Phase != domain.Closed {
		t.Fatal(state, e)
	}
	if runs.RequireLock() == nil {
		t.Fatal("lock was not released")
	}
	for _, major := range []int{30, 31} {
		matches, _ := filepath.Glob(filepath.Join(localRoot, "*", fmt.Sprintf("before-%d", major), "BACKUP-VERIFIED.json"))
		if len(matches) != 1 {
			t.Fatal("missing independent verified backup", major)
		}
	}
}
func TestCorruptDownloadPreventsDeploymentAndRestartsOriginal(t *testing.T) {
	d, s, runs := setup(t, 31)
	service := application.LocalService{Remote: &bridge{service: s, corrupt: true}, Backups: backup.Local{}, Log: func(string) {}}
	e := service.Run(context.Background(), application.LocalOptions{Approved: true, BackupRoot: t.TempDir()})
	if e == nil {
		t.Fatal("corrupt download accepted")
	}
	if d.applied != 0 || d.restarted != 1 || d.stopped || d.status.Maintenance {
		t.Fatalf("unsafe recovery: %+v", d)
	}
	if runs.RequireLock() == nil {
		t.Fatal("backup-only failure retained lock")
	}
}
func TestFailedMigrationNeverDowngradesAndRetainsLock(t *testing.T) {
	d, s, runs := setup(t, 31)
	d.verifyError = fmt.Errorf("migration failure")
	service := application.LocalService{Remote: &bridge{service: s}, Backups: backup.Local{}, Log: func(string) {}}
	e := service.Run(context.Background(), application.LocalOptions{Approved: true, BackupRoot: t.TempDir()})
	if e == nil {
		t.Fatal("failure ignored")
	}
	if d.applied != 1 || d.restarted != 0 || !d.status.Maintenance {
		t.Fatal("unsafe automatic rollback")
	}
	state, _ := runs.Load()
	if state.Phase != domain.DeploymentStarted || runs.RequireLock() != nil {
		t.Fatal("migration evidence/lock lost")
	}
}
func TestDisabledAppsStopFurtherMajorsAtHealthyVersion(t *testing.T) {
	d, s, runs := setup(t, 32)
	d.disabled = true
	service := application.LocalService{Remote: &bridge{service: s}, Backups: backup.Local{}, Log: func(string) {}}
	e := service.Run(context.Background(), application.LocalOptions{Approved: true, BackupRoot: t.TempDir()})
	if e == nil || !strings.Contains(e.Error(), "testapp") {
		t.Fatal(e)
	}
	if d.applied != 1 || d.status.Maintenance {
		t.Fatal("did not stop at a healthy version")
	}
	state, _ := runs.Load()
	if state.Phase != domain.Closed {
		t.Fatal(state.Phase)
	}
}
func TestRemoteApprovalCannotBeBypassedByDirectActions(t *testing.T) {
	d, s, runs := setup(t, 31)
	if e := runs.Acquire(); e != nil {
		t.Fatal(e)
	}
	runs.Save(domain.State{Phase: domain.Ready, Deployment: d.info})
	if _, e := s.Prepare(context.Background(), 31, false); e == nil {
		t.Fatal("prepare without approval")
	}
	runs.Save(domain.State{Phase: domain.Prepared, Deployment: d.info})
	if _, e := s.Backup(context.Background(), false); e == nil {
		t.Fatal("backup without approval")
	}
	runs.Save(domain.State{Phase: domain.BackedUp, Deployment: d.info})
	if _, e := s.Deploy(context.Background(), "receipt", false); e == nil {
		t.Fatal("deploy without approval")
	}
	if d.applied+d.dumps != 0 {
		t.Fatal("approval guard ran operations")
	}
}
func TestWrongReceiptPreventsAnyDeployment(t *testing.T) {
	d, s, _ := setup(t, 31)
	ctx := context.Background()
	if _, e := s.Plan(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Prepare(ctx, 30, true); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Backup(ctx, true); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Deploy(ctx, "incorrect", true); e == nil {
		t.Fatal("incorrect receipt accepted")
	}
	if d.applied != 0 {
		t.Fatal("deployment ran")
	}
}
func TestSkippingMajorIsRejectedBeforePull(t *testing.T) {
	_, s, _ := setup(t, 35)
	ctx := context.Background()
	s.Plan(ctx)
	if _, e := s.Prepare(ctx, 32, true); e == nil {
		t.Fatal("major skip accepted")
	}
}
func TestPreflightDoesNotQuiesceOrDump(t *testing.T) {
	d, s, _ := setup(t, 31)
	service := application.LocalService{Remote: &bridge{service: s}, Backups: backup.Local{}, Log: func(string) {}}
	if e := service.Run(context.Background(), application.LocalOptions{CheckOnly: true, BackupRoot: t.TempDir()}); e != nil {
		t.Fatal(e)
	}
	if len(d.events) != 0 || d.applied != 0 || d.dumps != 0 {
		t.Fatal("preflight changed deployment")
	}
}
