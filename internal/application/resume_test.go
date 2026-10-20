package application_test

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/domain"
	"testing"
)

func TestInterruptedDeploymentCanOnlyResumeVerificationWithReceiptAndApproval(t *testing.T) {
	d, s, runs := setup(t, 31)
	ctx := context.Background()
	if _, err := s.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prepare(ctx, 30, true); err != nil {
		t.Fatal(err)
	}
	b, err := s.Backup(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	d.verifyError = fmt.Errorf("interrupted verification")
	if _, err = s.Deploy(ctx, b.SHA256, true); err == nil {
		t.Fatal("expected interrupted verification")
	}
	d.verifyError = nil
	if _, err = s.ResumeVerification(ctx, b.SHA256, false); err == nil {
		t.Fatal("approval bypass")
	}
	if _, err = s.ResumeVerification(ctx, "wrong", true); err == nil {
		t.Fatal("receipt bypass")
	}
	if _, err = s.ResumeVerification(ctx, b.SHA256, true); err != nil {
		t.Fatal(err)
	}
	state, _ := runs.Load()
	if state.Phase != domain.Verified || d.applied != 1 || d.dumps != 1 {
		t.Fatal("verification reapplied deployment or lost state")
	}
}
