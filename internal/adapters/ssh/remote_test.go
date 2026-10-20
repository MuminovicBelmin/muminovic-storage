package ssh

import (
	"context"
	"encoding/json"
	"io"
	"nextcloud-updater/internal/adapters/process"
	"strings"
	"testing"
	"time"
)

type recorder struct{ commands []process.Command }

func (r *recorder) Run(_ context.Context, c process.Command) ([]byte, error) {
	r.commands = append(r.commands, c)
	return json.Marshal(map[string]any{"version": "31.0.14", "disabled_apps": []string{}})
}
func (r *recorder) Stream(context.Context, process.Command, io.Writer) error { return nil }
func TestRemoteApprovalAndKnownHostPolicy(t *testing.T) {
	commands := &recorder{}
	r := Remote{Commands: commands, Directory: "/home/Nextcloud/.nextcloud-updater/run", Config: Config{User: "root", Host: "offline.invalid", Port: 22, Target: 35, Timeout: time.Minute}}
	if _, e := r.Deploy(context.Background(), "receipt"); e != nil {
		t.Fatal(e)
	}
	args := strings.Join(commands.commands[0].Args, " ")
	if strings.Contains(args, "--approve-update") {
		t.Fatal("approval silently added")
	}
	if !strings.Contains(args, "StrictHostKeyChecking=yes") {
		t.Fatal("host key verification missing")
	}
	r.Config.Approved = true
	r.Deploy(context.Background(), "receipt")
	if !strings.Contains(strings.Join(commands.commands[1].Args, " "), "--approve-update") {
		t.Fatal("explicit approval not forwarded")
	}
}
func TestShellArgumentsAreQuoted(t *testing.T) {
	if Quote("a'b") != `'a'"'"'b'` {
		t.Fatal(Quote("a'b"))
	}
	if Quote("$(danger)") != "'$(danger)'" {
		t.Fatal("shell substitution not quoted")
	}
}
func TestDownloadCannotEscapeRun(t *testing.T) {
	r := Remote{Directory: "/safe/run", LocalDirectory: t.TempDir()}
	if r.Download(context.Background(), "/other/secret", "local") == nil {
		t.Fatal("accepted unrelated remote path")
	}
}
