package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"nextcloud-updater/internal/adapters/process"
	"nextcloud-updater/internal/domain"
	"strings"
	"time"
)

type Runtime struct {
	Commands process.Runner
	Timeout  time.Duration
}

const (
	appService      = "app"
	cronService     = "cron"
	databaseService = "db"
)

type Mount struct {
	Type, Name, Source, Destination string
	RW                              bool
}
type container struct {
	ID     string `json:"Id"`
	Image  string
	Config struct {
		Env    []string
		Labels map[string]string
	}
	State struct {
		Running   bool
		ExitCode  int
		OOMKilled bool
	}
	Mounts []Mount
}

func env(values []string) map[string]string {
	out := map[string]string{}
	for _, v := range values {
		if k, val, ok := strings.Cut(v, "="); ok {
			out[k] = val
		}
	}
	return out
}
func object(value any) map[string]any { m, _ := value.(map[string]any); return m }
func text(value any) string           { s, _ := value.(string); return s }
func list(value any) []any            { s, _ := value.([]any); return s }
func (r *Runtime) command(args []string, description string, long bool) process.Command {
	t := 5 * time.Minute
	if long {
		t = r.Timeout
	}
	return process.Command{Program: args[0], Args: args[1:], Description: description, Timeout: t}
}
func (r *Runtime) run(ctx context.Context, args []string, description string) ([]byte, error) {
	return r.Commands.Run(ctx, r.command(args, description, false))
}
func (r *Runtime) json(ctx context.Context, args []string, description string, out any) error {
	b, e := r.run(ctx, args, description)
	if e != nil {
		return e
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if e = decoder.Decode(out); e != nil {
		return fmt.Errorf("%s did not return valid JSON", description)
	}
	return nil
}
func (r *Runtime) inspect(ctx context.Context, id string) (container, error) {
	var rows []container
	e := r.json(ctx, []string{"docker", "inspect", id}, "inspect deployment container", &rows)
	if e != nil {
		return container{}, e
	}
	if len(rows) != 1 {
		return container{}, fmt.Errorf("expected exactly one inspected container")
	}
	return rows[0], nil
}
func (r *Runtime) find(ctx context.Context, project, service string, running bool) (string, error) {
	flag := "-aq"
	if running {
		flag = "-q"
	}
	args := []string{"docker", "ps", flag, "--filter", "label=com.docker.compose.service=" + service}
	if project != "" {
		args = append(args, "--filter", "label=com.docker.compose.project="+project)
	}
	b, e := r.run(ctx, args, "locate "+service)
	if e != nil {
		return "", e
	}
	ids := strings.Fields(string(b))
	if len(ids) != 1 {
		return "", fmt.Errorf("expected one %s container in the project", service)
	}
	return ids[0], nil
}
func (r *Runtime) app(ctx context.Context, d domain.Deployment) (container, error) {
	id, e := r.find(ctx, d.Project, appService, false)
	if e != nil {
		return container{}, e
	}
	return r.inspect(ctx, id)
}
func (r *Runtime) occ(ctx context.Context, d domain.Deployment, args ...string) ([]byte, error) {
	id, e := r.find(ctx, d.Project, appService, false)
	if e != nil {
		return nil, e
	}
	command := append([]string{"docker", "exec", "-u", "www-data", id, "php", "occ"}, args...)
	return r.Commands.Run(ctx, r.command(command, "Nextcloud "+args[0], true))
}
func (r *Runtime) occJSON(ctx context.Context, d domain.Deployment, out any, args ...string) error {
	b, e := r.occ(ctx, d, args...)
	if e != nil {
		return e
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if e = decoder.Decode(out); e != nil {
		return fmt.Errorf("Nextcloud command did not return valid JSON")
	}
	return nil
}
func (r *Runtime) compose(d domain.Deployment, source string, args ...string) []string {
	if source == "" {
		source = d.Source
	}
	out := append([]string{}, d.Compose...)
	out = append(out, "--project-directory", d.Root, "-p", d.Project, "-f", source)
	return append(out, args...)
}
func (r *Runtime) composeWithEnvironment(d domain.Deployment, environment string, args ...string) []string {
	out := append([]string{}, d.Compose...)
	out = append(out, "--env-file", environment, "--project-directory", d.Root, "-p", d.Project, "-f", d.Source)
	return append(out, args...)
}
func (r *Runtime) resolved(ctx context.Context, d domain.Deployment, source string) (map[string]any, error) {
	var out map[string]any
	e := r.json(ctx, r.compose(d, source, "config", "--format", "json"), "resolve Compose configuration", &out)
	return out, e
}
func (r *Runtime) resolvedWithEnvironment(ctx context.Context, d domain.Deployment, environment string) (map[string]any, error) {
	var out map[string]any
	e := r.json(ctx, r.composeWithEnvironment(d, environment, "config", "--format", "json"), "resolve Compose configuration", &out)
	return out, e
}
