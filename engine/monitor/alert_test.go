package monitor

import (
	"context"
	"testing"
	"time"
)

// [AC-S8a756d-2-1] config.json の alerts ルールが評価され、超過時に Event Bus にイベントが publish される

func TestAlertEvaluatorFires(t *testing.T) {
	ctx := context.Background()
	received := make(chan Event, 8)
	bus := NewEventBus(nil)
	sub := bus.Subscribe(ctx, 8)
	go func() {
		for e := range sub {
			received <- e
		}
	}()

	rules := []AlertRule{
		{
			Name:          "pool-usage-high",
			MetricPattern: "pool.*.used_percent",
			Op:            "gt",
			Threshold:     80.0,
			Severity:      SeverityWarning,
			Category:      "storage",
		},
		{
			Name:          "cpu-high",
			MetricPattern: "cpu.percent",
			Op:            "gt",
			Threshold:     90.0,
			Severity:      SeverityCritical,
			Category:      "system",
		},
	}

	ae := NewAlertEvaluator(rules, bus)
	snap := Snapshot{
		CPUPercent: 25.0, // under threshold
		PoolUsedPct: map[string]float64{
			"tank": 85.0, // OVER threshold
			"fast": 40.0, // under threshold
		},
		PoolUsedGB: map[string]float64{
			"tank": 18.3,
			"fast": 0.42,
		},
	}
	now := time.Unix(1_700_000_000, 0)
	ae.Evaluate(ctx, snap, now)

	// Only pool.tank.used_percent should fire.
	select {
	case ev := <-received:
		if ev.MetricName != "pool.tank.used_percent" {
			t.Errorf("event MetricName = %q, want pool.tank.used_percent", ev.MetricName)
		}
		if ev.Severity != SeverityWarning {
			t.Errorf("event Severity = %q, want warning", ev.Severity)
		}
		if ev.Category != "storage" {
			t.Errorf("event Category = %q, want storage", ev.Category)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected alert event but none received")
	}

	// Ensure no spurious event for cpu (25 < 90) or fast pool.
	select {
	case ev := <-received:
		t.Errorf("unexpected event: %+v", ev)
	case <-time.After(20 * time.Millisecond):
		// good — no spurious events
	}
}

func TestAlertEvaluatorCooldown(t *testing.T) {
	ctx := context.Background()
	bus := NewEventBus(nil)
	sub := bus.Subscribe(ctx, 8)
	received := make(chan Event, 8)
	go func() {
		for e := range sub {
			received <- e
		}
	}()

	rules := []AlertRule{{
		Name:            "pool-usage-high",
		MetricPattern:   "pool.*.used_percent",
		Op:              "gt",
		Threshold:       80.0,
		Severity:        SeverityWarning,
		Category:        "storage",
		CooldownMinutes: 60,
	}}
	ae := NewAlertEvaluator(rules, bus)
	snap := Snapshot{
		PoolUsedPct: map[string]float64{"tank": 85.0},
		PoolUsedGB:  map[string]float64{"tank": 18.3},
	}
	now := time.Unix(1_700_000_000, 0)

	ae.Evaluate(ctx, snap, now)
	ae.Evaluate(ctx, snap, now.Add(5*time.Minute))  // within cooldown — no fire
	ae.Evaluate(ctx, snap, now.Add(61*time.Minute)) // past cooldown — fires

	got := 0
	drain:
	for {
		select {
		case <-received:
			got++
		case <-time.After(20 * time.Millisecond):
			break drain
		}
	}
	if got != 2 {
		t.Errorf("expected 2 events (first + after cooldown), got %d", got)
	}
}

func TestAlertWildcardPattern(t *testing.T) {
	ctx := context.Background()
	bus := NewEventBus(nil)
	sub := bus.Subscribe(ctx, 8)
	received := make(chan Event, 8)
	go func() {
		for e := range sub {
			received <- e
		}
	}()

	// Wildcard should match all pools.
	rules := []AlertRule{{
		Name:          "any-pool-full",
		MetricPattern: "pool.*.used_percent",
		Op:            "gt",
		Threshold:     75.0,
		Severity:      SeverityCritical,
		Category:      "storage",
	}}
	ae := NewAlertEvaluator(rules, bus)
	snap := Snapshot{
		PoolUsedPct: map[string]float64{
			"tank": 82.0,
			"fast": 79.0,
		},
		PoolUsedGB: map[string]float64{
			"tank": 18.3,
			"fast": 0.42,
		},
	}
	ae.Evaluate(ctx, snap, time.Now())

	got := 0
	drain:
	for {
		select {
		case <-received:
			got++
		case <-time.After(50 * time.Millisecond):
			break drain
		}
	}
	if got != 2 {
		t.Errorf("expected 2 events (both pools over threshold), got %d", got)
	}
}

func TestEventBusPublishSubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewEventBus(nil)
	sub := bus.Subscribe(ctx, 4)

	ev := Event{
		Severity: SeverityInfo,
		Category: "test",
		Title:    "hello",
	}
	bus.Publish(ev)

	select {
	case got := <-sub:
		if got.Title != "hello" {
			t.Errorf("got title %q, want %q", got.Title, "hello")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no event received")
	}

	// After cancel the subscriber channel should be closed.
	cancel()
	time.Sleep(20 * time.Millisecond)
	// Sending to a closed channel would panic; try to receive to confirm close.
	_, ok := <-sub
	if ok {
		t.Error("expected subscriber channel to be closed after ctx cancel")
	}
}
