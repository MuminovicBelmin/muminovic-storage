package docker

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/domain"
	"regexp"
	"strings"
	"time"
)

func (r *Runtime) SetMaintenance(ctx context.Context, d domain.Deployment, on bool) error {
	value := "--off"
	if on {
		value = "--on"
	}
	_, e := r.occ(ctx, d, "maintenance:mode", value)
	return e
}
func (r *Runtime) StopApp(ctx context.Context, d domain.Deployment) error {
	cron, e := r.find(ctx, d.Project, cronService, false)
	if e != nil {
		return e
	}
	if _, e = r.run(ctx, []string{"docker", "stop", "--time", "120", cron}, "stop Nextcloud cron writer"); e != nil {
		return e
	}
	c, e := r.app(ctx, d)
	if e != nil {
		return e
	}
	_, e = r.run(ctx, []string{"docker", "stop", "--time", "120", c.ID}, "stop Nextcloud writers")
	return e
}
func wait(ctx context.Context, timeout, interval time.Duration, check func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if e := ctx.Err(); e != nil {
			return fmt.Errorf("operation timed out or was cancelled")
		}
		ok, e := check(ctx)
		if e != nil {
			return e
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation timed out or was cancelled")
		case <-time.After(interval):
		}
	}
}
func (r *Runtime) StartOriginal(ctx context.Context, d domain.Deployment) error {
	c, e := r.app(ctx, d)
	if e != nil {
		return e
	}
	if _, e = r.run(ctx, []string{"docker", "start", c.ID}, "restart original Nextcloud container"); e != nil {
		return e
	}
	if e = wait(ctx, 3*time.Minute, 5*time.Second, func(ctx context.Context) (bool, error) { return r.SetMaintenance(ctx, d, false) == nil, nil }); e != nil {
		return e
	}
	if d.Environment == "" {
		return nil
	}
	_, e = r.Commands.Run(ctx, r.command(r.compose(d, "", "up", "-d", "--no-deps", "--pull", "never", cronService), "restart Nextcloud cron", true))
	return e
}
func (r *Runtime) Apply(ctx context.Context, d domain.Deployment, image domain.Image, expectedHash string) error {
	if e := r.CheckDatabase(ctx, d); e != nil {
		return e
	}
	hash, e := r.SourceHash(d)
	if e != nil {
		return e
	}
	if hash != expectedHash {
		return fmt.Errorf("deployed Compose source changed")
	}
	actual, e := r.image(ctx, image.Tag)
	if e != nil {
		return e
	}
	if actual.ID != image.ID {
		return fmt.Errorf("target image tag changed after preflight")
	}
	candidate, e := r.candidate(ctx, d, image.Tag)
	if e != nil {
		return e
	}
	target := d.Environment
	if target == "" {
		target = d.Source
	}
	if e = filesystem.AtomicWrite(target, candidate, 0600, true); e != nil {
		return e
	}
	_, e = r.Commands.Run(ctx, r.command(r.compose(d, "", "up", "-d", "--no-deps", "--pull", "never", appService), "recreate Nextcloud app only", true))
	return e
}

var jobID = regexp.MustCompile(`^[0-9]+$`)

func (r *Runtime) migrations(ctx context.Context, d domain.Deployment) error {
	return wait(ctx, r.Timeout, 2*time.Second, func(ctx context.Context) (bool, error) {
		var rows []domain.Job
		if e := r.occJSON(ctx, d, &rows, "background-job:list", "--limit=100000", "--output=json"); e != nil {
			return false, e
		}
		jobs, e := domain.MigrationJobs(rows)
		if e != nil {
			return false, e
		}
		pending := jobs[:0]
		for _, job := range jobs {
			if job.Class == `OC\Core\BackgroundJobs\PreviewMigrationJob` {
				flag, err := r.occ(ctx, d, "config:app:get", "core", "previewMovedDone")
				if err != nil {
					return false, err
				}
				if strings.TrimSpace(string(flag)) == "1" {
					continue
				}
			}
			pending = append(pending, job)
		}
		jobs = pending
		if len(jobs) == 0 {
			return true, nil
		}
		for _, job := range jobs {
			id := fmt.Sprint(job.ID)
			if !jobID.MatchString(id) {
				return false, fmt.Errorf("invalid migration job identity")
			}
			if _, e = r.occ(ctx, d, "background-job:execute", "--force-execute", id); e != nil {
				return false, e
			}
		}
		return false, nil
	})
}
func (r *Runtime) Verify(ctx context.Context, d domain.Deployment, image domain.Image) (domain.Status, error) {
	expected, e := domain.ParseVersion(image.Version)
	if e != nil {
		return domain.Status{}, e
	}
	e = wait(ctx, r.Timeout, 5*time.Second, func(ctx context.Context) (bool, error) {
		s, id, e := r.Status(ctx, d)
		if e != nil {
			return false, nil
		}
		v, e := domain.ParseVersion(s.Version)
		if e != nil || v != expected || !s.Installed || s.NeedsDBUpgrade {
			return false, nil
		}
		if id != image.ID {
			return false, fmt.Errorf("running Nextcloud image differs from the approved target")
		}
		if s.Maintenance {
			if e = r.SetMaintenance(ctx, d, false); e != nil {
				return false, nil
			}
		}
		s, _, e = r.Status(ctx, d)
		return e == nil && s.Healthy(), nil
	})
	if e != nil {
		return domain.Status{}, e
	}
	if e = r.migrations(ctx, d); e != nil {
		return domain.Status{}, e
	}
	code := `$s=json_decode(file_get_contents('http://127.0.0.1/status.php'),true); exit(is_array($s) && !empty($s['installed']) && empty($s['maintenance']) && empty($s['needsDbUpgrade']) ? 0 : 1);`
	e = wait(ctx, r.Timeout, 5*time.Second, func(ctx context.Context) (bool, error) {
		c, e := r.app(ctx, d)
		if e != nil {
			return false, e
		}
		_, e = r.run(ctx, []string{"docker", "exec", c.ID, "php", "-r", code}, "verify local HTTP status")
		return e == nil, nil
	})
	if e != nil {
		return domain.Status{}, e
	}
	status, _, e := r.Status(ctx, d)
	if e != nil {
		return status, e
	}
	if d.Environment == "" {
		return status, nil
	}
	_, e = r.Commands.Run(ctx, r.command(r.compose(d, "", "up", "-d", "--no-deps", "--pull", "never", cronService), "recreate Nextcloud cron", true))
	return status, e
}
