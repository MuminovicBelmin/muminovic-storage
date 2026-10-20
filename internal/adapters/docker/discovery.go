package docker

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/domain"
	"path/filepath"
	"strings"
)

func (r *Runtime) Discover(ctx context.Context, root string) (domain.Deployment, error) {
	d := domain.Deployment{Root: root}
	b, e := r.run(ctx, []string{"docker", "ps", "-q", "--filter", "label=com.docker.compose.service=" + appService}, "find Nextcloud deployment")
	if e != nil {
		return d, e
	}
	var found []container
	for _, id := range strings.Fields(string(b)) {
		c, e := r.inspect(ctx, id)
		if e != nil {
			return d, e
		}
		working := c.Config.Labels["com.docker.compose.project.working_dir"]
		if working != "" {
			resolved, e := filepath.EvalSymlinks(working)
			if e == nil && resolved == root {
				found = append(found, c)
			}
		}
	}
	if len(found) != 1 {
		return d, fmt.Errorf("cannot uniquely match a running Nextcloud deployment to the remote directory")
	}
	app := found[0]
	d.Project = app.Config.Labels["com.docker.compose.project"]
	sources := strings.Split(app.Config.Labels["com.docker.compose.project.config_files"], ",")
	if len(sources) != 1 || sources[0] == "" {
		return d, fmt.Errorf("automatic updates currently require one deployed Compose source file")
	}
	d.Source, e = filepath.EvalSymlinks(sources[0])
	if e != nil {
		return d, e
	}
	if filepath.Dir(d.Source) != root {
		return d, fmt.Errorf("Compose source must be in the remote deployment directory")
	}
	d.Environment = filepath.Join(root, ".env")
	if _, e = filepath.EvalSymlinks(d.Environment); e != nil {
		return d, fmt.Errorf("deployment .env file is required: %w", e)
	}
	d.Compose = []string{"docker", "compose"}
	if _, e = r.run(ctx, append(append([]string{}, d.Compose...), "version"), "check Compose version"); e != nil {
		d.Compose = []string{"docker-compose"}
		if _, e = r.run(ctx, []string{"docker-compose", "version"}, "check Compose version"); e != nil {
			return d, e
		}
	}
	config, e := r.resolved(ctx, d, "")
	if e != nil {
		return d, e
	}
	services := object(config["services"])
	if len(services) != 5 || services[appService] == nil || services[databaseService] == nil || services[cronService] == nil || services["valkey"] == nil || services["tmp_init"] == nil {
		return d, fmt.Errorf("only the muminovic_cloud app/db/cron/valkey/tmp_init stack is supported")
	}
	databaseID, e := r.find(ctx, d.Project, databaseService, true)
	if e != nil {
		return d, e
	}
	database, e := r.inspect(ctx, databaseID)
	if e != nil {
		return d, e
	}
	d.DatabaseID = database.ID
	d.DatabaseImage = database.Image
	if e = r.validateLive(ctx, config, app, database); e != nil {
		return d, e
	}
	var exported map[string]any
	if e = r.occJSON(ctx, d, &exported, "config:list", "--private"); e != nil {
		return d, e
	}
	system := object(exported["system"])
	if !strings.HasSuffix(text(object(system["objectstore"])["class"]), `\S3`) || len(object(system["objectstore_multibucket"])) > 0 {
		return d, fmt.Errorf("this limited backup scope requires a single S3 primary store")
	}
	if text(system["dbtype"]) != "mysql" {
		return d, fmt.Errorf("only the existing MariaDB/MySQL database is supported")
	}
	d.DatabaseName = text(system["dbname"])
	if d.DatabaseName == "" {
		return d, fmt.Errorf("missing configured database name")
	}
	return d, nil
}
func (r *Runtime) validateLive(ctx context.Context, config map[string]any, app, database container) error {
	services := object(config["services"])
	containers := map[string]container{appService: app, databaseService: database}
	if services[cronService] != nil {
		cronID, e := r.find(ctx, app.Config.Labels["com.docker.compose.project"], cronService, true)
		if e != nil {
			return e
		}
		cron, e := r.inspect(ctx, cronID)
		if e != nil {
			return e
		}
		containers[cronService] = cron
	}
	for service, c := range containers {
		cfg := object(object(config["services"])[service])
		liveEnv := env(c.Config.Env)
		for key, value := range object(cfg["environment"]) {
			// `compose config` renders a reusable Compose document, including
			// dollar escaping in its JSON output. Compare that representation
			// with the equally escaped container value, not its raw secret.
			if value != nil && strings.ReplaceAll(liveEnv[key], "$", "$$") != fmt.Sprint(value) {
				return fmt.Errorf("Compose environment differs from the running %s container", service)
			}
		}
		for _, value := range list(cfg["volumes"]) {
			mount := object(value)
			source := text(mount["source"])
			if text(mount["type"]) == "volume" {
				source = text(object(object(config["volumes"])[source])["name"])
			}
			matches := 0
			for _, actual := range c.Mounts {
				if actual.Destination == text(mount["target"]) {
					expected := actual.Source
					if actual.Name != "" {
						expected = actual.Name
					}
					if expected == source {
						matches++
					}
				}
			}
			if matches != 1 {
				return fmt.Errorf("a %s mount would point at different storage", service)
			}
		}
	}
	var sources []string
	for _, c := range containers {
		for _, mount := range c.Mounts {
			if mount.RW {
				sources = append(sources, mount.Source)
			}
		}
	}
	b, e := r.run(ctx, []string{"docker", "ps", "-q"}, "inspect other storage writers")
	if e != nil {
		return e
	}
	for _, id := range strings.Fields(string(b)) {
		other, e := r.inspect(ctx, id)
		if e != nil {
			return e
		}
		if other.ID == app.ID || other.ID == database.ID {
			continue
		}
		if cron, ok := containers[cronService]; ok && other.ID == cron.ID {
			continue
		}
		for _, m := range other.Mounts {
			if m.RW {
				for _, source := range sources {
					if filesystem.Within(source, m.Source) || filesystem.Within(m.Source, source) {
						return fmt.Errorf("another running container shares this deployment's storage")
					}
				}
			}
		}
	}
	return nil
}
