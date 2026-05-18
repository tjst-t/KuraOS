package ui

import (
	"fmt"
	"net/http"
	"time"

	"github.com/kuraos-org/kura/engine/monitor"
	"github.com/kuraos-org/kura/i18n"
)

// DashboardDeps holds the optional monitor dependencies needed by the
// enhanced dashboard (S8a756d-1). When RingBuffer is nil, the dashboard
// falls back to the empty-state banner (pre-S8a756d behaviour).
type DashboardDeps struct {
	// RingBuffer is the production ring buffer; nil on dev boxes without
	// ZFS / systemd (the handler gracefully degrades to empty-state banner).
	RingBuffer *monitor.RingBuffer
	// EventStore, when non-nil, supplies the Recent Events panel.
	EventStore *monitor.EventStore
}

// dashboardExtra is passed as PageData.Extra to the dashboard template.
type dashboardExtra struct {
	HasMetrics      bool
	CPUPercent      float64
	MemUsedGB       float64
	MemTotalGB      float64
	DiskTempCelsius float64
	PoolUsedPct     float64
	LastUpdated     time.Time
	LastUpdatedFmt  string
	Events          []dashboardEvent
}

type dashboardEvent struct {
	SeverityClass string
	Title         string
	Source        string
	TimeAgo       string
}

// SetDashboardDeps wires the monitor ring buffer into the Dashboard handler.
// Must be called before Routes(). When not called (or nil), the dashboard
// shows the pre-S8a756d empty-state banner.
func (r *Renderer) SetDashboardDeps(deps DashboardDeps) {
	r.dashboardDeps = &deps
}

// handleDashboard is the GET /ui/admin/dashboard route. It reads the latest
// ring buffer snapshot and passes it to the dashboard template.
func (r *Renderer) handleDashboard(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	data := r.buildPageData("dashboard", i18n.MsgNavDashboard)

	if r.dashboardDeps != nil && r.dashboardDeps.RingBuffer != nil {
		stats, err := monitor.LatestStats(r.dashboardDeps.RingBuffer)
		extra := &dashboardExtra{
			HasMetrics:      err == nil && !stats.LastUpdated.IsZero(),
			CPUPercent:      stats.CPUPercent,
			MemUsedGB:       stats.MemUsedGB,
			MemTotalGB:      stats.MemTotalMB / 1024.0,
			DiskTempCelsius: stats.DiskTempCelsius,
			PoolUsedPct:     stats.PoolUsedPct,
			LastUpdated:     stats.LastUpdated,
		}
		if !stats.LastUpdated.IsZero() {
			extra.LastUpdatedFmt = formatAgo(stats.LastUpdated)
		}

		if r.dashboardDeps.EventStore != nil {
			rows, _ := r.dashboardDeps.EventStore.List(req.Context(), "", "", 5)
			for _, row := range rows {
				extra.Events = append(extra.Events, dashboardEvent{
					SeverityClass: severityClass(string(row.Severity)),
					Title:         row.Title,
					Source:        row.Source,
					TimeAgo:       formatAgo(row.OccurredAt),
				})
			}
		}
		data.Extra = extra
	}

	r.render(w, "templates/pages/dashboard.tmpl", data)
}

func severityClass(s string) string {
	switch s {
	case "critical":
		return "crit"
	case "warning":
		return "warn"
	case "ok":
		return "ok"
	default:
		return "info"
	}
}

func formatAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "たった今"
	case d < time.Hour:
		return fmt.Sprintf("%d 分前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 時間前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 日前", int(d.Hours()/24))
	}
}
