package application

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/domain"
	"strings"
)

// ResumeVerification never reapplies an image or migration. It only accepts the
// recorded target, original verified backup receipt and unchanged deployment.
func (s *RemoteService) ResumeVerification(ctx context.Context, receipt string, approved bool) (domain.Result, error) {
	state, err := s.load("deploy", approved)
	if err != nil {
		return domain.Result{}, err
	}
	if state.Phase != domain.DeploymentStarted || state.Kind != "nextcloud" || receipt == "" || receipt != state.Backup.SHA256 {
		return domain.Result{}, fmt.Errorf("an interrupted Nextcloud deployment and its verified backup receipt are required")
	}
	if err = s.Backups.Verify(state.Backup); err != nil {
		return domain.Result{}, err
	}
	live, err := s.Deployment.Discover(ctx, s.Root)
	if err != nil {
		return domain.Result{}, err
	}
	d := state.Deployment
	sameDatabase := live.DatabaseID == d.DatabaseID || (len(d.DatabaseID) >= 12 && strings.HasPrefix(live.DatabaseID, d.DatabaseID))
	if live.Source != d.Source || live.Project != d.Project || !sameDatabase || live.DatabaseImage != d.DatabaseImage {
		return domain.Result{}, fmt.Errorf("deployment identity changed; manual assessment required")
	}
	status, err := s.Deployment.Verify(ctx, d, state.Target)
	if err != nil {
		return domain.Result{}, err
	}
	return s.completeDeployment(ctx, state, status)
}
