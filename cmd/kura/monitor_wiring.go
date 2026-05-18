package main

import (
	"context"
	"time"

	"github.com/kuraos-org/kura/engine/monitor"
)

// alertingProbe wraps a MetricsCollector and an AlertEvaluator so that every
// snapshot collected by the ring buffer also triggers alert rule evaluation.
// This keeps monitor.Collector decoupled from the alert engine (DESIGN_PRINCIPLES #9).
type alertingProbe struct {
	probe monitor.MetricsCollector
	ae    *monitor.AlertEvaluator
}

func (ap *alertingProbe) CollectSnapshot(ctx context.Context) (monitor.Snapshot, error) {
	snap, err := ap.probe.CollectSnapshot(ctx)
	if err == nil && ap.ae != nil {
		ap.ae.Evaluate(ctx, snap, time.Now())
	}
	return snap, err
}
