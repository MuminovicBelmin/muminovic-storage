// Package cli is the composition root: it connects application ports to concrete adapters.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"nextcloud-updater/internal/adapters/backup"
	"nextcloud-updater/internal/adapters/compose"
	"nextcloud-updater/internal/adapters/docker"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/adapters/process"
	"nextcloud-updater/internal/adapters/ssh"
	"nextcloud-updater/internal/application"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func Run(ctx context.Context, args []string, out, errors io.Writer) int {
	var err error
	if len(args) > 0 && args[0] == "remote" {
		err = remote(ctx, args[1:], out, errors)
	} else {
		err = local(ctx, args, out, errors)
	}
	if err != nil {
		fmt.Fprintln(errors, "UPDATE STOPPED:", err)
		return 1
	}
	return 0
}
func local(ctx context.Context, args []string, out, errors io.Writer) error {
	f := flag.NewFlagSet("nextcloud-update", flag.ContinueOnError)
	f.SetOutput(errors)
	var host, user, root, backupRoot, configDir, helperDir string
	var port, target, seconds int
	var check, approve bool
	for _, name := range []string{"remote-host", "RemoteHost"} {
		f.StringVar(&host, name, "", "SSH DNS/IPv4 host")
	}
	for _, name := range []string{"remote-user", "RemoteUser"} {
		f.StringVar(&user, name, "root", "SSH user")
	}
	for _, name := range []string{"compose-dir", "ComposeDirectory"} {
		f.StringVar(&root, name, "/home/Nextcloud", "remote deployment directory")
	}
	for _, name := range []string{"backup-root", "BackupRoot"} {
		f.StringVar(&backupRoot, name, "", "local backup destination")
	}
	for _, name := range []string{"remote-port", "RemotePort"} {
		f.IntVar(&port, name, 22, "SSH port")
	}
	for _, name := range []string{"target-major", "TargetMajor"} {
		f.IntVar(&target, name, 35, "target Nextcloud major")
	}
	for _, name := range []string{"timeout", "UpgradeTimeoutSeconds"} {
		f.IntVar(&seconds, name, 3600, "timeout seconds per long operation")
	}
	for _, name := range []string{"check-only", "CheckOnly"} {
		f.BoolVar(&check, name, false, "preflight only")
	}
	for _, name := range []string{"approve-update", "ApproveUpdate"} {
		f.BoolVar(&approve, name, false, "explicitly authorize backup/maintenance/upgrades")
	}
	f.StringVar(&configDir, "config-dir", ".", "directory containing the versioned Compose files")
	f.StringVar(&helperDir, "helper-dir", "", "directory containing the Linux helper builds")
	if e := f.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if !approve && !check {
		fmt.Fprintln(out, "No server connection or update was started. Use --check-only, or explicitly --approve-update.")
		return nil
	}
	if approve && check {
		return fmt.Errorf("choose either --check-only or --approve-update")
	}
	if target < 33 || target > 35 || seconds < 60 || seconds > 86400 || port < 1 || port > 65535 {
		return fmt.Errorf("invalid target, timeout or SSH port")
	}
	if host == "" {
		return fmt.Errorf("--remote-host is required")
	}
	var e error
	configDir, e = filepath.Abs(configDir)
	if e != nil {
		return e
	}
	if backupRoot == "" {
		backupRoot = filepath.Join(configDir, "backups")
	}
	if helperDir == "" {
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		helperDir = filepath.Dir(exe)
	}
	images, e := compose.ImageChoices(target)
	if e != nil {
		return e
	}
	transport := &ssh.Remote{Config: ssh.Config{Host: host, User: user, Root: root, Port: port, Target: target, Timeout: time.Duration(seconds) * time.Second, Approved: approve, HelperDirectory: helperDir, Progress: errors}, Commands: process.Native{}}
	service := application.LocalService{Remote: transport, Backups: backup.Local{}, Log: func(s string) { fmt.Fprintln(out, s) }}
	return service.Run(ctx, application.LocalOptions{Approved: approve, CheckOnly: check, BackupRoot: backupRoot, Images: images})
}
func remote(ctx context.Context, args []string, out, errors io.Writer) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("remote mode is available on Linux only")
	}
	if len(args) == 0 {
		return fmt.Errorf("remote action is required")
	}
	action := args[0]
	f := flag.NewFlagSet("remote "+action, flag.ContinueOnError)
	f.SetOutput(errors)
	var root, dir, receipt, series string
	var target, major, seconds int
	var approve bool
	f.StringVar(&root, "compose-dir", "", "remote deployment directory")
	f.StringVar(&dir, "run-dir", "", "remote run directory")
	f.StringVar(&receipt, "receipt", "", "locally verified manifest hash")
	f.StringVar(&series, "database-series", "", "reviewed MariaDB LTS step")
	f.IntVar(&target, "target", 35, "target major")
	f.IntVar(&major, "major", 0, "current step")
	f.IntVar(&seconds, "timeout", 3600, "operation timeout seconds")
	f.BoolVar(&approve, "approve-update", false, "explicit operator approval")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if root == "" || dir == "" || target < 33 || target > 35 || seconds < 60 || seconds > 86400 || f.NArg() != 0 {
		return fmt.Errorf("invalid remote arguments")
	}
	if e := filesystem.RequireRoot(); e != nil {
		return e
	}
	runs, e := filesystem.NewRuns(root, dir)
	if e != nil {
		return e
	}
	lease, e := filesystem.ActionLock(filepath.Join(runs.Directory, "action.lock"))
	if e != nil {
		return e
	}
	defer lease.Close()
	images := map[string]string{}
	if e = filesystem.ReadJSON(filepath.Join(runs.Directory, "images.json"), &images); e != nil {
		return e
	}
	runtimeAdapter := &docker.Runtime{Commands: process.Native{}, Timeout: time.Duration(seconds) * time.Second}
	service := application.RemoteService{Deployment: runtimeAdapter, Backups: &backup.Remote{Dumper: runtimeAdapter, RunDirectory: runs.Directory}, Runs: runs, Root: runs.Root, Images: images, Target: target}
	var result any
	switch action {
	case "plan":
		result, e = service.Plan(ctx)
	case "prepare":
		result, e = service.Prepare(ctx, major, approve)
	case "prepare-database":
		result, e = service.PrepareDatabase(ctx, series, approve)
	case "backup":
		result, e = service.Backup(ctx, approve)
	case "deploy":
		result, e = service.Deploy(ctx, receipt, approve)
	case "verify-deployment":
		result, e = service.ResumeVerification(ctx, receipt, approve)
	case "recover":
		result, e = service.Recover(ctx, approve)
	case "finish":
		e = service.Finish()
		result = map[string]bool{"complete": e == nil}
	default:
		return fmt.Errorf("unknown remote action")
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}
