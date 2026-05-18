package main

import (
	"context"
	"fmt"
	"time"

	"github.com/kuraos-org/kura/engine/app"
)

// lifecycleAppStopper adapts app.AppLifecycle to the backup.AppStopper
// interface. StopAll stops every running app (by listing Docker containers
// managed by kura and calling StopContainer on each). StartAll restarts
// them. This is best-effort for the pre-upgrade flow.
//
// DESIGN_PRINCIPLES priority #9: the backup engine declares the AppStopper
// interface; this adapter lives in cmd/kura so it stays inside the binary
// without creating a circular dependency.
type lifecycleAppStopper struct {
	lc *app.AppLifecycle
}

// StopAll lists all kura-managed containers and stops them gracefully.
// Returns the list of container names so StartAll can restart exactly them.
func (s *lifecycleAppStopper) StopAll(ctx context.Context) ([]string, error) {
	if s.lc == nil {
		return nil, nil
	}
	containers, err := s.lc.Docker.ListContainersByLabel(ctx, map[string]string{
		"kura.managed": "true",
	})
	if err != nil {
		// Non-fatal if Docker is unreachable — pre-upgrade snapshot still runs.
		return nil, fmt.Errorf("backup: list kura containers: %w", err)
	}
	var stopped []string
	for _, c := range containers {
		if err := s.lc.Docker.StopContainer(ctx, c.ID, 15*time.Second); err != nil {
			// Log but continue — we want to stop as many as possible.
			_ = err
			continue
		}
		stopped = append(stopped, c.ID)
	}
	return stopped, nil
}

// StartAll restarts previously stopped containers. Errors are swallowed so
// the upgrade result isn't polluted by partial restart failures — the operator
// can manually restart via the Apps page.
func (s *lifecycleAppStopper) StartAll(ctx context.Context, containerIDs []string) error {
	if s.lc == nil {
		return nil
	}
	for _, id := range containerIDs {
		_ = s.lc.Docker.StartContainer(ctx, id)
	}
	return nil
}
