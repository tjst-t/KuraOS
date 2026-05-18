package monitor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// sysProbe reads metrics from /proc and zpool commands. All methods are
// best-effort: on a dev machine without ZFS the pool section is simply empty.
type sysProbe struct {
	lastCPUStat cpuStat
}

// NewSysProbe constructs the production MetricsCollector.
func NewSysProbe() MetricsCollector {
	return &sysProbe{}
}

type cpuStat struct {
	idle, total uint64
}

func (p *sysProbe) CollectSnapshot(ctx context.Context) (Snapshot, error) {
	snap := Snapshot{
		PoolUsedPct: make(map[string]float64),
		PoolUsedGB:  make(map[string]float64),
	}

	// CPU — two successive reads with a tiny gap; on a fast machine both
	// reads happen near-instantaneously so we delta against the previous call.
	cur, err := readCPUStat()
	if err == nil && p.lastCPUStat.total > 0 {
		deltIdle := float64(cur.idle - p.lastCPUStat.idle)
		deltTotal := float64(cur.total - p.lastCPUStat.total)
		if deltTotal > 0 {
			snap.CPUPercent = (1.0 - deltIdle/deltTotal) * 100.0
		}
	}
	p.lastCPUStat = cur

	// Memory from /proc/meminfo.
	if total, used, err := readMemInfo(); err == nil {
		snap.MemTotalMB = float64(total) / 1024.0
		snap.MemUsedMB = float64(used) / 1024.0
	}

	// Pool usage via `zpool list -H -p -o name,alloc,size`.
	if pools, err := readPoolStats(ctx); err == nil {
		for _, ps := range pools {
			snap.PoolUsedGB[ps.name] = float64(ps.usedBytes) / 1e9
			if ps.totalBytes > 0 {
				snap.PoolUsedPct[ps.name] = float64(ps.usedBytes) / float64(ps.totalBytes) * 100.0
			}
		}
	}

	// Disk temperature via smartctl or hwmon — best effort.
	snap.DiskTempCelsius = readDiskTemp(ctx)

	return snap, nil
}

func readCPUStat() (cpuStat, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuStat{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			break
		}
		// user nice system idle iowait irq softirq steal
		vals := make([]uint64, len(fields)-1)
		for i, fv := range fields[1:] {
			vals[i], _ = strconv.ParseUint(fv, 10, 64)
		}
		idle := vals[3] + vals[4]
		var total uint64
		for _, v := range vals {
			total += v
		}
		return cpuStat{idle: idle, total: total}, nil
	}
	return cpuStat{}, fmt.Errorf("monitor: cpu stat not found in /proc/stat")
}

func readMemInfo() (totalKB, usedKB uint64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var total, available uint64
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			available = v
		}
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("monitor: MemTotal not found")
	}
	return total, total - available, nil
}

type poolStat struct {
	name       string
	usedBytes  uint64
	totalBytes uint64
}

func readPoolStats(ctx context.Context) ([]poolStat, error) {
	// zpool list -H -p -o name,alloc,size
	// -H: no header, -p: parseable (bytes), -o: columns
	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctxTimeout, "zpool", "list", "-H", "-p", "-o", "name,alloc,size").Output()
	if err != nil {
		return nil, fmt.Errorf("monitor: zpool list: %w", err)
	}
	var pools []poolStat
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		used, _ := strconv.ParseUint(fields[1], 10, 64)
		total, _ := strconv.ParseUint(fields[2], 10, 64)
		pools = append(pools, poolStat{name: fields[0], usedBytes: used, totalBytes: total})
	}
	return pools, nil
}

func readDiskTemp(ctx context.Context) float64 {
	// Try hwmon first (fast, no root needed on most distros).
	if temp := readHwmonTemp(); temp > 0 {
		return temp
	}
	// Fall back to smartctl -A on /dev/sda.
	ctxTimeout, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctxTimeout, "smartctl", "-A", "/dev/sda").Output()
	if err != nil {
		return 0
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Temperature_Celsius") {
			fields := strings.Fields(line)
			if len(fields) >= 10 {
				if v, err := strconv.ParseFloat(fields[9], 64); err == nil {
					return v
				}
			}
		}
	}
	return 0
}

func readHwmonTemp() float64 {
	dirs, _ := os.ReadDir("/sys/class/hwmon")
	for _, d := range dirs {
		path := "/sys/class/hwmon/" + d.Name() + "/temp1_input"
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
		if err == nil && v > 0 {
			return v / 1000.0 // millicelsius → celsius
		}
	}
	return 0
}
