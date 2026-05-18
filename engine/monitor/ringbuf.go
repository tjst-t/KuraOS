// Package monitor implements the KuraOS metrics collection stack.
//
// VISION: "自前バイナリ Ring Buffer" — Prometheus/Grafana are too heavy for a
// home NAS; the ring buffer is KuraOS's differentiator (DESIGN_PRINCIPLES #6).
// The ring buffer file format is a fixed-size binary file where each 16-byte
// entry wraps around and overwrites the oldest entry. This gives O(1) writes
// and bounded disk usage with no external dependencies.
package monitor

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EntrySize is the fixed byte-width of a single ring buffer entry.
// Layout (little-endian):
//
//	[0..7]  unix_ts_sec  int64
//	[8..9]  metric_id    uint16  (see metricID constants)
//	[10..11] pad          uint16  (reserved, always 0)
//	[12..15] value_uint32 uint32  (metric value * 100 for two decimal places)
const EntrySize = 16

// Ring buffer capacity defaults — sized so 30-second raw entries run 24 hours
// per metric before wrapping. 24h * 3600s / 30s = 2880 entries per metric.
// With 8 metric IDs × 2880 = 23040 entries × 16 bytes = ~360 KB total.
const DefaultCapacity = 23040

// metricID identifies which system metric is stored in an entry.
type metricID uint16

const (
	metricCPUPercent     metricID = 1
	metricMemUsedMB      metricID = 2
	metricMemTotalMB     metricID = 3
	metricNetRxMBps      metricID = 4
	metricNetTxMBps      metricID = 5
	metricDiskTempCelsius metricID = 6
	metricPoolUsedPct    metricID = 7
	metricPoolUsedGB     metricID = 8
)

// Entry is one decoded metric sample.
type Entry struct {
	Timestamp time.Time
	MetricID  metricID
	Value     float64 // MetricID-specific unit (see constants above)
}

// RingBuffer is a fixed-capacity, file-backed circular buffer of metric
// entries. Concurrent writes are serialized by mu; reads take the same lock.
// The file contains a 4-byte header (capacity uint32 LE) followed by capacity
// × EntrySize bytes of entry data.
type RingBuffer struct {
	mu       sync.Mutex
	f        *os.File
	capacity uint32
	writePos uint32 // next slot to write (0-indexed, wraps at capacity)
}

const headerSize = 8 // [0..3] capacity uint32 LE, [4..7] writePos uint32 LE

// OpenRingBuffer opens or creates a ring buffer at path with the given
// capacity (number of entries). When an existing file has a different
// capacity, it is truncated and reinitialised — this is intentional on
// config change.
func OpenRingBuffer(path string, capacity uint32) (*RingBuffer, error) {
	if capacity == 0 {
		return nil, fmt.Errorf("monitor: ring buffer capacity must be > 0")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("monitor: mkdir for ring buffer: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("monitor: open ring buffer %q: %w", path, err)
	}

	rb := &RingBuffer{f: f, capacity: capacity}
	if err := rb.init(capacity); err != nil {
		f.Close()
		return nil, err
	}
	return rb, nil
}

func (rb *RingBuffer) init(capacity uint32) error {
	info, err := rb.f.Stat()
	if err != nil {
		return fmt.Errorf("monitor: stat ring buffer: %w", err)
	}
	want := int64(headerSize) + int64(capacity)*EntrySize
	if info.Size() == want {
		// Existing file of correct size — read header to restore writePos.
		var hdr [headerSize]byte
		if _, err := rb.f.ReadAt(hdr[:], 0); err != nil {
			return fmt.Errorf("monitor: read ring buffer header: %w", err)
		}
		cap2 := binary.LittleEndian.Uint32(hdr[0:4])
		if cap2 == capacity {
			rb.writePos = binary.LittleEndian.Uint32(hdr[4:8])
			return nil
		}
	}
	// New file or capacity change — truncate and write blank header.
	if err := rb.f.Truncate(want); err != nil {
		return fmt.Errorf("monitor: truncate ring buffer: %w", err)
	}
	var hdr [headerSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], capacity)
	binary.LittleEndian.PutUint32(hdr[4:8], 0)
	if _, err := rb.f.WriteAt(hdr[:], 0); err != nil {
		return fmt.Errorf("monitor: write ring buffer header: %w", err)
	}
	rb.writePos = 0
	return nil
}

// Write appends one entry to the ring buffer, overwriting the oldest entry
// when the buffer is full.
func (rb *RingBuffer) Write(e Entry) error {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	var raw [EntrySize]byte
	binary.LittleEndian.PutUint64(raw[0:8], uint64(e.Timestamp.Unix()))
	binary.LittleEndian.PutUint16(raw[8:10], uint16(e.MetricID))
	// pad: zero
	binary.LittleEndian.PutUint32(raw[12:16], uint32(e.Value*100))

	offset := int64(headerSize) + int64(rb.writePos)*EntrySize
	if _, err := rb.f.WriteAt(raw[:], offset); err != nil {
		return fmt.Errorf("monitor: write entry at pos %d: %w", rb.writePos, err)
	}

	rb.writePos = (rb.writePos + 1) % rb.capacity

	// Persist writePos to header so restarts know where to continue.
	var hdrPos [4]byte
	binary.LittleEndian.PutUint32(hdrPos[:], rb.writePos)
	if _, err := rb.f.WriteAt(hdrPos[:], 4); err != nil {
		return fmt.Errorf("monitor: update header writePos: %w", err)
	}
	return nil
}

// ReadAll returns all non-zero entries in chronological order. Entries whose
// timestamp is zero are treated as unused (padding after a fresh file).
func (rb *RingBuffer) ReadAll() ([]Entry, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Read entire data region in one call for efficiency.
	data := make([]byte, int(rb.capacity)*EntrySize)
	if _, err := rb.f.ReadAt(data, headerSize); err != nil && err != io.EOF {
		return nil, fmt.Errorf("monitor: read ring buffer data: %w", err)
	}

	// Reconstruct chronological order: entries from writePos to capacity
	// then 0 to writePos-1 (the classic ring buffer traversal).
	out := make([]Entry, 0, rb.capacity)
	readOrder := func(start, end uint32) {
		for i := start; i < end; i++ {
			off := i * EntrySize
			ts := binary.LittleEndian.Uint64(data[off : off+8])
			if ts == 0 {
				continue // unused slot
			}
			mid := binary.LittleEndian.Uint16(data[off+8 : off+10])
			val := binary.LittleEndian.Uint32(data[off+12 : off+16])
			out = append(out, Entry{
				Timestamp: time.Unix(int64(ts), 0).UTC(),
				MetricID:  metricID(mid),
				Value:     float64(val) / 100.0,
			})
		}
	}
	readOrder(rb.writePos, rb.capacity)
	readOrder(0, rb.writePos)
	return out, nil
}

// Latest returns the most recent entry for each metric ID. If no entries
// exist for a metric, that metric is absent from the result.
func (rb *RingBuffer) Latest() (map[metricID]Entry, error) {
	entries, err := rb.ReadAll()
	if err != nil {
		return nil, err
	}
	out := make(map[metricID]Entry)
	for _, e := range entries {
		if prev, ok := out[e.MetricID]; !ok || e.Timestamp.After(prev.Timestamp) {
			out[e.MetricID] = e
		}
	}
	return out, nil
}

// Close flushes and releases the file handle.
func (rb *RingBuffer) Close() error {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.f == nil {
		return nil
	}
	if err := rb.f.Sync(); err != nil {
		return fmt.Errorf("monitor: sync ring buffer: %w", err)
	}
	return rb.f.Close()
}

// Clock is an interface for time.Now so tests can fast-forward the 30-second
// collection ticker without sleeping (DESIGN_PRINCIPLES #9 — interface for IO).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock is the production Clock implementation backed by the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Collector runs a goroutine every 30 seconds (by default) that probes the
// system and writes entries to the ring buffer. Tests inject a mock Clock and
// mock MetricsCollector to run multiple ticks without sleeping.
type Collector struct {
	rb       *RingBuffer
	probe    MetricsCollector
	clock    Clock
	interval time.Duration
}

// MetricsCollector is the probe interface — mocked in tests, implemented by
// sysProbe in production (DESIGN_PRINCIPLES #9).
type MetricsCollector interface {
	// CollectSnapshot returns the current system metrics. Errors are
	// best-effort; a partial Snapshot is acceptable (missing metrics are
	// simply not written to the ring buffer for this tick).
	CollectSnapshot(ctx context.Context) (Snapshot, error)
}

// Snapshot is a point-in-time reading of all monitored metrics. Zero values
// mean the probe was unable to read the metric this tick (e.g. no disks).
type Snapshot struct {
	CPUPercent      float64
	MemUsedMB       float64
	MemTotalMB      float64
	NetRxMBps       float64
	NetTxMBps       float64
	DiskTempCelsius float64 // average or max across drives; 0 if unavailable
	// Pool metrics are keyed by pool name.
	PoolUsedPct map[string]float64
	PoolUsedGB  map[string]float64
}

// NewCollector wires together a ring buffer and a probe. interval defaults to
// 30 seconds when 0.
func NewCollector(rb *RingBuffer, probe MetricsCollector, clock Clock, interval time.Duration) *Collector {
	if interval == 0 {
		interval = 30 * time.Second
	}
	if clock == nil {
		clock = RealClock{}
	}
	return &Collector{rb: rb, probe: probe, clock: clock, interval: interval}
}

// Run starts the collection loop. It blocks until ctx is cancelled. Errors
// from CollectSnapshot are logged but do not stop the loop — a transient
// sysfs read failure should not break monitoring for the rest of the session.
func (c *Collector) Run(ctx context.Context, errFn func(error)) {
	tick := func() {
		snap, err := c.probe.CollectSnapshot(ctx)
		if err != nil {
			if errFn != nil {
				errFn(fmt.Errorf("monitor: probe snapshot: %w", err))
			}
			return
		}
		now := c.clock.Now()
		entries := snapshotToEntries(snap, now)
		for _, e := range entries {
			if werr := c.rb.Write(e); werr != nil && errFn != nil {
				errFn(fmt.Errorf("monitor: write ring buffer: %w", werr))
			}
		}
	}
	// Collect immediately on first tick so the dashboard isn't blank for 30s.
	tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.clock.After(c.interval):
			tick()
		}
	}
}

func snapshotToEntries(s Snapshot, now time.Time) []Entry {
	var out []Entry
	add := func(mid metricID, val float64) {
		out = append(out, Entry{Timestamp: now, MetricID: mid, Value: val})
	}
	add(metricCPUPercent, s.CPUPercent)
	add(metricMemUsedMB, s.MemUsedMB)
	add(metricMemTotalMB, s.MemTotalMB)
	add(metricNetRxMBps, s.NetRxMBps)
	add(metricNetTxMBps, s.NetTxMBps)
	if s.DiskTempCelsius > 0 {
		add(metricDiskTempCelsius, s.DiskTempCelsius)
	}
	// Pool metrics use a single "aggregate" slot (latest pool-wide values).
	// In v1 we store the sum of all pool usages so the dashboard can show
	// a single sparkline. Per-pool metrics are in /metrics OpenMetrics output.
	var totalUsedPct, totalUsedGB float64
	for _, v := range s.PoolUsedPct {
		totalUsedPct = v // last pool wins for ring buffer aggregate
	}
	for _, v := range s.PoolUsedGB {
		totalUsedGB = v
	}
	if len(s.PoolUsedPct) > 0 {
		add(metricPoolUsedPct, totalUsedPct)
		add(metricPoolUsedGB, totalUsedGB)
	}
	return out
}
