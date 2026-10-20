package docker

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/adapters/process"
	"nextcloud-updater/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveEnvironmentComparisonUsesComposeDollarEncoding(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker CLI unavailable")
	}
	root := t.TempDir()
	source := filepath.Join(root, "compose.yml")
	fixture := "services:\n  db:\n    image: mariadb:11.4\n    environment:\n      MYSQL_PASSWORD: literal$%s\n  app:\n    image: nextcloud:30\n    environment:\n      MYSQL_PASSWORD: literal$%s\n      DOUBLE: dollar$$$$literal\n"
	if err := os.WriteFile(source, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	commands := &commands{handle: func(c process.Command) ([]byte, error) {
		if strings.Contains(strings.Join(c.Args, " "), "config --format json") {
			return (process.Native{}).Run(context.Background(), c)
		}
		if strings.Join(c.Args, " ") == "ps -q" {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected fixture operation")
	}}
	r := Runtime{Commands: commands, Timeout: time.Minute}
	config, err := r.resolved(context.Background(), domain.Deployment{Root: root, Source: source, Project: "dollar-fixture", Compose: []string{"docker", "compose"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	var app, db container
	app.Config.Env = []string{"MYSQL_PASSWORD=literal$%s", "DOUBLE=dollar$$literal"}
	db.Config.Env = []string{"MYSQL_PASSWORD=literal$%s"}
	if err = r.validateLive(context.Background(), config, app, db); err != nil {
		t.Fatal("literal dollars were mistaken for credential drift", err)
	}
	app.Config.Env[0] = "MYSQL_PASSWORD=wrong"
	if err = r.validateLive(context.Background(), config, app, db); err == nil {
		t.Fatal("actual credential mismatch accepted")
	}
}
