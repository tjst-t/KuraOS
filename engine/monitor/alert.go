package monitor

import (
	"context"
	"fmt"
	"path"
	"time"
)

// AlertRule matches DESIGN_PRINCIPLES priority #1 SSOT: alert rules live in
// config.json under `alerts[]`. Each rule has a metric wildcard pattern
// (e.g. "pool.*.used_percent"), an operator, a threshold, and a severity.
//
// [AC-S8a756d-2-1] config.json の alerts ルールが評価され、超過時に Event Bus にイベントが publish される
type AlertRule struct {
	// Name is a human-readable identifier, also used as the Event.Source.
	Name string `json:"name"`
	// MetricPattern uses path.Match syntax:
	//   "pool.*.used_percent"  matches pool.tank.used_percent
	//   "cpu.percent"          matches cpu.percent
	//   "disk.temp"            matches disk.temp
	MetricPattern string `json:"metric_pattern"`
	// Op is the comparison operator: "gt" (>), "lt" (<), "gte" (>=), "lte" (<=), "eq" (==).
	Op string `json:"op"`
	// Threshold is the numeric value to compare against.
	Threshold float64 `json:"threshold"`
	// Severity sets the Event.Severity when the rule fires.
	Severity Severity `json:"severity"`
	// Category sets the Event.Category (e.g. "storage", "system").
	Category string `json:"category"`
	// CooldownMinutes suppresses repeated fires of the same rule within this
	// window. 0 = no cooldown (fires every evaluation cycle).
	CooldownMinutes int `json:"cooldown_minutes,omitempty"`
}

// AlertEvaluator takes a set of rules and a snapshot, evaluates every rule,
// and publishes events to the bus for rules that fire. It tracks per-rule
// cooldown state so noisy rules don't flood the bus.
type AlertEvaluator struct {
	rules   []AlertRule
	bus     *EventBus
	// lastFired tracks when each rule (by Name) last published an event.
	lastFired map[string]time.Time
}

// NewAlertEvaluator constructs an AlertEvaluator. rules must not be nil
// (an empty slice disables alerting without error).
func NewAlertEvaluator(rules []AlertRule, bus *EventBus) *AlertEvaluator {
	return &AlertEvaluator{
		rules:     rules,
		bus:       bus,
		lastFired: make(map[string]time.Time),
	}
}

// Evaluate checks every rule against the current snapshot and publishes events
// for any that fire. ctx is checked for cancellation.
func (ae *AlertEvaluator) Evaluate(ctx context.Context, snap Snapshot, now time.Time) {
	// Build a flat metric map from the snapshot so rules can match by name.
	metrics := snapshotToMetricMap(snap)

	for _, rule := range ae.rules {
		if ctx.Err() != nil {
			return
		}
		for metricName, value := range metrics {
			matched, err := path.Match(rule.MetricPattern, metricName)
			if err != nil || !matched {
				continue
			}
			if !evalOp(rule.Op, value, rule.Threshold) {
				continue
			}
			// Cooldown check.
			if rule.CooldownMinutes > 0 {
				if last, ok := ae.lastFired[rule.Name+":"+metricName]; ok {
					if now.Sub(last) < time.Duration(rule.CooldownMinutes)*time.Minute {
						continue
					}
				}
			}
			ae.lastFired[rule.Name+":"+metricName] = now
			ae.bus.Publish(Event{
				Timestamp:   now,
				Severity:    rule.Severity,
				Category:    rule.Category,
				Source:      "monitor",
				Title:       fmt.Sprintf("alert %s: %s = %.2f (%s %.2f)", rule.Name, metricName, value, rule.Op, rule.Threshold),
				MetricName:  metricName,
				MetricValue: value,
			})
		}
	}
}

// snapshotToMetricMap produces the flat name→value map that rules match against.
// Pool metric names are expanded to pool.<name>.used_percent etc. so wildcard
// patterns work correctly.
func snapshotToMetricMap(s Snapshot) map[string]float64 {
	m := map[string]float64{
		"cpu.percent":      s.CPUPercent,
		"memory.used_mb":   s.MemUsedMB,
		"memory.total_mb":  s.MemTotalMB,
		"disk.temp":        s.DiskTempCelsius,
	}
	for name, pct := range s.PoolUsedPct {
		m["pool."+name+".used_percent"] = pct
	}
	for name, gb := range s.PoolUsedGB {
		m["pool."+name+".used_gb"] = gb
	}
	return m
}

func evalOp(op string, value, threshold float64) bool {
	switch op {
	case "gt":
		return value > threshold
	case "lt":
		return value < threshold
	case "gte":
		return value >= threshold
	case "lte":
		return value <= threshold
	case "eq":
		return value == threshold
	default:
		return false
	}
}
