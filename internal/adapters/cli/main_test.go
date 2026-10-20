package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDefaultStopsBeforeAnyRemoteOrFilesystemSetup(t *testing.T) {
	var out, errors bytes.Buffer
	code := Run(context.Background(), []string{"--remote-host", "offline.invalid", "--config-dir", "does-not-exist"}, &out, &errors)
	if code != 0 || !strings.Contains(out.String(), "No server connection") {
		t.Fatal(code, out.String(), errors.String())
	}
}
func TestConflictingApprovalModesRefused(t *testing.T) {
	var out, errors bytes.Buffer
	if Run(context.Background(), []string{"--check-only", "--approve-update"}, &out, &errors) == 0 {
		t.Fatal("accepted conflicting approval modes")
	}
}
func TestLegacyBatchFlagsKeepApprovalGate(t *testing.T) {
	var out, errors bytes.Buffer
	if Run(context.Background(), []string{"-RemoteHost", "offline.invalid", "-TargetMajor", "35"}, &out, &errors) != 0 {
		t.Fatal(errors.String())
	}
	if !strings.Contains(out.String(), "No server connection") {
		t.Fatal(out.String())
	}
}
