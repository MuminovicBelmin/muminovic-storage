// Package application owns the workflow. Adapters implement these ports.
package application

import (
	"context"
	"io"
	"nextcloud-updater/internal/domain"
)

type DeploymentPort interface {
	DatabasePort
	Discover(context.Context, string) (domain.Deployment, error)
	Status(context.Context, domain.Deployment) (domain.Status, string, error)
	CheckDatabase(context.Context, domain.Deployment) error
	SourceHash(domain.Deployment) (string, error)
	ValidateImage(context.Context, domain.Deployment, string) error
	Pull(context.Context, string) (domain.Image, error)
	EnabledApps(context.Context, domain.Deployment) ([]string, error)
	BackupInput(context.Context, domain.Deployment) (domain.BackupInput, error)
	SetMaintenance(context.Context, domain.Deployment, bool) error
	StopApp(context.Context, domain.Deployment) error
	StartOriginal(context.Context, domain.Deployment) error
	Apply(context.Context, domain.Deployment, domain.Image, string) error
	Verify(context.Context, domain.Deployment, domain.Image) (domain.Status, error)
}

// DatabasePort separates engine upgrades from Nextcloud schema migrations.
type DatabasePort interface {
	DatabaseVersion(context.Context, domain.Deployment) (domain.Version, error)
	ValidateDatabaseImage(context.Context, domain.Deployment, string) error
	PullDatabase(context.Context, string) (domain.Image, error)
	ApplyDatabase(context.Context, domain.Deployment, domain.Image, string) (domain.Deployment, error)
}
type RunStore interface {
	Load() (domain.State, error)
	Save(domain.State) error
	Acquire() error
	RequireLock() error
	Release() error
	Stage(string) (string, error)
}
type DatabaseDumper interface {
	DumpDatabase(context.Context, domain.Deployment, io.Writer) error
}
type BackupPort interface {
	CheckSpace(string, domain.BackupInput) error
	Create(context.Context, domain.Deployment, domain.BackupInput, string, string, string) (domain.Backup, error)
	Verify(domain.Backup) error
	Clear(string) error
}
type RemotePort interface {
	Open(context.Context, string, map[string]string) error
	Plan(context.Context) (domain.Plan, error)
	Prepare(context.Context, int) (domain.Step, error)
	PrepareDatabase(context.Context, string) (domain.Step, error)
	Backup(context.Context) (domain.Backup, error)
	Download(context.Context, string, string) error
	Deploy(context.Context, string) (domain.Result, error)
	Finish(context.Context) error
	Recover(context.Context) (domain.Recovery, error)
}
type LocalBackupPort interface {
	Begin(string) (string, error)
	CheckSpace(string, int64) error
	Receive(context.Context, string, domain.Backup, func(context.Context, string, string) error) (string, error)
}
