package compose

import (
	"go.yaml.in/yaml/v3"
	"strings"
	"testing"
)

func TestDatabaseEditPreservesAppAndSupportsEnvironmentStyles(t *testing.T) {
	for _, environment := range []string{
		"      MYSQL_DATABASE: fixture\n",
		"      - MYSQL_DATABASE=fixture\n",
		"      MYSQL_DATABASE: fixture\n      MARIADB_AUTO_UPGRADE: '0'\n",
		"      - MYSQL_DATABASE=fixture\n      - MARIADB_AUTO_UPGRADE=0\n",
	} {
		source := "services:\n  db:\n    image: mariadb:lts # keep\n    environment:\n" + environment + "  app:\n    image: nextcloud:30\n    restart: unless-stopped\n"
		out, err := ReplaceDatabase([]byte(source), "mariadb:11.4")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Services map[string]struct {
				Image       string
				Environment any
			}
		}
		if err = yaml.Unmarshal(out, &doc); err != nil {
			t.Fatal(err, string(out))
		}
		if doc.Services["db"].Image != "mariadb:11.4" || doc.Services["app"].Image != "nextcloud:30" || !strings.Contains(string(out), "MYSQL_DATABASE") || !strings.Contains(string(out), "# keep") {
			t.Fatal(string(out))
		}
		switch env := doc.Services["db"].Environment.(type) {
		case map[string]any:
			if env["MARIADB_AUTO_UPGRADE"] != "1" {
				t.Fatal("upgrade flag missing", env)
			}
		case []any:
			found := false
			for _, item := range env {
				if item == "MARIADB_AUTO_UPGRADE=1" {
					found = true
				}
			}
			if !found {
				t.Fatal("upgrade flag missing", env)
			}
		default:
			t.Fatal("invalid environment")
		}
		second, err := ReplaceDatabase(out, "mariadb:11.8")
		if err != nil || strings.Count(string(second), "MARIADB_AUTO_UPGRADE") != 1 {
			t.Fatal(string(second), err)
		}
	}
}
