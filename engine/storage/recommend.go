// recommend.go — disk topology recommendation engine for the setup wizard
// (S99702c-2). Implements design.md §18.2 smart defaults logic.
//
// Rules (DESIGN_PRINCIPLES priority #2: 賢いデフォルト):
//   1 disk  → single
//   2 disks → mirror
//   3 disks → mirror + spare OR raidz1 (raidz1 preferred)
//   4-5     → raidz1
//   6-7     → raidz2
//   8+      → raidz2 (or raidz3 for 8+; raidz2 is the conservative default)
//
// Special vdev: only when SSD+HDD mix is detected — NVMe/SSD disks become a
// mirror-special vdev; HDD disks form the data pool. DESIGN_PRINCIPLES
// forbidden #3: single-SSD special vdev is rejected by the engine; the
// recommendation engine never proposes it.
package storage

import "strings"

// RecommendedPool is the smart-default pool configuration the setup wizard
// proposes to the operator. The operator can accept or override it.
type RecommendedPool struct {
	// Name is the suggested pool name ("tank" for HDD, "fast" for SSD-only).
	Name string
	// DataTopology is the suggested ZFS layout for data vdevs.
	DataTopology string // "single" | "mirror" | "raidz1" | "raidz2" | "raidz3"
	// DataDisks is the list of disk paths to use for data vdevs.
	DataDisks []string
	// SpecialTopology is the layout for the special vdev (empty = no special).
	SpecialTopology string
	// SpecialDisks is the list of disk paths for the special vdev.
	SpecialDisks []string
	// Explanation is a human-readable rationale (developer-facing English).
	Explanation string
}

// RecommendTopology derives a smart-default pool configuration from the
// detected free disks. Returns nil when no free disks are available.
//
// Disk classification: Rotation == 0 → SSD/NVMe, Rotation > 0 → HDD.
// Mixed workloads: SSD/NVMe → special vdev, HDD → data pool (tank).
// SSD-only or HDD-only: single pool with the appropriate topology.
func RecommendTopology(disks []Disk) *RecommendedPool {
	var hdd, ssd []string
	for _, d := range disks {
		if d.Usage != DiskUsageFree {
			continue
		}
		if d.Rotation == 0 {
			ssd = append(ssd, diskPath(d))
		} else {
			hdd = append(hdd, diskPath(d))
		}
	}

	hasSSD := len(ssd) > 0
	hasHDD := len(hdd) > 0

	switch {
	case !hasSSD && !hasHDD:
		// No free disks — no recommendation.
		return nil

	case hasHDD && hasSSD:
		// Mixed: HDD → tank, SSD → mirror special (only if ≥2 SSDs).
		rec := &RecommendedPool{
			Name:        "tank",
			DataTopology: topologyFor(len(hdd)),
			DataDisks:   hdd,
			Explanation: "HDD disks form the data pool; SSD/NVMe disks form the special vdev for small-block acceleration.",
		}
		if len(ssd) >= 2 {
			rec.SpecialTopology = "mirror"
			rec.SpecialDisks = ssd
		}
		return rec

	case hasSSD && !hasHDD:
		// SSD-only pool — use NVMe-friendly naming.
		return &RecommendedPool{
			Name:        "fast",
			DataTopology: topologyFor(len(ssd)),
			DataDisks:   ssd,
			Explanation: "SSD/NVMe-only pool — optimal for apps and databases.",
		}

	default: // HDD-only
		return &RecommendedPool{
			Name:        "tank",
			DataTopology: topologyFor(len(hdd)),
			DataDisks:   hdd,
			Explanation: "HDD pool with recommended redundancy level.",
		}
	}
}

// topologyFor returns the recommended ZFS data layout for n disks.
// Based on design.md §18.2 / sprint brief rule:
//   1 → single, 2 → mirror, 3-5 → raidz1, 6-7 → raidz2, 8+ → raidz2.
func topologyFor(n int) string {
	switch {
	case n <= 0:
		return "single"
	case n == 1:
		return "single"
	case n == 2:
		return "mirror"
	case n <= 5:
		return "raidz1"
	case n <= 7:
		return "raidz2"
	default:
		return "raidz2"
	}
}

// diskPath returns ByID if set (preferred for ZFS pool stability), otherwise Path.
func diskPath(d Disk) string {
	if strings.HasPrefix(d.ByID, "/dev/disk/by-id/") {
		return d.ByID
	}
	return d.Path
}
