// storage.go renders the /ui/admin/storage page from a storage.Engine.
//
// The handler shape mirrors auth.go / setup.go from S1e7eeb: deps in a struct,
// a single per-request handler, all data shaped into a view-model so the
// template stays free of CLI tokens. Visual SSOT for this page is
// prototype/claude_design/Storage.html — PoolCard / disks table / import
// banner. Volume / snapshot / scrub tabs from the prototype are left for
// later sprints (CLAUDE.md: "書き込み系 (CreatePool 等) は次スプリント").
package ui

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kuraos-org/kura/engine/storage"
	"github.com/kuraos-org/kura/i18n"
)

// StorageDeps is what the storage page handler reads from the binary.
// The Engine is an interface for the same priority #9 testability reason
// the auth deps use small interfaces — the test suite injects a fake
// storage.Engine without booting any Executor.
type StorageDeps struct {
	Engine StorageEngineReader
}

// StorageEngineReader is the read-only subset of storage.Engine the UI uses.
// Defined here so the gateway tests can stub it locally without depending
// on the engine package.
type StorageEngineReader interface {
	ListPools(ctx context.Context) ([]storage.Pool, error)
	ListDisks(ctx context.Context) ([]storage.Disk, error)
	ListImportable(ctx context.Context) ([]storage.ImportablePool, error)
}

// StorageView is the template view-model. Pre-formatted strings live here so
// templates stay free of business logic; raw structs travel via Pools/Disks
// for unit tests that want to assert the underlying values.
type StorageView struct {
	Subtitle    string
	HasPools    bool
	HasDisks    bool
	HasImports  bool
	ImportError string

	Pools    []PoolView
	Disks    []DiskView
	Imports  []ImportableView
	Importer ImportBanner
}

// ImportBanner aggregates the cross-pool count for the page-top banner.
type ImportBanner struct {
	Visible    bool
	Count      int
	First      string
	BannerText string
	Deferred   string
}

// PoolView is the row of data each PoolCard renders. Capacity / used / free
// are pre-formatted to "12.4 TB" so the template avoids unit math.
type PoolView struct {
	Name         string
	GUID         string
	HealthBadge  string
	HealthClass  string
	Topology     string
	UsedBytes    int64
	SizeBytes    int64
	CapPercent   int
	FragPercent  int
	UsedDisplay  string
	SizeDisplay  string
	UsedSentence string
	FragSentence string
	BarClass     string
	Vdevs        []VdevView
}

// VdevView is one row in the topology block under a PoolCard. We render at
// most one level deep (pool → top vdev → leaf) since deeper nesting is rare
// in practice and already covered visually by the parent badge.
type VdevView struct {
	Type        string
	TypeLabel   string
	Layout      string
	HealthBadge string
	HealthClass string
	Children    []LeafView
}

type LeafView struct {
	Path        string
	HealthBadge string
	HealthClass string
}

// DiskView is one row in the disks table. UsageLabel / SMARTBadge are pre-
// translated; the template renders them verbatim.
type DiskView struct {
	Path        string
	Model       string
	Serial      string
	Pool        string
	SizeDisplay string
	TempDisplay string
	HoursLabel  string
	UsageLabel  string
	UsageClass  string
	SMARTBadge  string
	SMARTClass  string
}

// ImportableView is one row in the importable-pools table.
type ImportableView struct {
	Name        string
	GUID        string
	StateBadge  string
	StateClass  string
	Description string
	Topology    string
}

// StorageHandler returns an http.Handler for /ui/admin/storage. The handler
// is mounted under the admin-role middleware by the gateway, so the request
// is guaranteed to be authenticated as an admin by the time we run.
func (r *Renderer) StorageHandler(d StorageDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		view := r.buildStorageView(req.Context(), d)
		data := r.buildPageData("storage", i18n.MsgStorageTitle)
		data.Extra = view
		r.render(w, "templates/pages/storage.tmpl", data)
	})
}

// buildStorageView is broken out from the handler so unit tests can
// exercise the formatter (i18n + bytes-to-TB) without spinning up an
// http server.
func (r *Renderer) buildStorageView(ctx context.Context, d StorageDeps) StorageView {
	pools, perr := d.Engine.ListPools(ctx)
	disks, _ := d.Engine.ListDisks(ctx)
	imports, _ := d.Engine.ListImportable(ctx)

	view := StorageView{
		Pools:      poolsToView(r.tr, pools),
		Disks:      disksToView(r.tr, disks),
		Imports:    importsToView(r.tr, imports),
		HasPools:   len(pools) > 0,
		HasDisks:   len(disks) > 0,
		HasImports: len(imports) > 0,
	}
	view.Subtitle = r.tr.T(i18n.MsgStorageSubtitle, len(pools), len(disks))
	if len(imports) > 0 {
		view.Importer = ImportBanner{
			Visible:    true,
			Count:      len(imports),
			First:      imports[0].Name,
			BannerText: bannerText(r.tr, imports),
			Deferred:   r.tr.T(i18n.MsgStorageImportDeferred),
		}
	}
	if perr != nil {
		// DESIGN_PRINCIPLES forbidden: do NOT splat raw zpool error text into
		// the page. Map the error to a translated category instead. The
		// developer message goes nowhere visible; operators see a sentence
		// like "ZFS が利用できません" plus a known reason code.
		reason := r.tr.T(classifyStorageErrMsgID(perr))
		view.ImportError = r.tr.T(i18n.MsgStorageError, reason)
	}
	return view
}

// classifyStorageErrMsgID maps a wrapped engine error to a translated reason
// MessageID. We match on substrings of the underlying go error rather than
// import os/exec to type-check, because the CLAUDE.md "no plugin" /
// dependency-minimization stance keeps the package boundary thin.
func classifyStorageErrMsgID(err error) i18n.MessageID {
	s := err.Error()
	switch {
	case strings.Contains(s, "executable file not found"):
		return "page.storage.error.zfs_unavailable"
	case strings.Contains(s, "permission denied"):
		return "page.storage.error.zfs_permission"
	case strings.Contains(s, "context deadline") || strings.Contains(s, "context canceled"):
		return "page.storage.error.timeout"
	}
	return "page.storage.error.unknown"
}

func bannerText(tr translator, ps []storage.ImportablePool) string {
	if len(ps) == 1 {
		return tr.T(i18n.MsgStorageImportBannerOne, ps[0].Name)
	}
	return tr.T(i18n.MsgStorageImportBanner, len(ps))
}

// translator is the minimal Translator interface the formatters depend on.
// Defined here so unit tests can pass a stub without importing i18n.
type translator interface {
	T(id i18n.MessageID, args ...any) string
}

func poolsToView(tr translator, pools []storage.Pool) []PoolView {
	out := make([]PoolView, 0, len(pools))
	for _, p := range pools {
		v := PoolView{
			Name:         p.Name,
			GUID:         p.GUID,
			HealthBadge:  healthLabel(tr, p.Health),
			HealthClass:  healthClass(p.Health),
			Topology:     topologySummary(p.Topology),
			UsedBytes:    p.AllocatedBytes,
			SizeBytes:    p.SizeBytes,
			CapPercent:   p.CapPercent,
			FragPercent:  p.FragPercent,
			UsedDisplay:  formatBytes(p.AllocatedBytes),
			SizeDisplay:  formatBytes(p.SizeBytes),
			UsedSentence: tr.T(i18n.MsgStoragePoolUsed, formatBytes(p.AllocatedBytes), formatBytes(p.SizeBytes)),
			FragSentence: tr.T(i18n.MsgStoragePoolFrag, p.FragPercent),
			BarClass:     barClass(p.CapPercent),
			Vdevs:        vdevsToView(tr, p.Topology),
		}
		out = append(out, v)
	}
	return out
}

func vdevsToView(tr translator, vs []storage.Vdev) []VdevView {
	out := make([]VdevView, 0, len(vs))
	for _, v := range vs {
		vv := VdevView{
			Type:        string(v.Type),
			TypeLabel:   vdevTypeLabel(tr, v.Type),
			Layout:      string(v.Layout),
			HealthBadge: healthLabel(tr, v.Health),
			HealthClass: healthClass(v.Health),
		}
		for _, c := range v.Children {
			vv.Children = append(vv.Children, LeafView{
				Path:        c.Path,
				HealthBadge: healthLabel(tr, c.Health),
				HealthClass: healthClass(c.Health),
			})
		}
		// A flush-left leaf (cache disk, log disk, spare) shows up as a
		// type with no children but its own path — surface it as a single
		// leaf so the template renders the disk path.
		if len(v.Children) == 0 && v.Path != "" {
			vv.Children = []LeafView{{
				Path:        v.Path,
				HealthBadge: healthLabel(tr, v.Health),
				HealthClass: healthClass(v.Health),
			}}
		}
		out = append(out, vv)
	}
	return out
}

func disksToView(tr translator, disks []storage.Disk) []DiskView {
	out := make([]DiskView, 0, len(disks))
	for _, d := range disks {
		out = append(out, DiskView{
			Path:        d.Path,
			Model:       d.Model,
			Serial:      d.Serial,
			Pool:        d.Pool,
			SizeDisplay: formatBytes(d.SizeBytes),
			TempDisplay: tempDisplay(d.SMART),
			HoursLabel:  hoursDisplay(d.SMART),
			UsageLabel:  diskUsageLabel(tr, d.Usage),
			UsageClass:  diskUsageClass(d.Usage),
			SMARTBadge:  smartLabel(tr, d.SMART),
			SMARTClass:  smartClass(d.SMART.Status),
		})
	}
	return out
}

func importsToView(tr translator, ps []storage.ImportablePool) []ImportableView {
	out := make([]ImportableView, 0, len(ps))
	for _, p := range ps {
		out = append(out, ImportableView{
			Name:        p.Name,
			GUID:        p.GUID,
			StateBadge:  healthLabel(tr, p.State),
			StateClass:  healthClass(p.State),
			Description: p.StatusMsg,
			Topology:    topologySummary(p.Topology),
		})
	}
	return out
}

// topologySummary collapses a pool's top-level vdev layouts into a single
// human-readable string like "raidz2 + special mirror" — the prototype's
// PoolCard sub-title text. Cache / log / spare are appended in their own
// notation.
func topologySummary(vs []storage.Vdev) string {
	if len(vs) == 0 {
		return ""
	}
	var parts []string
	for _, v := range vs {
		switch v.Type {
		case storage.VdevTypeData:
			if v.Layout != "" && v.Layout != storage.LayoutUnknown {
				parts = append(parts, fmt.Sprintf("%s × %d", v.Layout, len(v.Children)))
			} else if v.Path != "" {
				parts = append(parts, "single disk")
			}
		case storage.VdevTypeSpecial:
			parts = append(parts, "special "+string(v.Layout))
		case storage.VdevTypeCache:
			parts = append(parts, "cache")
		case storage.VdevTypeLog:
			parts = append(parts, "log")
		case storage.VdevTypeSpare:
			parts = append(parts, "spare")
		}
	}
	return strings.Join(parts, " · ")
}

func healthLabel(tr translator, h storage.Health) string {
	switch h {
	case storage.HealthOnline:
		return tr.T(i18n.MsgStoragePoolHealthOnline)
	case storage.HealthDegraded:
		return tr.T(i18n.MsgStoragePoolHealthDegraded)
	case storage.HealthFaulted:
		return tr.T(i18n.MsgStoragePoolHealthFaulted)
	case storage.HealthOffline:
		return tr.T(i18n.MsgStoragePoolHealthOffline)
	case storage.HealthUnavail:
		return tr.T(i18n.MsgStoragePoolHealthUnavail)
	case storage.HealthRemoved:
		return tr.T(i18n.MsgStoragePoolHealthRemoved)
	case storage.HealthSuspended:
		return tr.T(i18n.MsgStoragePoolHealthSuspended)
	}
	return tr.T(i18n.MsgStoragePoolHealthUnknown)
}

func healthClass(h storage.Health) string {
	switch h {
	case storage.HealthOnline:
		return "ok"
	case storage.HealthDegraded:
		return "warn"
	case storage.HealthFaulted, storage.HealthUnavail, storage.HealthRemoved, storage.HealthSuspended:
		return "crit"
	case storage.HealthOffline:
		return "warn"
	}
	return ""
}

func vdevTypeLabel(tr translator, t storage.VdevType) string {
	switch t {
	case storage.VdevTypeData:
		return tr.T(i18n.MsgStorageVdevDataRow)
	case storage.VdevTypeSpecial:
		return tr.T(i18n.MsgStorageVdevSpecial)
	case storage.VdevTypeCache:
		return tr.T(i18n.MsgStorageVdevCache)
	case storage.VdevTypeLog:
		return tr.T(i18n.MsgStorageVdevLog)
	case storage.VdevTypeSpare:
		return tr.T(i18n.MsgStorageVdevSpare)
	}
	return string(t)
}

func diskUsageLabel(tr translator, u storage.DiskUsage) string {
	switch u {
	case storage.DiskUsageFree:
		return tr.T(i18n.MsgStorageDiskUsageFree)
	case storage.DiskUsagePool:
		return tr.T(i18n.MsgStorageDiskUsagePool)
	case storage.DiskUsageSpare:
		return tr.T(i18n.MsgStorageDiskUsageSpare)
	case storage.DiskUsageForeign:
		return tr.T(i18n.MsgStorageDiskUsageForeign)
	case storage.DiskUsageSystem:
		return tr.T(i18n.MsgStorageDiskUsageSystem)
	}
	return ""
}

func diskUsageClass(u storage.DiskUsage) string {
	switch u {
	case storage.DiskUsagePool:
		return "ok"
	case storage.DiskUsageSpare:
		return "ok"
	case storage.DiskUsageFree:
		return ""
	case storage.DiskUsageForeign:
		return "warn"
	case storage.DiskUsageSystem:
		return ""
	}
	return ""
}

func smartLabel(tr translator, s storage.SMARTInfo) string {
	switch s.Status {
	case storage.SMARTStatusPassed:
		return tr.T(i18n.MsgStorageSMARTPassed)
	case storage.SMARTStatusWarning:
		return tr.T(i18n.MsgStorageSMARTWarning)
	case storage.SMARTStatusFailed:
		return tr.T(i18n.MsgStorageSMARTFailed)
	}
	if s.Source == "unsupported" {
		return tr.T(i18n.MsgStorageSMARTUnsupported)
	}
	return tr.T(i18n.MsgStorageSMARTUnavailable)
}

func smartClass(s storage.SMARTStatus) string {
	switch s {
	case storage.SMARTStatusPassed:
		return "ok"
	case storage.SMARTStatusWarning:
		return "warn"
	case storage.SMARTStatusFailed:
		return "crit"
	}
	return ""
}

func tempDisplay(s storage.SMARTInfo) string {
	if s.Source == "" || s.Source == "unavailable" || s.Source == "unsupported" {
		return "—"
	}
	if s.TemperatureC == 0 {
		return "—"
	}
	return fmt.Sprintf("%d °C", s.TemperatureC)
}

func hoursDisplay(s storage.SMARTInfo) string {
	if s.Source == "" || s.Source == "unavailable" || s.Source == "unsupported" || s.PowerOnHours == 0 {
		return "—"
	}
	return fmt.Sprintf("%d h", s.PowerOnHours)
}

func barClass(capPct int) string {
	switch {
	case capPct >= 90:
		return "crit"
	case capPct >= 80:
		return "warn"
	default:
		return "ok"
	}
}

// formatBytes prints SI units with one decimal up to TB. Above PB we'd need
// to revisit, but the dev-box / target hardware caps below petabyte.
func formatBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const (
		KB = 1000
		MB = 1000 * KB
		GB = 1000 * MB
		TB = 1000 * GB
	)
	switch {
	case n >= TB:
		return fmt.Sprintf("%.2f TB", float64(n)/float64(TB))
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(KB))
	}
	return fmt.Sprintf("%d B", n)
}
