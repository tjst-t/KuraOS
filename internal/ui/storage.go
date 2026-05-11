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
	// Writer is the optional write surface (CreatePool / CreateVolume /
	// CreateSnapshot / ImportPool). When nil, mutation routes return 503.
	// Tests that only need read coverage leave it unset; the production
	// wiring in cmd/kura passes a *storage.CLI which implements both.
	Writer StorageEngineWriter
	// InstalledAppNames returns the set of manifest.Names currently in
	// app_installs. Used by the volume list to grey out the delete
	// button on a dataset whose owning app is still installed (data
	// loss guard). Nil = treat every app dataset as orphan.
	InstalledAppNames func(ctx context.Context) map[string]bool
}

// StorageEngineReader is the read-only subset of storage.Engine the UI uses.
// Defined here so the gateway tests can stub it locally without depending
// on the engine package.
type StorageEngineReader interface {
	ListPools(ctx context.Context) ([]storage.Pool, error)
	ListDisks(ctx context.Context) ([]storage.Disk, error)
	ListImportable(ctx context.Context) ([]storage.ImportablePool, error)
	ListVolumes(ctx context.Context, pool string) ([]storage.VolumeInfo, error)
	ListSnapshots(ctx context.Context, dataset string) ([]storage.SnapshotInfo, error)
}

// StorageEngineWriter is the mutation surface the New Pool / Snapshot /
// Import flows need. It deliberately omits destroy / wipe — those are
// scope-out for S9db742 (DESIGN_PRINCIPLES priority #5: 信頼性 > 機能).
type StorageEngineWriter interface {
	CreatePool(ctx context.Context, cfg storage.PoolConfig) error
	CreateVolume(ctx context.Context, dataset string, opts storage.VolumeOpts) error
	CreateSnapshot(ctx context.Context, dataset, name string) error
	Rollback(ctx context.Context, dataset, snapshot string) error
	SetQuota(ctx context.Context, dataset string, bytes int64) error
	ImportPool(ctx context.Context, name string, opts storage.ImportOpts) error
	DestroyPool(ctx context.Context, name string, force bool) error
	DestroyVolume(ctx context.Context, dataset string, recursive bool) error
	DestroySnapshot(ctx context.Context, dataset, name string) error
}

// StorageView is the template view-model. Pre-formatted strings live here so
// templates stay free of business logic; raw structs travel via Pools/Disks
// for unit tests that want to assert the underlying values.
type StorageView struct {
	Subtitle     string
	HasPools     bool
	HasDisks     bool
	HasImports   bool
	HasVolumes   bool
	HasSnapshots bool
	ImportError  string
	WriteEnabled bool

	// ActiveTab drives which panel renders below the pool cards. Values:
	// "volumes" (default) | "disks" | "snapshots" | "imports". Selected by
	// the ?tab= query param so each tab has a real URL — operators can
	// bookmark "tank's snapshots", and back/forward navigation works.
	ActiveTab string
	Tabs      []TabOption

	// ShowAllDatasets reflects the ?showAll=1 query param. When false
	// (default) the volume list hides every IsAppDataset row so the
	// operator sees only datasets they created. When true the list
	// includes app datasets with badges + the relevant disabled-delete
	// reason. The checkbox above the table flips this.
	ShowAllDatasets bool

	Pools     []PoolView
	Disks     []DiskView
	Imports   []ImportableView
	Volumes   []VolumeView
	Snapshots []SnapshotView
	Importer  ImportBanner

	// Layouts / Presets are surfaced to the New Pool dialog and Volume form.
	Layouts []LayoutOption
	Presets []PresetOption

	// FreeDisks is the subset of Disks eligible to be added to a new pool
	// (Usage = free). The New Pool dialog renders these as a checklist so
	// the operator never has to hand-type /dev paths.
	FreeDisks []DiskView
}

// VolumeView is one row in the Volume tab. Numeric byte values are
// pre-formatted to "12.4 TB"-style strings; raw bytes stay on UsedBytes /
// QuotaBytes so tests / form pre-fill have a stable shape.
type VolumeView struct {
	Name        string
	UsedBytes   int64
	QuotaBytes  int64
	UsedDisplay string
	AvailDisplay string
	QuotaDisplay string
	MountPoint  string
	RecordSize  string
	Compression string
	IsPoolRoot  bool
	// IsAppDataset is true when the path matches the app-namespace
	// convention <pool>/apps/<appname>(/<sub>...). The default volume
	// list hides these so operators see only user-created datasets; a
	// "show all datasets" checkbox surfaces them.
	IsAppDataset bool
	// AppName is the manifest.Name extracted from the path (empty when
	// the row is the <pool>/apps parent itself).
	AppName string
	// DeleteDisabled greys out the destroy-volume button. True when:
	//  - the row is a pool root (covered by IsPoolRoot already)
	//  - the row is the <pool>/apps parent
	//  - the row belongs to an app currently installed (data-loss guard)
	// False (delete enabled) on app dataset orphans = the install was
	// uninstalled with deleteData=false and the operator now wants to
	// reclaim the space.
	DeleteDisabled bool
	DeleteReason   string
}

// SnapshotView is one row in the Snapshot tab.
type SnapshotView struct {
	Dataset      string
	Name         string
	UsedBytes    int64
	UsedDisplay  string
	ReferDisplay string
	CreatedLabel string
}

// LayoutOption is one entry in the layout selector. ID is the engine-side
// VdevLayout string (mirror / raidz1 / ...); Label is the translated copy.
type LayoutOption struct {
	ID    string
	Label string
}

// TabOption is one entry in the Storage page's tab strip.
type TabOption struct {
	ID     string
	Label  string
	Active bool
	Href   string
}

// PresetOption is one entry in the preset selector for the volume form.
type PresetOption struct {
	ID    string
	Label string
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
		tab := normalizeStorageTab(req.URL.Query().Get("tab"))
		showAll := req.URL.Query().Get("showAll") == "1"
		view := r.buildStorageView(req.Context(), d)
		view.ActiveTab = tab
		view.Tabs = storageTabs(r.tr, tab)
		view.ShowAllDatasets = showAll
		// When the operator hasn't asked for "all", strip app-managed
		// rows so the table shows only datasets they created. Done
		// here (not in buildStorageView) so unit tests can exercise
		// classification independently of the query param.
		if !showAll {
			kept := view.Volumes[:0]
			for _, v := range view.Volumes {
				if v.IsAppDataset {
					continue
				}
				kept = append(kept, v)
			}
			view.Volumes = kept
			view.HasVolumes = len(view.Volumes) > 0
		}
		data := r.buildPageData("storage", i18n.MsgStorageTitle)
		data.Extra = view
		r.render(w, "templates/pages/storage.tmpl", data)
	})
}

func normalizeStorageTab(s string) string {
	switch s {
	case "volumes", "disks", "snapshots", "imports":
		return s
	}
	return "volumes"
}

func storageTabs(tr translator, active string) []TabOption {
	ids := []string{"volumes", "disks", "snapshots", "imports"}
	labels := map[string]i18n.MessageID{
		"volumes":   i18n.MsgStorageTabVolumes,
		"disks":     i18n.MsgStorageTabDisks,
		"snapshots": i18n.MsgStorageTabSnapshots,
		"imports":   i18n.MsgStorageTabImports,
	}
	out := make([]TabOption, 0, len(ids))
	for _, id := range ids {
		out = append(out, TabOption{
			ID:     id,
			Label:  tr.T(labels[id]),
			Active: id == active,
			Href:   "/ui/admin/storage?tab=" + id,
		})
	}
	return out
}

// buildStorageView is broken out from the handler so unit tests can
// exercise the formatter (i18n + bytes-to-TB) without spinning up an
// http server.
func (r *Renderer) buildStorageView(ctx context.Context, d StorageDeps) StorageView {
	pools, perr := d.Engine.ListPools(ctx)
	disks, _ := d.Engine.ListDisks(ctx)
	imports, _ := d.Engine.ListImportable(ctx)
	volumes, _ := d.Engine.ListVolumes(ctx, "")

	// Aggregate snapshots across every dataset that came back from
	// ListVolumes. ListSnapshots is recursive per pool but takes a single
	// argument; calling it once per dataset is acceptable at v1 scale
	// (small dataset counts) and lets the Snapshot tab show snapshots
	// from all volumes at once.
	var snapshots []storage.SnapshotInfo
	seenSnap := make(map[string]bool)
	for _, v := range volumes {
		got, err := d.Engine.ListSnapshots(ctx, v.Name)
		if err != nil {
			continue
		}
		for _, s := range got {
			key := s.Dataset + "@" + s.Name
			if seenSnap[key] {
				continue
			}
			seenSnap[key] = true
			snapshots = append(snapshots, s)
		}
	}

	disksView := disksToView(r.tr, disks)
	freeDisks := make([]DiskView, 0)
	for i, dv := range disksView {
		if i >= len(disks) {
			continue
		}
		// "Foreign" disks are physically unused — they only carry stale ZFS
		// labels from a destroyed/exported pool that lsblk still reads as
		// fstype=zfs_member. Hide them from the picker would strand the
		// hardware until labels are wiped manually; CreatePool runs
		// `zpool labelclear -f` on every selected leaf before `zpool create`,
		// so picking a foreign disk is safe.
		switch disks[i].Usage {
		case storage.DiskUsageFree, storage.DiskUsageForeign:
			freeDisks = append(freeDisks, dv)
		}
	}

	view := StorageView{
		Pools:        poolsToView(r.tr, pools),
		Disks:        disksView,
		Imports:      importsToView(r.tr, imports),
		Volumes:      volumesToView(volumes, pools, installedAppsFromDeps(ctx, d)),
		Snapshots:    snapshotsToView(snapshots),
		HasPools:     len(pools) > 0,
		HasDisks:     len(disks) > 0,
		HasImports:   len(imports) > 0,
		HasVolumes:   len(volumes) > 0,
		HasSnapshots: len(snapshots) > 0,
		WriteEnabled: d.Writer != nil,
		Layouts:      layoutOptions(r.tr),
		Presets:      presetOptions(r.tr),
		FreeDisks:    freeDisks,
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

// installedAppsFromDeps invokes the optional callback and returns its
// result, or nil if the dep wasn't wired. nil makes volumesToView treat
// every app dataset as an orphan (= delete enabled), which is the right
// fallback for tests + the first few moments before cmd/kura finishes
// wiring AppLifecycle.
func installedAppsFromDeps(ctx context.Context, d StorageDeps) map[string]bool {
	if d.InstalledAppNames == nil {
		return nil
	}
	return d.InstalledAppNames(ctx)
}

// classifyAppDataset matches <pool>/apps/<appName>[/<sub>...] paths.
// Returns isApp=true with appName="" for the <pool>/apps parent itself,
// isApp=true with the manifest.Name for per-app rows, isApp=false for
// regular user datasets. Pool roots short-circuit (handled separately).
func classifyAppDataset(path string) (isApp bool, appName string) {
	// Strip pool prefix: split into at most 4 parts so deep names like
	// tank/apps/immich/db keep `db` (and anything below) intact.
	parts := strings.SplitN(path, "/", 4)
	if len(parts) < 2 || parts[1] != "apps" {
		return false, ""
	}
	if len(parts) == 2 {
		return true, "" // <pool>/apps parent
	}
	return true, parts[2]
}

// volumesToView converts engine VolumeInfo rows into the template view-model.
// A volume's Name == its pool's Name marks it as the pool root, which the
// template renders specially (the operator can't delete a pool root from the
// Volume tab — that's a Pool-level destructive op, scoped out for v1).
//
// installedApps holds the manifest.Names currently in app_installs; an
// app dataset whose owning app is still installed gets the destroy button
// greyed out (data-loss guard). Pass nil to treat every app dataset as
// orphan-with-delete-enabled (tests, early dev).
func volumesToView(vols []storage.VolumeInfo, pools []storage.Pool, installedApps map[string]bool) []VolumeView {
	poolNames := make(map[string]bool, len(pools))
	for _, p := range pools {
		poolNames[p.Name] = true
	}
	out := make([]VolumeView, 0, len(vols))
	for _, v := range vols {
		quotaDisplay := "—"
		if v.QuotaBytes > 0 {
			quotaDisplay = formatBytes(v.QuotaBytes)
		}
		isApp, appName := classifyAppDataset(v.Name)
		view := VolumeView{
			Name:         v.Name,
			UsedBytes:    v.UsedBytes,
			QuotaBytes:   v.QuotaBytes,
			UsedDisplay:  formatBytes(v.UsedBytes),
			AvailDisplay: formatBytes(v.AvailableBytes),
			QuotaDisplay: quotaDisplay,
			MountPoint:   v.MountPoint,
			RecordSize:   v.RecordSize,
			Compression:  v.Compression,
			IsPoolRoot:   poolNames[v.Name],
			IsAppDataset: isApp,
			AppName:      appName,
		}
		// Decide whether destroy is enabled. Pool root already
		// suppresses the form in the template; here we only annotate
		// app-namespace rows.
		switch {
		case isApp && appName == "":
			// <pool>/apps parent — never deletable from the UI.
			view.DeleteDisabled = true
			view.DeleteReason = "ds.delete.reason.apps_parent"
		case isApp && installedApps[appName]:
			// App still installed → deletion would destroy live data.
			view.DeleteDisabled = true
			view.DeleteReason = "ds.delete.reason.app_installed"
		case isApp:
			// App dataset orphan — uninstall left data behind. The
			// operator may legitimately reclaim the space here.
			view.DeleteDisabled = false
		}
		out = append(out, view)
	}
	return out
}

// snapshotsToView converts engine SnapshotInfo rows into the template view-
// model. The created label is RFC-3339 short form so the mono column lines
// up; ja.json doesn't currently localize date formats — v1.x backlog.
func snapshotsToView(snaps []storage.SnapshotInfo) []SnapshotView {
	out := make([]SnapshotView, 0, len(snaps))
	for _, s := range snaps {
		created := "—"
		if !s.Created.IsZero() {
			created = s.Created.Format("2006-01-02 15:04")
		}
		out = append(out, SnapshotView{
			Dataset:      s.Dataset,
			Name:         s.Name,
			UsedBytes:    s.UsedBytes,
			UsedDisplay:  formatBytes(s.UsedBytes),
			ReferDisplay: formatBytes(s.ReferBytes),
			CreatedLabel: created,
		})
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

// layoutOptions builds the vdev-layout dropdown source. We surface the v1
// layouts (single / mirror / raidz1-3); LayoutUnknown stays out so the form
// can never submit a layout the validator will reject as unknown.
func layoutOptions(tr translator) []LayoutOption {
	return []LayoutOption{
		{ID: string(storage.LayoutSingle), Label: tr.T(i18n.MsgStorageLayoutSingle)},
		{ID: string(storage.LayoutMirror), Label: tr.T(i18n.MsgStorageLayoutMirror)},
		{ID: string(storage.LayoutRaidZ1), Label: tr.T(i18n.MsgStorageLayoutRaidZ1)},
		{ID: string(storage.LayoutRaidZ2), Label: tr.T(i18n.MsgStorageLayoutRaidZ2)},
		{ID: string(storage.LayoutRaidZ3), Label: tr.T(i18n.MsgStorageLayoutRaidZ3)},
	}
}

func presetOptions(tr translator) []PresetOption {
	out := make([]PresetOption, 0, 3)
	for _, id := range storage.AllPresets() {
		var label string
		switch id {
		case storage.PresetGeneral:
			label = tr.T(i18n.MsgStoragePresetGeneral)
		case storage.PresetMedia:
			label = tr.T(i18n.MsgStoragePresetMedia)
		case storage.PresetDatabase:
			label = tr.T(i18n.MsgStoragePresetDatabase)
		}
		out = append(out, PresetOption{ID: string(id), Label: label})
	}
	return out
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
