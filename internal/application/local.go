package application

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/domain"
	"strings"
	"time"
)

type LocalOptions struct {
	Approved   bool
	CheckOnly  bool
	BackupRoot string
	Images     map[string]string
}
type LocalService struct {
	Remote  RemotePort
	Backups LocalBackupPort
	Log     func(string)
}

func (s *LocalService) Run(ctx context.Context, o LocalOptions) (err error) {
	if o.Approved == o.CheckOnly {
		return fmt.Errorf("choose either --check-only or explicit --approve-update")
	}
	directory, err := s.Backups.Begin(o.BackupRoot)
	if err != nil {
		return err
	}
	s.Log("Local backups: " + directory)
	// Recovery must remain possible if cancellation has ended the original SSH context.
	closed := false
	defer func() {
		if !closed {
			recoveryCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			r, e := s.Remote.Recover(recoveryCtx)
			if e != nil {
				s.Log("Remote recovery could not complete; inspect the recorded remote state.")
			} else {
				s.Log(r.Message)
			}
		}
	}()
	if err = s.Remote.Open(ctx, directory, o.Images); err != nil {
		return err
	}
	plan, err := s.Remote.Plan(ctx)
	if err != nil {
		return err
	}
	s.Log("Current Nextcloud: " + plan.CurrentVersion)
	s.Log("Current MariaDB: " + plan.DatabaseVersion)
	s.Log(fmt.Sprintf("MariaDB stages before Nextcloud: %v; after Nextcloud: %v", plan.DatabaseBefore, plan.DatabaseAfter))
	s.Log(plan.Scope)
	if o.CheckOnly {
		if err = s.Remote.Finish(ctx); err != nil {
			return err
		}
		closed = true
		s.Log("Preflight complete; no images pulled or services stopped.")
		return nil
	}
	if err = s.Backups.CheckSpace(directory, plan.EstimatedDatabaseBytes*2+(256<<20)); err != nil {
		return err
	}
	execute := func(step domain.Step, e error) error {
		if e != nil {
			return e
		}
		if step.Skip {
			s.Log(step.TargetVersion + " is already current.")
			return nil
		}
		s.Log("Preparing " + step.FromVersion + " -> " + step.TargetVersion)
		s.Log("Creating database and settings backup; S3 objects excluded.")
		backup, e := s.Remote.Backup(ctx)
		if e != nil {
			return e
		}
		s.Log("Downloading and verifying the local backup.")
		receipt, e := s.Backups.Receive(ctx, directory, backup, s.Remote.Download)
		if e != nil {
			return e
		}
		s.Log("Local backup verified; starting " + step.TargetVersion)
		result, e := s.Remote.Deploy(ctx, receipt)
		if e != nil {
			return e
		}
		s.Log("Verified Nextcloud " + result.Version)
		if result.DatabaseVersion != "" {
			s.Log("Verified MariaDB " + result.DatabaseVersion)
		}
		if len(result.DisabledApps) > 0 {
			if e = s.Remote.Finish(ctx); e != nil {
				return e
			}
			closed = true
			return fmt.Errorf("upgrade is healthy but apps became disabled; review before continuing: %s", strings.Join(result.DisabledApps, ", "))
		}
		return nil
	}
	for _, series := range plan.DatabaseBefore {
		if err = execute(s.Remote.PrepareDatabase(ctx, series)); err != nil {
			return err
		}
	}
	for _, major := range plan.Stages {
		if err = execute(s.Remote.Prepare(ctx, major)); err != nil {
			return err
		}
	}
	for _, series := range plan.DatabaseAfter {
		if err = execute(s.Remote.PrepareDatabase(ctx, series)); err != nil {
			return err
		}
	}
	if err = s.Remote.Finish(ctx); err != nil {
		return err
	}
	closed = true
	s.Log("Update complete. Backups retained at " + directory)
	return nil
}
