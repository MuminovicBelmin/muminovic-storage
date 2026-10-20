package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"nextcloud-updater/internal/adapters/compose"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/adapters/process"
	"nextcloud-updater/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type commands struct {
	calls  []process.Command
	handle func(process.Command) ([]byte, error)
}

func (c *commands) Run(_ context.Context, cmd process.Command) ([]byte, error) {
	c.calls = append(c.calls, cmd)
	return c.handle(cmd)
}
func (c *commands) Stream(context.Context, process.Command, io.Writer) error { return nil }
func data(v any) []byte                                                      { b, _ := json.Marshal(v); return b }
func TestApplyChangesOnlyNextcloudImageAndDoesNotRestartDatabase(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "compose.yml")
	original := "services:\n  db:\n    image: mariadb:lts\n  app:\n    image: nextcloud:30 # deployment\n"
	os.WriteFile(source, []byte(original), 0600)
	d := domain.Deployment{Root: root, Source: source, Project: "fixture", Compose: []string{"docker", "compose"}, DatabaseID: "db", DatabaseImage: "db-image"}
	recorder := &commands{}
	recorder.handle = func(c process.Command) ([]byte, error) {
		a := c.Args
		if len(a) > 0 && a[0] == "inspect" {
			return data([]any{map[string]any{"Id": "db", "Image": "db-image", "State": map[string]bool{"Running": true}}}), nil
		}
		if len(a) > 1 && a[0] == "image" {
			return data([]any{map[string]any{"Id": "target", "Config": map[string]any{"Env": []string{"NEXTCLOUD_VERSION=31.0.14"}}}}), nil
		}
		joined := strings.Join(a, " ")
		if strings.Contains(joined, "config --format json") {
			file := ""
			for i, arg := range a {
				if arg == "-f" {
					file = a[i+1]
				}
			}
			b, e := os.ReadFile(file)
			if e != nil {
				return nil, e
			}
			n, e := compose.ImageNode(b)
			if e != nil {
				return nil, e
			}
			return data(map[string]any{"services": map[string]any{"app": map[string]any{"image": n.Value}, "db": map[string]any{"image": "mariadb:lts"}}}), nil
		}
		if strings.Contains(joined, "up -d --no-deps --pull never app") {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected command")
	}
	r := Runtime{Commands: recorder, Timeout: time.Minute}
	hash, _ := filesystem.Hash(source)
	if e := r.Apply(context.Background(), d, domain.Image{Tag: "nextcloud:31", ID: "target", Version: "31.0.14"}, hash); e != nil {
		t.Fatal(e)
	}
	out, _ := os.ReadFile(source)
	if string(out) != strings.Replace(original, "nextcloud:30", "nextcloud:31", 1) {
		t.Fatal(string(out))
	}
	for _, c := range recorder.calls {
		a := strings.Join(c.Args, " ")
		if strings.Contains(a, "pull mariadb") || strings.Contains(a, "down") {
			t.Fatal("database was changed")
		}
	}
}
func TestBackgroundJobIDsAreNotRoundedAndRetentionIsNotExecuted(t *testing.T) {
	runs := 0
	recorder := &commands{}
	recorder.handle = func(c process.Command) ([]byte, error) {
		a := strings.Join(c.Args, " ")
		if strings.Contains(a, "ps -aq") {
			return []byte("app\n"), nil
		}
		if strings.Contains(a, "background-job:list") {
			runs++
			if runs == 1 {
				return []byte(`[{"id":98109593040965633,"class":"OC\\Repair\\AsyncRepair"},{"id":2,"class":"OCA\\Files_Trashbin\\ExpireTrash"}]`), nil
			}
			return []byte(`[]`), nil
		}
		if strings.Contains(a, "background-job:execute") {
			if !strings.HasSuffix(a, "98109593040965633") {
				t.Fatal("job ID was rounded or wrong job selected", a)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := Runtime{Commands: recorder, Timeout: 5 * time.Second}
	if e := r.migrations(ctx, domain.Deployment{Project: "test"}); e != nil {
		t.Fatal(e)
	}
}
func TestContainerPathMappingRefusesEscapeAndUsesSpecificMount(t *testing.T) {
	c := container{Mounts: []Mount{{Destination: "/var/www/html", Source: filepath.Join(t.TempDir(), "app")}, {Destination: "/var/www/html/config", Source: filepath.Join(t.TempDir(), "configuration")}}}
	p, e := hostPath(c, "/var/www/html/config/config.php")
	if e != nil || p != filepath.Join(c.Mounts[1].Source, "config.php") {
		t.Fatal(p, e)
	}
	if _, e = hostPath(c, "/var/www/html/../../etc/private"); e == nil {
		t.Fatal("path escaped mount")
	}
}
func TestDatabaseCredentialsStayInsideContainerShell(t *testing.T) {
	r := Runtime{}
	args := r.databaseCommand(domain.Deployment{DatabaseID: "db", DatabaseName: "nextcloud_database"}, "exec dump")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "password-value") {
		t.Fatal("credential exposed")
	}
	if !strings.Contains(joined, "MYSQL_ROOT_PASSWORD_FILE") || !strings.Contains(joined, "export MYSQL_PWD") {
		t.Fatal("container credential lookup missing")
	}
}

func TestImageEditWithActualComposeConfigParser(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker CLI unavailable")
	}
	root := t.TempDir()
	source := filepath.Join(root, "compose.yml")
	text := "services:\n  db:\n    image: mariadb:lts\n  app:\n    image: nextcloud:30\n    volumes:\n      - data:/var/www/html\nvolumes:\n  data:\n"
	if err := os.WriteFile(source, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	r := Runtime{Commands: process.Native{}, Timeout: time.Minute}
	d := domain.Deployment{Root: root, Source: source, Project: "updater-offline-fixture", Compose: []string{"docker", "compose"}}
	// `config` parses local files only; it never starts/pulls containers or contacts the server.
	if err := r.ValidateImage(context.Background(), d, "nextcloud:31"); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(source)
	if string(actual) != text {
		t.Fatal("validation modified the source")
	}
}
