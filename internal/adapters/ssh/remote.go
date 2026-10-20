// Package ssh adapts the remote application protocol to installed OpenSSH tools.
package ssh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/adapters/process"
	"nextcloud-updater/internal/domain"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host, User, Root, HelperDirectory string
	Port, Target                      int
	Timeout                           time.Duration
	Approved                          bool
	Progress                          io.Writer
}
type Remote struct {
	Config                    Config
	Commands                  process.Runner
	Directory, LocalDirectory string
	uploaded                  bool
}

var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`)
var userPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)
var rootPattern = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)
var runPattern = regexp.MustCompile(`^\d{8}-\d{6}-[a-f0-9]{16}$`)

func Quote(value string) string       { return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'" }
func (r *Remote) destination() string { return r.Config.User + "@" + r.Config.Host }
func (r *Remote) options(scp bool) []string {
	port := "-p"
	if scp {
		port = "-P"
	}
	return []string{port, strconv.Itoa(r.Config.Port), "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=20", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3"}
}
func (r *Remote) invoke(ctx context.Context, program string, args []string, description string) ([]byte, error) {
	return r.Commands.Run(ctx, process.Command{Program: program, Args: args, Description: description, Timeout: 4*r.Config.Timeout + time.Minute, Stderr: r.Config.Progress})
}
func (r *Remote) shell(ctx context.Context, command string) ([]byte, error) {
	return r.invoke(ctx, "ssh", append(r.options(false), r.destination(), command), "remote command")
}
func (r *Remote) Open(ctx context.Context, local string, images map[string]string) error {
	if !hostPattern.MatchString(r.Config.Host) || !userPattern.MatchString(r.Config.User) || !rootPattern.MatchString(r.Config.Root) || strings.Contains("/"+r.Config.Root+"/", "/../") || path.Clean(r.Config.Root) == "/" {
		return fmt.Errorf("use a DNS/IPv4 host, valid SSH user, and absolute remote directory without traversal/spaces")
	}
	id := filepath.Base(local)
	if !runPattern.MatchString(id) {
		return fmt.Errorf("invalid local run identity")
	}
	r.LocalDirectory = local
	r.Directory = path.Join(r.Config.Root, ".nextcloud-updater", id)
	platform, e := r.shell(ctx, "uname -s -m")
	if e != nil {
		return e
	}
	fields := strings.Fields(string(platform))
	if len(fields) != 2 || fields[0] != "Linux" {
		return fmt.Errorf("the remote helper requires Linux")
	}
	arch := ""
	switch fields[1] {
	case "x86_64", "amd64":
		arch = "amd64"
	case "aarch64", "arm64":
		arch = "arm64"
	default:
		return fmt.Errorf("unsupported Linux architecture")
	}
	helper := filepath.Join(r.Config.HelperDirectory, "linux-"+arch, "muminovic-cloud-update")
	info, e := os.Stat(helper)
	if e != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("Linux helper missing; build the release with go run ./cmd/build")
	}
	choices := filepath.Join(local, "images.json")
	if e = filesystem.WriteJSON(choices, images); e != nil {
		return e
	}
	if _, e = r.shell(ctx, "umask 077 && mkdir -p -- "+Quote(r.Directory)); e != nil {
		return e
	}
	args := append(r.options(true), helper, choices, r.destination()+":"+r.Directory+"/")
	if _, e = r.invoke(ctx, "scp", args, "upload Go helper and image choices"); e != nil {
		return e
	}
	if _, e = r.shell(ctx, "chmod 700 -- "+Quote(path.Join(r.Directory, "muminovic-cloud-update"))); e != nil {
		return e
	}
	r.uploaded = true
	return nil
}
func (r *Remote) call(ctx context.Context, action string, major int, receipt string, out any, extra ...string) error {
	args := []string{path.Join(r.Directory, "muminovic-cloud-update"), "remote", action, "--compose-dir", r.Config.Root, "--run-dir", r.Directory, "--target", strconv.Itoa(r.Config.Target), "--timeout", strconv.Itoa(int(r.Config.Timeout.Seconds()))}
	if major > 0 {
		args = append(args, "--major", strconv.Itoa(major))
	}
	if receipt != "" {
		args = append(args, "--receipt", receipt)
	}
	if r.Config.Approved {
		args = append(args, "--approve-update")
	}
	args = append(args, extra...)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = Quote(a)
	}
	b, e := r.shell(ctx, strings.Join(quoted, " "))
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, out); e != nil {
		return fmt.Errorf("remote %s returned invalid JSON", action)
	}
	return nil
}
func (r *Remote) Plan(ctx context.Context) (domain.Plan, error) {
	var p domain.Plan
	e := r.call(ctx, "plan", 0, "", &p)
	return p, e
}
func (r *Remote) Prepare(ctx context.Context, major int) (domain.Step, error) {
	var s domain.Step
	e := r.call(ctx, "prepare", major, "", &s)
	return s, e
}
func (r *Remote) PrepareDatabase(ctx context.Context, series string) (domain.Step, error) {
	var step domain.Step
	e := r.call(ctx, "prepare-database", 0, "", &step, "--database-series", series)
	return step, e
}
func (r *Remote) Backup(ctx context.Context) (domain.Backup, error) {
	var b domain.Backup
	e := r.call(ctx, "backup", 0, "", &b)
	if e == nil && (path.Dir(b.Directory) != r.Directory || !strings.HasPrefix(path.Base(b.Directory), "before-")) {
		e = fmt.Errorf("remote backup returned an invalid stage")
	}
	return b, e
}
func (r *Remote) Deploy(ctx context.Context, receipt string) (domain.Result, error) {
	var v domain.Result
	e := r.call(ctx, "deploy", 0, receipt, &v)
	return v, e
}
func (r *Remote) Finish(ctx context.Context) error {
	var v struct{ Complete bool }
	return r.call(ctx, "finish", 0, "", &v)
}
func (r *Remote) Recover(ctx context.Context) (domain.Recovery, error) {
	if !r.uploaded {
		return domain.Recovery{Message: "No remote update was started."}, nil
	}
	var v domain.Recovery
	e := r.call(ctx, "recover", 0, "", &v)
	if !v.Recovered && e == nil {
		v.Message += " Remote state: " + path.Join(r.Directory, "state.json")
	}
	return v, e
}
func (r *Remote) Download(ctx context.Context, remote, local string) error {
	if !strings.HasPrefix(path.Clean(remote), r.Directory+"/") || !filesystem.Within(r.LocalDirectory, local) {
		return fmt.Errorf("backup transfer path is outside this run")
	}
	temporary := local + ".partial"
	_, e := r.invoke(ctx, "scp", append(r.options(true), r.destination()+":"+remote, temporary), "download backup")
	if e != nil {
		return e
	}
	return os.Rename(temporary, local)
}
