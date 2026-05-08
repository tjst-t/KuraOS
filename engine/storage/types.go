// Package storage abstracts ZFS pool / dataset / disk inspection behind an
// interface so tests can swap a fake. The production implementation runs the
// zpool / zfs / lsblk / smartctl CLIs through internal/cmdexec.
//
// VISION tech_constraints_phase_1: "ZFS CLI ラッパー経由 (libzfs_core CGo は
// 使わない、ビルド依存最小化)" — every call goes through Executor.Run, never
// libzfs.
//
// The struct definitions in this file are the schema engines / handlers /
// templates / config.json must round-trip through. Adding a field here is a
// schema change; engines reading it should do so through these names rather
// than re-parsing CLI output.
package storage

import "time"

// Health is the coarse pool / vdev state. Mirrors the strings ZFS prints
// (ONLINE / DEGRADED / FAULTED / OFFLINE / UNAVAIL / REMOVED) so debug logs
// align with what an operator sees on the CLI. Templates render it via
// i18n.T to keep raw CLI tokens off the screen (DESIGN_PRINCIPLES forbidden:
// "Docker / ZFS / SMB の CLI 出力を直接画面に表示しない").
type Health string

const (
	HealthUnknown   Health = ""
	HealthOnline    Health = "ONLINE"
	HealthDegraded  Health = "DEGRADED"
	HealthFaulted   Health = "FAULTED"
	HealthOffline   Health = "OFFLINE"
	HealthUnavail   Health = "UNAVAIL"
	HealthRemoved   Health = "REMOVED"
	HealthSuspended Health = "SUSPENDED"
)

// VdevType enumerates the topology roles a vdev may play inside a pool. Only
// the v1 set is listed — dedup / SLOG / L2ARC are explicitly out_of_scope_permanent
// in VISION so they intentionally have no constants here. Anything the parser
// can't classify falls back to VdevTypeUnknown so reads don't drop data.
type VdevType string

const (
	VdevTypeUnknown VdevType = ""
	// VdevTypeData is a top-level data vdev (raidz / mirror / single-disk
	// stripe). It has children that are leaf disks.
	VdevTypeData VdevType = "data"
	// VdevTypeSpecial is the optional small-block / metadata accelerator
	// vdev. v1 UI lets operators see one if it exists; creation is gated
	// behind redundancy validation in the next sprint.
	VdevTypeSpecial VdevType = "special"
	// VdevTypeCache / VdevTypeLog / VdevTypeSpare exist as data classes the
	// parser must recognize so we don't lose them when a CLI-managed pool is
	// imported. v1 UI does not expose creation; the prototype design only
	// shows them in read views.
	VdevTypeCache VdevType = "cache"
	VdevTypeLog   VdevType = "log"
	VdevTypeSpare VdevType = "spare"
)

// VdevLayout is the immediate child topology of a data vdev. Single = stripe
// of one disk (no redundancy), Mirror = N-way mirror, RaidZ{1,2,3} = raidz of
// the corresponding parity count. Unknown is reserved for future layouts so
// the parser never crashes on a string it doesn't recognize.
type VdevLayout string

const (
	LayoutUnknown VdevLayout = ""
	LayoutSingle  VdevLayout = "single"
	LayoutMirror  VdevLayout = "mirror"
	LayoutRaidZ1  VdevLayout = "raidz1"
	LayoutRaidZ2  VdevLayout = "raidz2"
	LayoutRaidZ3  VdevLayout = "raidz3"
)

// Vdev is a node in the pool's topology tree. Top-level Vdevs hold leaves
// (Disks); nested Vdevs cover the mirror-of-raidz / raidz-of-mirror cases the
// parser preserves verbatim. Path is the device path for a leaf, "" for a
// container.
type Vdev struct {
	Type     VdevType   `json:"type"`
	Layout   VdevLayout `json:"layout,omitempty"`
	Name     string     `json:"name"`
	Path     string     `json:"path,omitempty"`
	Health   Health     `json:"health"`
	Read     int64      `json:"read_errors"`
	Write    int64      `json:"write_errors"`
	Cksum    int64      `json:"checksum_errors"`
	Children []Vdev     `json:"children,omitempty"`
}

// Pool aggregates everything the Storage page needs to render a PoolCard +
// device tree. Sizes are in bytes; the template formats to TB. Frag is a
// percentage 0–100.
type Pool struct {
	Name           string    `json:"name"`
	GUID           string    `json:"guid"`
	Health         Health    `json:"health"`
	SizeBytes      int64     `json:"size_bytes"`
	AllocatedBytes int64     `json:"allocated_bytes"`
	FreeBytes      int64     `json:"free_bytes"`
	FragPercent    int       `json:"frag_percent"`
	CapPercent     int       `json:"cap_percent"`
	Dedup          float64   `json:"dedup"`
	Topology       []Vdev    `json:"topology"`
	LastScrub      time.Time `json:"last_scrub,omitempty"`
}

// DiskUsage classifies how a physical disk relates to ZFS at the moment.
// "pool" = active member, "spare" = hot spare, "free" = unused (eligible for
// new pool / spare), "foreign" = looks like a ZFS member of a non-imported
// pool, "system" = boot/OS disk we shouldn't touch.
type DiskUsage string

const (
	DiskUsageUnknown DiskUsage = ""
	DiskUsageFree    DiskUsage = "free"
	DiskUsagePool    DiskUsage = "pool"
	DiskUsageSpare   DiskUsage = "spare"
	DiskUsageForeign DiskUsage = "foreign"
	DiskUsageSystem  DiskUsage = "system"
)

// SMARTStatus is the SMART overall-health flag normalized across vendors.
// "passed" = OK, "warning" = pre-fail attribute crossed, "failed" = drive
// reports failed, "unknown" = SMART not available (USB enclosure, smartctl
// missing, etc.). Templates render via i18n.T.
type SMARTStatus string

const (
	SMARTStatusUnknown SMARTStatus = ""
	SMARTStatusPassed  SMARTStatus = "passed"
	SMARTStatusWarning SMARTStatus = "warning"
	SMARTStatusFailed  SMARTStatus = "failed"
)

// SMARTInfo is the subset of `smartctl -a -j` we surface in the UI. Anything
// not parseable falls back to zero / Unknown so the page still renders.
type SMARTInfo struct {
	Status         SMARTStatus `json:"status"`
	TemperatureC   int         `json:"temperature_c"`
	PowerOnHours   int         `json:"power_on_hours"`
	ReallocatedSec int         `json:"reallocated_sectors"`
	PendingSec     int         `json:"pending_sectors"`
	UDMACRC        int         `json:"udma_crc_errors"`
	// Source records which tool produced the data. "smartctl" for normal
	// drives, "unsupported" when smartctl reported the device cannot do
	// SMART (USB / NVMe-via-enclosure), "unavailable" when smartctl is not
	// installed at all. Lets the UI show a different empty state.
	Source string `json:"source,omitempty"`
}

// Disk is one physical block device plus the SMART layer. Path is the canonical
// /dev/<name>; ByID is the /dev/disk/by-id link if present (preferred for ZFS
// pool creation in the next sprint).
type Disk struct {
	Path      string    `json:"path"`
	ByID      string    `json:"by_id,omitempty"`
	Model     string    `json:"model,omitempty"`
	Serial    string    `json:"serial,omitempty"`
	SizeBytes int64     `json:"size_bytes"`
	Rotation  int       `json:"rotation_rpm,omitempty"`
	Pool      string    `json:"pool,omitempty"`
	Usage     DiskUsage `json:"usage"`
	SMART     SMARTInfo `json:"smart"`
}

// ImportablePool is the shape returned by `zpool import` (no args = scan).
// Matches what the Storage page's "import banner" needs to render.
type ImportablePool struct {
	Name      string `json:"name"`
	GUID      string `json:"guid"`
	State     Health `json:"state"`
	StatusMsg string `json:"status_msg,omitempty"`
	Action    string `json:"action,omitempty"`
	Topology  []Vdev `json:"topology"`
}
