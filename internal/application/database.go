package application

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/domain"
)

func (s *RemoteService) validateTarget(ctx context.Context, state domain.State) error {
	if state.Kind == "mariadb" {
		return s.Deployment.ValidateDatabaseImage(ctx, state.Deployment, state.Target.Tag)
	}
	return s.Deployment.ValidateImage(ctx, state.Deployment, state.Target.Tag)
}

func (s *RemoteService) PrepareDatabase(ctx context.Context, series string, approved bool) (domain.Step, error) {
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
	status, _, err := s.Deployment.Status(ctx, d)
	if err != nil {
		return domain.Step{}, err
	}
	if !status.Healthy() {
		return domain.Step{}, fmt.Errorf("Nextcloud must be healthy before a database upgrade")
	}
	nc, err := domain.ParseVersion(status.Version)
	if err != nil {
		return domain.Step{}, err
	}
	current, err := s.Deployment.DatabaseVersion(ctx, d)
	if err != nil {
		return domain.Step{}, err
	}
	tag := "mariadb:" + series
	if _, err = domain.DatabaseImage(tag); err != nil {
		return domain.Step{}, err
	}
	// Validate the intended series before pulling, then validate the actual patch.
	requested, err := domain.ParseVersion(series + ".0")
	if err != nil {
		return domain.Step{}, err
	}
	if domain.DatabaseSeries(current) == series {
		requested = current
	}
	if err = domain.ValidateDatabaseStep(current, requested); err != nil {
		return domain.Step{}, err
	}
	if !domain.DatabaseCompatible(requested, nc[0]) {
		return domain.Step{}, fmt.Errorf("MariaDB target is incompatible with the running Nextcloud major")
	}
	if err = s.Deployment.ValidateDatabaseImage(ctx, d, tag); err != nil {
		return domain.Step{}, err
	}
	image, err := s.Deployment.PullDatabase(ctx, tag)
	if err != nil {
		return domain.Step{}, err
	}
	target, err := domain.ParseVersion(image.Version)
	if err != nil {
		return domain.Step{}, err
	}
	if domain.DatabaseSeries(target) != series {
		return domain.Step{}, fmt.Errorf("MariaDB image has the wrong release series")
	}
	if err = domain.ValidateDatabaseStep(current, target); err != nil {
		return domain.Step{}, err
	}
	step := domain.Step{FromVersion: "MariaDB " + current.String(), TargetVersion: "MariaDB " + target.String()}
	if current == target && image.ID == d.DatabaseImage {
		step.Skip = true
		return step, nil
	}
	stage, err := s.Runs.Stage("mariadb-" + series)
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
	state.Kind, state.Phase, state.Stage = "mariadb", domain.Prepared, stage
	state.FromVersion, state.Target, state.EnabledApps, state.SourceHash = current.String(), image, apps, hash
	return step, s.Runs.Save(state)
}
