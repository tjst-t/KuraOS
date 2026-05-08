package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// lsblkRoot mirrors the JSON shape produced by `lsblk -J -O -b`. Only the
// fields we consume are decoded; lsblk emits dozens we don't need.
type lsblkRoot struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

type lsblkDevice struct {
	Name       string        `json:"name"`
	Path       string        `json:"path"`
	Type       string        `json:"type"`
	Size       int64         `json:"size"`
	Rota       bool          `json:"rota"`
	Model      string        `json:"model"`
	Serial     string        `json:"serial"`
	Tran       string        `json:"tran"`
	Mountpoint string        `json:"mountpoint"`
	Fstype     string        `json:"fstype"`
	Children   []lsblkDevice `json:"children"`
}

// ListDisks enumerates every disk-type block device, classifies its usage
// against current pool membership, and (best-effort) attaches SMART data.
func (c *CLI) ListDisks(ctx context.Context) ([]Disk, error) {
	stdout, _, err := c.exec.Run(ctx, "lsblk", "-J", "-O", "-b")
	if err != nil {
		return nil, fmt.Errorf("storage: lsblk: %w", err)
	}
	disks, err := parseLsblk(stdout)
	if err != nil {
		return nil, fmt.Errorf("storage: parse lsblk: %w", err)
	}

	// Pool membership classification: ask zpool which disks belong to which
	// imported pool. Failure here is non-fatal — we still return disks with
	// usage="unknown". The dev box has no ZFS so this branch is exercised in
	// tests via the fake exec returning canned output.
	pools, perr := c.ListPools(ctx)
	if perr == nil {
		assignPoolMembership(disks, pools)
	}

	// SMART per disk — best effort, errors leave SMARTInfo zero with Source
	// set so the UI shows "—" / "Unknown".
	for i := range disks {
		disks[i].SMART = c.smartFor(ctx, disks[i].Path)
	}
	return disks, nil
}

func parseLsblk(raw []byte) ([]Disk, error) {
	var root lsblkRoot
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	var out []Disk
	for _, d := range root.BlockDevices {
		if d.Type != "disk" {
			continue
		}
		usage := DiskUsageFree
		if hasMountedChild(d) {
			usage = DiskUsageSystem
		} else if hasZFSChild(d) {
			// Could be a member of an imported pool or a foreign exported
			// pool; classify here as "pool" and let pool-membership
			// resolution downgrade to "foreign" for unmatched ones.
			usage = DiskUsagePool
		}
		out = append(out, Disk{
			Path:      strings.TrimSpace(devPath(d)),
			Model:     strings.TrimSpace(d.Model),
			Serial:    strings.TrimSpace(d.Serial),
			SizeBytes: d.Size,
			Rotation:  rotationRPM(d.Rota),
			Usage:     usage,
		})
	}
	return out, nil
}

func devPath(d lsblkDevice) string {
	if d.Path != "" {
		return d.Path
	}
	return "/dev/" + d.Name
}

func hasMountedChild(d lsblkDevice) bool {
	for _, c := range d.Children {
		if c.Mountpoint != "" {
			return true
		}
	}
	return d.Mountpoint != ""
}

func hasZFSChild(d lsblkDevice) bool {
	for _, c := range d.Children {
		if strings.EqualFold(strings.TrimSpace(c.Fstype), "zfs_member") {
			return true
		}
	}
	return false
}

func rotationRPM(rota bool) int {
	if rota {
		return 7200 // unknown; placeholder until smartctl reports it
	}
	return 0
}

func assignPoolMembership(disks []Disk, pools []Pool) {
	// Build a set of disk paths referenced anywhere in any pool topology.
	// /dev/disk/by-id/sda → "sda"; /dev/sda → "sda". We strip the prefix so
	// either form lines up against lsblk's /dev/<name>.
	leafPool := map[string]string{}
	var walk func(string, []Vdev)
	walk = func(poolName string, vs []Vdev) {
		for _, v := range vs {
			if v.Path != "" {
				leafPool[shortDevName(v.Path)] = poolName
			}
			if len(v.Children) > 0 {
				walk(poolName, v.Children)
			}
		}
	}
	for _, p := range pools {
		walk(p.Name, p.Topology)
	}
	for i := range disks {
		short := shortDevName(disks[i].Path)
		if pname, ok := leafPool[short]; ok {
			disks[i].Pool = pname
			disks[i].Usage = DiskUsagePool
			continue
		}
		// Disk advertises ZFS partitions but no live pool claims it →
		// foreign / exported pool member.
		if disks[i].Usage == DiskUsagePool {
			disks[i].Usage = DiskUsageForeign
		}
	}
}

func shortDevName(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// smartctlReport is the subset of `smartctl -a -j` we read.
type smartctlReport struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
	} `json:"smartctl"`
	SMARTStatus struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOnTime struct {
		Hours int `json:"hours"`
	} `json:"power_on_time"`
	AtaSmartAttributes struct {
		Table []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Raw  struct {
				Value int `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
}

// smartFor runs `smartctl -a -j <path>` and parses the JSON. Missing tool /
// unsupported device returns a zero SMARTInfo with Source set so the UI can
// distinguish "not available" from "passed but missing thresholds".
func (c *CLI) smartFor(ctx context.Context, path string) SMARTInfo {
	stdout, _, err := c.exec.Run(ctx, "smartctl", "-a", "-j", path)
	if err != nil {
		// smartctl exit codes 1-7 indicate informational issues with non-empty
		// JSON output. errors.As surfaces ExitError so we can still parse.
		var ee *exec.ExitError
		if !errors.As(err, &ee) || len(stdout) == 0 {
			return SMARTInfo{Source: "unavailable"}
		}
	}
	return parseSmartctl(stdout)
}

func parseSmartctl(raw []byte) SMARTInfo {
	if len(raw) == 0 {
		return SMARTInfo{Source: "unavailable"}
	}
	var rep smartctlReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return SMARTInfo{Source: "unsupported"}
	}
	info := SMARTInfo{
		TemperatureC: rep.Temperature.Current,
		PowerOnHours: rep.PowerOnTime.Hours,
		Source:       "smartctl",
	}
	if rep.SMARTStatus.Passed {
		info.Status = SMARTStatusPassed
	} else {
		info.Status = SMARTStatusFailed
	}
	for _, a := range rep.AtaSmartAttributes.Table {
		switch a.ID {
		case 5:
			info.ReallocatedSec = a.Raw.Value
		case 197:
			info.PendingSec = a.Raw.Value
		case 199:
			info.UDMACRC = a.Raw.Value
		}
	}
	// Heuristic warning: status passed but bad-block counters non-zero.
	if info.Status == SMARTStatusPassed && (info.ReallocatedSec > 0 || info.PendingSec > 0) {
		info.Status = SMARTStatusWarning
	}
	return info
}
