package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// [AC-S8a756d-1-1] 30 秒間隔の収集が動き、raw.bin が固定サイズで自動上書きされる

// fakeClock implements Clock for tests — controllable ticks.
type fakeClock struct {
	now  time.Time
	tick chan time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t, tick: make(chan time.Time, 8)}
}

func (fc *fakeClock) Now() time.Time                         { return fc.now }
func (fc *fakeClock) After(d time.Duration) <-chan time.Time { return fc.tick }
func (fc *fakeClock) advance(d time.Duration) {
	fc.now = fc.now.Add(d)
	fc.tick <- fc.now
}

// fakeProbe returns a fixed Snapshot for every call.
type fakeProbe struct {
	snap Snapshot
	err  error
}

func (fp *fakeProbe) CollectSnapshot(_ context.Context) (Snapshot, error) {
	return fp.snap, fp.err
}

func TestRingBufferFixedSize(t *testing.T) {
	// [AC-S8a756d-1-1]: raw.bin must stay at exactly the declared size even
	// after more entries are written than capacity allows.
	dir := t.TempDir()
	path := filepath.Join(dir, "raw.bin")
	const cap = 16

	rb, err := OpenRingBuffer(path, cap)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rb.Close()

	now := time.Unix(1_700_000_000, 0)

	// Write 2× capacity — should wrap without growing the file.
	for i := 0; i < cap*2; i++ {
		if werr := rb.Write(Entry{Timestamp: now.Add(time.Duration(i) * time.Second), MetricID: metricCPUPercent, Value: 42.5}); werr != nil {
			t.Fatalf("write[%d]: %v", i, werr)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	want := int64(headerSize) + int64(cap)*EntrySize
	if info.Size() != want {
		t.Errorf("file size = %d, want %d (fixed)", info.Size(), want)
	}
}

func TestRingBufferRoundTrip(t *testing.T) {
	// Write several entries, read them back in chronological order.
	dir := t.TempDir()
	rb, err := OpenRingBuffer(filepath.Join(dir, "raw.bin"), 32)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rb.Close()

	base := time.Unix(1_700_000_000, 0)
	entries := []Entry{
		{Timestamp: base, MetricID: metricCPUPercent, Value: 22.4},
		{Timestamp: base.Add(30 * time.Second), MetricID: metricMemUsedMB, Value: 9318},
		{Timestamp: base.Add(60 * time.Second), MetricID: metricCPUPercent, Value: 35.1},
	}
	for _, e := range entries {
		if err := rb.Write(e); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	got, err := rb.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("ReadAll len = %d, want %d", len(got), len(entries))
	}
	for i, e := range got {
		if e.MetricID != entries[i].MetricID {
			t.Errorf("[%d] MetricID = %v, want %v", i, e.MetricID, entries[i].MetricID)
		}
		if e.Timestamp.Unix() != entries[i].Timestamp.Unix() {
			t.Errorf("[%d] Timestamp = %v, want %v", i, e.Timestamp, entries[i].Timestamp)
		}
	}
}

func TestRingBufferWrapAround(t *testing.T) {
	// When capacity is 4 and we write 6 entries, the buffer wraps.
	// Latest() should return the two most-recent for each metric.
	dir := t.TempDir()
	rb, err := OpenRingBuffer(filepath.Join(dir, "raw.bin"), 4)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rb.Close()

	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 6; i++ {
		if err := rb.Write(Entry{Timestamp: base.Add(time.Duration(i) * 30 * time.Second), MetricID: metricCPUPercent, Value: float64(i * 10)}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	latest, err := rb.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if e, ok := latest[metricCPUPercent]; !ok {
		t.Error("latest: missing metricCPUPercent")
	} else if e.Value != 50.0 {
		t.Errorf("latest CPU = %.1f, want 50.0", e.Value)
	}
}

func TestCollectorTick(t *testing.T) {
	// [AC-S8a756d-1-1]: collector must write an entry for every tick.
	dir := t.TempDir()
	rb, err := OpenRingBuffer(filepath.Join(dir, "raw.bin"), 64)
	if err != nil {
		t.Fatalf("open ring buffer: %v", err)
	}
	defer rb.Close()

	probe := &fakeProbe{snap: Snapshot{
		CPUPercent: 22.4,
		MemUsedMB:  9318,
		MemTotalMB: 16384,
	}}
	clk := newFakeClock(time.Unix(1_700_000_000, 0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	coll := NewCollector(rb, probe, clk, 30*time.Second)
	done := make(chan struct{})
	go func() {
		coll.Run(ctx, func(e error) { t.Logf("collector err: %v", e) })
		close(done)
	}()

	// Advance clock 3 ticks.
	for i := 0; i < 3; i++ {
		clk.advance(30 * time.Second)
		time.Sleep(10 * time.Millisecond) // let the goroutine process
	}
	cancel()
	<-done

	entries, err := rb.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	// Initial tick + 3 ticks × multiple metrics per tick.
	if len(entries) < 4 {
		t.Errorf("expected ≥ 4 ring buffer entries after 3 ticks, got %d", len(entries))
	}
}

func TestRingBufferPersistReopen(t *testing.T) {
	// [AC-S8a756d-1-1]: writePos is persisted so data survives close/reopen.
	dir := t.TempDir()
	path := filepath.Join(dir, "raw.bin")

	rb, err := OpenRingBuffer(path, 32)
	if err != nil {
		t.Fatalf("open1: %v", err)
	}
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		if err := rb.Write(Entry{Timestamp: base.Add(time.Duration(i) * 30 * time.Second), MetricID: metricCPUPercent, Value: float64(i)}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	rb.Close()

	rb2, err := OpenRingBuffer(path, 32)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	defer rb2.Close()

	entries, err := rb2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("after reopen: expected 5 entries, got %d", len(entries))
	}
}
