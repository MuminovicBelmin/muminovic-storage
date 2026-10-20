package application

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/domain"
	"sort"
	"strconv"
)

type RemoteService struct {
	Deployment DeploymentPort
	Backups    BackupPort
	Runs       RunStore
	Root       string
	Images     map[string]string
	Target     int
}

func (s *RemoteService) Plan(ctx context.Context) (domain.Plan, error) {
	if err := s.Runs.Acquire(); err != nil {
		return domain.Plan{}, err
	}
	state := domain.State{Phase: domain.Planning}
	if err := s.Runs.Save(state); err != nil {
		return domain.Plan{}, err
	}
	d, err := s.Deployment.Discover(ctx, s.Root)
	if err != nil {
		return domain.Plan{}, err
	}
	state.Deployment = d
	if err = s.Runs.Save(state); err != nil {
		return domain.Plan{}, err
	}
	status, _, err := s.Deployment.Status(ctx, d)
	if err != nil {
		return domain.Plan{}, err
	}
	if !status.Healthy() {
		return domain.Plan{}, fmt.Errorf("Nextcloud must be healthy before starting")
	}
	v, err := domain.ParseVersion(status.Version)
	if err != nil {
		return domain.Plan{}, err
	}
	if v[0] > s.Target {
		return domain.Plan{}, fmt.Errorf("requested target would downgrade the server")
	}
	plan := domain.Plan{CurrentVersion: status.Version, Scope: domain.BackupScope, Stages: []int{}}
	dbv, err := s.Deployment.DatabaseVersion(ctx, d)
	if err != nil {
		return plan, err
	}
	plan.DatabaseVersion = dbv.String()
	plan.DatabaseBefore, plan.DatabaseAfter, err = domain.DatabasePath(dbv, v[0], s.Target)
	if err != nil {
		return plan, err
	}
	for _, series := range append(append([]string{}, plan.DatabaseBefore...), plan.DatabaseAfter...) {
		if err = s.Deployment.ValidateDatabaseImage(ctx, d, "mariadb:"+series); err != nil {
			return plan, err
		}
	}
	for n := v[0]; n <= s.Target; n++ {
		imageMajor, imageErr := domain.ImageMajor(s.Images[strconv.Itoa(n)])
		if imageErr != nil || imageMajor != n {
			return plan, fmt.Errorf("missing major-version image choice: %d", n)
		}
		plan.Stages = append(plan.Stages, n)
	}
	input, err := s.Deployment.BackupInput(ctx, d)
	if err != nil {
		return plan, err
	}
	plan.EstimatedDatabaseBytes = input.EstimatedDatabaseBytes
	if err = s.Deployment.ValidateImage(ctx, d, s.Images[strconv.Itoa(v[0])]); err != nil {
		return plan, err
	}
	state.Phase = domain.Ready
	return plan, s.Runs.Save(state)
}
func (s *RemoteService) load(action string, approved bool) (domain.State, error) {
	state, err := s.Runs.Load()
	if err != nil {
		return state, err
	}
	if err = domain.RequireApproval(action, approved, state.Phase); err != nil {
		return state, err
	}
	return state, s.Runs.RequireLock()
}
func (s *RemoteService) Prepare(ctx context.Context, major int, approved bool) (domain.Step, error) {
	state, err := s.load("prepare", approved)
	if err != nil {
		return domain.Step{}, err
	}
	if state.Phase != domain.Ready && state.Phase != domain.Verified {
		return domain.Step{}, fmt.Errorf("previous step is incomplete")
	}
	d := state.Deployment
	if err = s.Deployment.CheckDatabase(ctx, d); err != nil {
		return domain.Step{}, err
	}
	status, currentImage, err := s.Deployment.Status(ctx, d)
	if err != nil {
		return domain.Step{}, err
	}
	if !status.Healthy() {
		return domain.Step{}, fmt.Errorf("Nextcloud is not healthy")
	}
	current, err := domain.ParseVersion(status.Version)
	if err != nil {
		return domain.Step{}, err
	}
	if major != current[0] && major != current[0]+1 {
		return domain.Step{}, fmt.Errorf("skipping major releases is prohibited")
	}
	dbv, err := s.Deployment.DatabaseVersion(ctx, d)
	if err != nil {
		return domain.Step{}, err
	}
	if !domain.DatabaseCompatible(dbv, major) {
		return domain.Step{}, fmt.Errorf("MariaDB is incompatible with the proposed Nextcloud major")
	}
	tag := s.Images[strconv.Itoa(major)]
	if imageMajor, imageErr := domain.ImageMajor(tag); imageErr != nil || imageMajor != major {
		return domain.Step{}, fmt.Errorf("invalid image choice")
	}
	image, err := s.Deployment.Pull(ctx, tag)
	if err != nil {
		return domain.Step{}, err
	}
	target, err := domain.ParseVersion(image.Version)
	if err != nil {
		return domain.Step{}, err
	}
	if err = domain.ValidateStep(current, target, major); err != nil {
		return domain.Step{}, err
	}
	step := domain.Step{FromVersion: status.Version, TargetVersion: image.Version}
	if current == target && image.ID == currentImage {
		step.Skip = true
		return step, nil
	}
	if err = s.Deployment.ValidateImage(ctx, d, tag); err != nil {
		return step, err
	}
	stage, err := s.Runs.Stage(strconv.Itoa(major))
	if err != nil {
		return step, err
	}
	apps, err := s.Deployment.EnabledApps(ctx, d)
	if err != nil {
		return step, err
	}
	hash, err := s.Deployment.SourceHash(d)
	if err != nil {
		return step, err
	}
	state.Phase = domain.Prepared
	state.Kind = "nextcloud"
	state.Stage = stage
	state.FromVersion = status.Version
	state.Target = image
	state.EnabledApps = apps
	state.SourceHash = hash
	return step, s.Runs.Save(state)
}
func (s *RemoteService) Backup(ctx context.Context, approved bool) (domain.Backup, error) {
	state, err := s.load("backup", approved)
	if err != nil {
		return domain.Backup{}, err
	}
	if state.Phase != domain.Prepared {
		return domain.Backup{}, fmt.Errorf("prepare the upgrade first")
	}
	d := state.Deployment
	if err = s.Deployment.CheckDatabase(ctx, d); err != nil {
		return domain.Backup{}, err
	}
	hash, err := s.Deployment.SourceHash(d)
	if err != nil {
		return domain.Backup{}, err
	}
	if hash != state.SourceHash {
		return domain.Backup{}, fmt.Errorf("Compose source changed")
	}
	input, err := s.Deployment.BackupInput(ctx, d)
	if err != nil {
		return domain.Backup{}, err
	}
	if err = s.Backups.CheckSpace(state.Stage, input); err != nil {
		return domain.Backup{}, err
	}
	state.Phase = domain.Quiescing
	if err = s.Runs.Save(state); err != nil {
		return domain.Backup{}, err
	}
	if err = s.Deployment.SetMaintenance(ctx, d, true); err != nil {
		return domain.Backup{}, err
	}
	if err = s.Deployment.StopApp(ctx, d); err != nil {
		return domain.Backup{}, err
	}
	state.Phase = domain.BackingUp
	if err = s.Runs.Save(state); err != nil {
		return domain.Backup{}, err
	}
	input.Kind = state.Kind
	backup, err := s.Backups.Create(ctx, d, input, state.Stage, state.FromVersion, state.Target.Version)
	if err != nil {
		return backup, err
	}
	state.Phase = domain.BackedUp
	state.Backup = backup
	return backup, s.Runs.Save(state)
}
func (s *RemoteService) Deploy(ctx context.Context, receipt string, approved bool) (domain.Result, error) {
	state, err := s.load("deploy", approved)
	if err != nil {
		return domain.Result{}, err
	}
	if state.Phase != domain.BackedUp || receipt == "" || receipt != state.Backup.SHA256 {
		return domain.Result{}, fmt.Errorf("a locally verified backup receipt is required")
	}
	if err = s.Backups.Verify(state.Backup); err != nil {
		return domain.Result{}, err
	}
	d := state.Deployment
	if err = s.Deployment.CheckDatabase(ctx, d); err != nil {
		return domain.Result{}, err
	}
	hash, err := s.Deployment.SourceHash(d)
	if err != nil {
		return domain.Result{}, err
	}
	if hash != state.SourceHash {
		return domain.Result{}, fmt.Errorf("Compose source changed after backup")
	}
	if err = s.validateTarget(ctx, state); err != nil {
		return domain.Result{}, err
	}
	state.Phase = domain.DeploymentStarted
	if err = s.Runs.Save(state); err != nil {
		return domain.Result{}, err
	}
	var status domain.Status
	if state.Kind == "mariadb" {
		d, err = s.Deployment.ApplyDatabase(ctx, d, state.Target, state.SourceHash)
		if err != nil {
			return domain.Result{}, err
		}
		state.Deployment = d
		if err = s.Runs.Save(state); err != nil {
			return domain.Result{}, err
		}
		status, _, err = s.Deployment.Status(ctx, d)
	} else {
		if err = s.Deployment.Apply(ctx, d, state.Target, state.SourceHash); err != nil {
			return domain.Result{}, err
		}
		status, err = s.Deployment.Verify(ctx, d, state.Target)
	}
	if err != nil {
		return domain.Result{}, err
	}
	return s.completeDeployment(ctx, state, status)
}
func (s *RemoteService) completeDeployment(ctx context.Context, state domain.State, status domain.Status) (domain.Result, error) {
	d := state.Deployment
	var err error
	if err = s.Deployment.CheckDatabase(ctx, d); err != nil {
		return domain.Result{}, err
	}
	if !status.Healthy() {
		return domain.Result{}, fmt.Errorf("post-upgrade status is unhealthy")
	}
	apps, err := s.Deployment.EnabledApps(ctx, d)
	if err != nil {
		return domain.Result{}, err
	}
	result := domain.Result{Version: status.Version, ComposeFile: d.Source, DisabledApps: []string{}}
	if state.Kind == "mariadb" {
		result.DatabaseVersion = state.Target.Version
	}
	enabled := map[string]bool{}
	for _, a := range apps {
		enabled[a] = true
	}
	for _, a := range state.EnabledApps {
		if !enabled[a] {
			result.DisabledApps = append(result.DisabledApps, a)
		}
	}
	sort.Strings(result.DisabledApps)
	state.Phase = domain.Verified
	if err = s.Runs.Save(state); err != nil {
		return result, err
	}
	return result, s.Backups.Clear(state.Stage)
}
func (s *RemoteService) Finish() error {
	state, err := s.Runs.Load()
	if err != nil {
		return err
	}
	if err = s.Runs.RequireLock(); err != nil {
		return err
	}
	if state.Phase != domain.Ready && state.Phase != domain.Verified {
		return fmt.Errorf("deployment has not reached a verified state")
	}
	state.Phase = domain.Closed
	if err = s.Runs.Save(state); err != nil {
		return err
	}
	return s.Runs.Release()
}
func (s *RemoteService) Recover(ctx context.Context, approved bool) (domain.Recovery, error) {
	state, err := s.Runs.Load()
	if err != nil {
		return domain.Recovery{}, err
	}
	if err = domain.RequireApproval("recover", approved, state.Phase); err != nil {
		return domain.Recovery{}, err
	}
	if s.Runs.RequireLock() != nil {
		return domain.Recovery{Message: "No update lock owned by this run."}, nil
	}
	if state.Phase == domain.DeploymentStarted {
		_ = s.Deployment.SetMaintenance(ctx, state.Deployment, true)
		return domain.Recovery{Message: "A migration may have started. Lock retained; inspect the server and restore a matching backup if needed. No automatic downgrade attempted."}, nil
	}
	if state.Phase == domain.Quiescing || state.Phase == domain.BackingUp || state.Phase == domain.BackedUp {
		if err = s.Backups.Clear(state.Stage); err != nil {
			return domain.Recovery{}, err
		}
		if err = s.Deployment.StartOriginal(ctx, state.Deployment); err != nil {
			return domain.Recovery{}, err
		}
	}
	state.Phase = domain.Closed
	if err = s.Runs.Save(state); err != nil {
		return domain.Recovery{}, err
	}
	err = s.Runs.Release()
	return domain.Recovery{Recovered: err == nil, Message: "Original or verified deployment remains available."}, err
}
