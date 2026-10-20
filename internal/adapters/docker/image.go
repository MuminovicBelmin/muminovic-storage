package docker

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/adapters/compose"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/domain"
	"os"
	"reflect"
	"sort"
)

func (r *Runtime) Status(ctx context.Context, d domain.Deployment) (domain.Status, string, error) {
	var status domain.Status
	e := r.occJSON(ctx, d, &status, "status", "--output=json")
	if e != nil {
		return status, "", e
	}
	c, e := r.app(ctx, d)
	return status, c.Image, e
}
func (r *Runtime) CheckDatabase(ctx context.Context, d domain.Deployment) error {
	c, e := r.inspect(ctx, d.DatabaseID)
	if e != nil {
		return e
	}
	if c.Image != d.DatabaseImage || !c.State.Running {
		return fmt.Errorf("the recorded database container and image must remain running between upgrade steps")
	}
	return nil
}
func (r *Runtime) SourceHash(d domain.Deployment) (string, error) {
	source, err := filesystem.Hash(d.Source)
	if err != nil {
		return "", err
	}
	if d.Environment == "" {
		return source, nil
	}
	environment, err := filesystem.Hash(d.Environment)
	if err != nil {
		return "", err
	}
	return source + ":" + environment, nil
}
func (r *Runtime) candidate(ctx context.Context, d domain.Deployment, image string) ([]byte, error) {
	return r.serviceCandidate(ctx, d, image, false)
}
func (r *Runtime) serviceCandidate(ctx context.Context, d domain.Deployment, image string, database bool) ([]byte, error) {
	if d.Environment == "" {
		return r.legacyServiceCandidate(ctx, d, image, database)
	}
	environment, e := os.ReadFile(d.Environment)
	if e != nil {
		return nil, e
	}
	key := "NEXTCLOUD_IMAGE"
	if database {
		key = "MARIADB_IMAGE"
	}
	candidate, e := compose.ReplaceEnvironment(environment, key, image)
	if e != nil {
		return nil, e
	}
	f, e := os.CreateTemp(d.Root, ".nextcloud-candidate-*.env")
	if e != nil {
		return nil, e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return nil, e
	}
	if _, e = f.Write(candidate); e != nil {
		f.Close()
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	before, e := r.resolved(ctx, d, "")
	if e != nil {
		return nil, e
	}
	after, e := r.resolvedWithEnvironment(ctx, d, f.Name())
	if e != nil {
		return nil, e
	}
	if database {
		service := object(object(before["services"])[databaseService])
		if service == nil {
			return nil, fmt.Errorf("MariaDB service is absent")
		}
		service["image"] = image
		environment := object(service["environment"])
		if environment == nil {
			return nil, fmt.Errorf("explicit MariaDB environment required")
		}
		environment["MARIADB_AUTO_UPGRADE"] = "1"
	} else {
		for _, name := range []string{appService, cronService} {
			service := object(object(before["services"])[name])
			if service == nil {
				return nil, fmt.Errorf("Nextcloud %s service is absent", name)
			}
			service["image"] = image
		}
	}
	if !reflect.DeepEqual(before, after) {
		return nil, fmt.Errorf("the proposed Compose edit changes more than the selected image and required upgrade setting")
	}
	return candidate, nil
}

func (r *Runtime) legacyServiceCandidate(ctx context.Context, d domain.Deployment, image string, database bool) ([]byte, error) {
	source, err := os.ReadFile(d.Source)
	if err != nil {
		return nil, err
	}
	var candidate []byte
	if database {
		candidate, err = compose.ReplaceDatabase(source, image)
	} else {
		candidate, err = compose.ReplaceImage(source, image)
	}
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(d.Root, ".nextcloud-candidate-*.yml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if _, err = f.Write(candidate); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	before, err := r.resolved(ctx, d, "")
	if err != nil {
		return nil, err
	}
	after, err := r.resolved(ctx, d, f.Name())
	if err != nil {
		return nil, err
	}
	name := appService
	if database {
		name = databaseService
	}
	service := object(object(before["services"])[name])
	if service == nil {
		return nil, fmt.Errorf("required %s service is absent", name)
	}
	service["image"] = image
	if database {
		environment := object(service["environment"])
		if environment == nil {
			return nil, fmt.Errorf("explicit MariaDB environment required")
		}
		environment["MARIADB_AUTO_UPGRADE"] = "1"
	}
	if !reflect.DeepEqual(before, after) {
		return nil, fmt.Errorf("the proposed Compose edit changes more than the selected image and required upgrade setting")
	}
	return candidate, nil
}
func (r *Runtime) ValidateImage(ctx context.Context, d domain.Deployment, image string) error {
	_, e := r.candidate(ctx, d, image)
	return e
}
func (r *Runtime) image(ctx context.Context, tag string) (domain.Image, error) {
	var rows []struct {
		ID     string `json:"Id"`
		Config struct{ Env []string }
	}
	e := r.json(ctx, []string{"docker", "image", "inspect", tag}, "inspect Nextcloud image", &rows)
	if e != nil {
		return domain.Image{}, e
	}
	if len(rows) != 1 {
		return domain.Image{}, fmt.Errorf("expected one target image")
	}
	v := env(rows[0].Config.Env)["NEXTCLOUD_VERSION"]
	if _, e = domain.ParseVersion(v); e != nil {
		return domain.Image{}, e
	}
	return domain.Image{Tag: tag, ID: rows[0].ID, Version: v}, nil
}
func (r *Runtime) Pull(ctx context.Context, tag string) (domain.Image, error) {
	if _, e := domain.ImageMajor(tag); e != nil {
		return domain.Image{}, e
	}
	_, e := r.Commands.Run(ctx, r.command([]string{"docker", "pull", tag}, "pull Nextcloud image", true))
	if e != nil {
		return domain.Image{}, e
	}
	return r.image(ctx, tag)
}
func (r *Runtime) EnabledApps(ctx context.Context, d domain.Deployment) ([]string, error) {
	var apps struct {
		Enabled map[string]string `json:"enabled"`
	}
	if e := r.occJSON(ctx, d, &apps, "app:list", "--output=json"); e != nil {
		return nil, e
	}
	out := []string{}
	for id := range apps.Enabled {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
