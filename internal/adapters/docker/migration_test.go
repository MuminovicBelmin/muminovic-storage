package docker

import (
	"context"
	"fmt"
	"nextcloud-updater/internal/adapters/process"
	"nextcloud-updater/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestCompletedRecurringPreviewJobDoesNotBlockOrExecuteAgain(t *testing.T) {
	c := &commands{handle: func(c process.Command) ([]byte, error) {
		a := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(a, "ps -aq"):
			return []byte("app"), nil
		case strings.Contains(a, "background-job:list"):
			return []byte(`[{"id":123,"class":"OC\\Core\\BackgroundJobs\\PreviewMigrationJob"}]`), nil
		case strings.Contains(a, "config:app:get core previewMovedDone"):
			return []byte("1\n"), nil
		default:
			return nil, fmt.Errorf("completed migration executed unexpectedly")
		}
	}}
	r := Runtime{Commands: c, Timeout: time.Second}
	if err := r.migrations(context.Background(), domain.Deployment{Project: "fixture"}); err != nil {
		t.Fatal(err)
	}
}
