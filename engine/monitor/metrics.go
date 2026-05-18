package monitor

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// OpenMetricsWriter writes the latest ring buffer snapshot in OpenMetrics
// (Prometheus) text format to w.
//
// [AC-S8a756d-1-2] /metrics エンドポイントが OpenMetrics 形式で現在値を返す
func OpenMetricsWriter(rb *RingBuffer, w io.Writer) error {
	latest, err := rb.Latest()
	if err != nil {
		return fmt.Errorf("monitor: read latest: %w", err)
	}

	now := time.Now().UnixMilli()

	writeGauge := func(name, help string, value float64, labels ...string) {
		fmt.Fprintf(w, "# HELP %s %s\n", name, help)
		fmt.Fprintf(w, "# TYPE %s gauge\n", name)
		labelStr := ""
		if len(labels) > 0 {
			labelStr = "{" + strings.Join(labels, ",") + "}"
		}
		fmt.Fprintf(w, "%s%s %.4f %d\n", name, labelStr, value, now)
	}

	if e, ok := latest[metricCPUPercent]; ok {
		writeGauge("kura_cpu_usage_percent", "Current CPU usage percentage", e.Value)
	}
	if e, ok := latest[metricMemUsedMB]; ok {
		writeGauge("kura_memory_used_mb", "Memory currently in use (MB)", e.Value)
	}
	if e, ok := latest[metricMemTotalMB]; ok {
		writeGauge("kura_memory_total_mb", "Total system memory (MB)", e.Value)
	}
	if e, ok := latest[metricNetRxMBps]; ok {
		writeGauge("kura_net_rx_mbps", "Network receive rate (MB/s)", e.Value)
	}
	if e, ok := latest[metricNetTxMBps]; ok {
		writeGauge("kura_net_tx_mbps", "Network transmit rate (MB/s)", e.Value)
	}
	if e, ok := latest[metricDiskTempCelsius]; ok {
		writeGauge("kura_disk_temp_celsius", "Disk temperature in Celsius (max across drives)", e.Value)
	}
	if e, ok := latest[metricPoolUsedPct]; ok {
		writeGauge("kura_pool_used_percent", "ZFS pool used percentage (aggregate)", e.Value)
	}
	if e, ok := latest[metricPoolUsedGB]; ok {
		writeGauge("kura_pool_used_gb", "ZFS pool used capacity in GB (aggregate)", e.Value)
	}

	// OpenMetrics terminator
	fmt.Fprintf(w, "# EOF\n")
	return nil
}

// DashboardStats returns the latest values formatted for the Dashboard page.
// Missing metrics are returned as 0 / empty string so the template can
// detect and show a placeholder.
type DashboardStats struct {
	CPUPercent      float64
	MemUsedMB       float64
	MemTotalMB      float64
	MemUsedGB       float64
	DiskTempCelsius float64
	PoolUsedPct     float64
	PoolUsedGB      float64
	LastUpdated     time.Time
}

// LatestStats extracts dashboard-ready stats from the ring buffer.
func LatestStats(rb *RingBuffer) (DashboardStats, error) {
	m, err := rb.Latest()
	if err != nil {
		return DashboardStats{}, err
	}
	s := DashboardStats{}
	if e, ok := m[metricCPUPercent]; ok {
		s.CPUPercent = e.Value
		s.LastUpdated = e.Timestamp
	}
	if e, ok := m[metricMemUsedMB]; ok {
		s.MemUsedMB = e.Value
		s.MemUsedGB = e.Value / 1024.0
	}
	if e, ok := m[metricMemTotalMB]; ok {
		s.MemTotalMB = e.Value
	}
	if e, ok := m[metricDiskTempCelsius]; ok {
		s.DiskTempCelsius = e.Value
	}
	if e, ok := m[metricPoolUsedPct]; ok {
		s.PoolUsedPct = e.Value
	}
	if e, ok := m[metricPoolUsedGB]; ok {
		s.PoolUsedGB = e.Value
	}
	return s, nil
}
