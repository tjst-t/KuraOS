package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kuraos-org/kura/engine/storage"
	"github.com/kuraos-org/kura/internal/cmdexec"
)

// storageCmd dispatches `kura storage <subcommand>`. The CLI surface is the
// only path that exposes --force-no-redundancy (single-SSD special vdev
// override). Per DESIGN_PRINCIPLES forbidden #14, the UI never reaches that
// flag — it lives here so a knowledgeable operator can perform a one-off
// override under their own responsibility.
func storageCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("kura storage: subcommand required (create-pool|create-volume|list-volumes|create-snapshot|list-snapshots|rollback|set-quota)")
	}
	switch args[0] {
	case "create-pool":
		return storageCreatePool(args[1:], os.Stdout, os.Stderr)
	case "create-volume":
		return storageCreateVolume(args[1:], os.Stdout, os.Stderr)
	case "list-volumes":
		return storageListVolumes(args[1:], os.Stdout, os.Stderr)
	case "create-snapshot":
		return storageCreateSnapshot(args[1:], os.Stdout, os.Stderr)
	case "list-snapshots":
		return storageListSnapshots(args[1:], os.Stdout, os.Stderr)
	case "rollback":
		return storageRollback(args[1:], os.Stdout, os.Stderr)
	case "set-quota":
		return storageSetQuota(args[1:], os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("kura storage: unknown subcommand %q", args[0])
	}
}

func storageCreatePool(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("storage create-pool", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "pool name (required)")
	vdev := fs.String("vdev", "", "data vdev: 'mirror:/dev/sda,/dev/sdb' or 'raidz2:/dev/sda,/dev/sdb,/dev/sdc,/dev/sdd' or 'single:/dev/sda'")
	special := fs.String("special", "", "optional special vdev (same syntax as --vdev)")
	spares := fs.String("spares", "", "comma-separated list of spare disks")
	smallBlock := fs.Int64("small-block", 0, "special_small_blocks threshold in bytes (0 = metadata only)")
	force := fs.Bool("force-no-redundancy", false, "allow non-redundant special vdev (escape hatch — DESIGN_PRINCIPLES forbidden #3 normally rejects this)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *vdev == "" {
		return fmt.Errorf("--name and --vdev are required")
	}
	dataSpec, err := parseVdevFlag(*vdev)
	if err != nil {
		return fmt.Errorf("--vdev: %w", err)
	}
	cfg := storage.PoolConfig{
		Name:                *name,
		Data:                dataSpec,
		SmallBlockThreshold: *smallBlock,
		ForceNoRedundancy:   *force,
	}
	if *special != "" {
		s, err := parseVdevFlag(*special)
		if err != nil {
			return fmt.Errorf("--special: %w", err)
		}
		cfg.Special = &s
	}
	if *spares != "" {
		for _, s := range strings.Split(*spares, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				cfg.Spares = append(cfg.Spares, s)
			}
		}
	}

	eng := storage.NewCLI(cmdexec.NewReal())
	if err := eng.CreatePool(context.Background(), cfg); err != nil {
		return fmt.Errorf("create pool: %w", err)
	}
	fmt.Fprintf(stdout, "pool %q created\n", *name)
	return nil
}

// storageCreateVolume runs `kura storage create-volume <pool>/<dataset> [flags]`.
//
//	kura storage create-volume tank/photos --preset media
//	kura storage create-volume tank/db --preset database --quota 5368709120
//	kura storage create-volume tank/raw --recordsize 128K --compression zstd
//
// Either --preset or the raw flags may be set; preset wins when both are
// passed and a raw value would otherwise be filled. Engine-level
// applyPresetToOpts logic stays in engine/storage/presets.go.
func storageCreateVolume(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("storage create-volume", flag.ContinueOnError)
	fs.SetOutput(stderr)
	preset := fs.String("preset", "", "preset id (general|media|database). Sets recordsize/compression/special_small_blocks bundle.")
	quota := fs.Int64("quota", 0, "dataset quota in bytes (0 = no quota)")
	mountpoint := fs.String("mountpoint", "", "override default mountpoint")
	recordsize := fs.String("recordsize", "", "raw recordsize (ignored when --preset is set)")
	compression := fs.String("compression", "", "raw compression (ignored when --preset is set)")
	logbias := fs.String("logbias", "", "raw logbias (ignored when --preset is set)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("kura storage create-volume: dataset positional arg required (e.g. tank/photos)")
	}
	dataset := strings.TrimSpace(fs.Arg(0))
	if dataset == "" {
		return fmt.Errorf("dataset name is empty")
	}
	opts := storage.VolumeOpts{
		Preset:      storage.PresetID(*preset),
		QuotaBytes:  *quota,
		MountPoint:  *mountpoint,
		RecordSize:  *recordsize,
		Compression: *compression,
		LogBias:     *logbias,
	}
	eng := storage.NewCLI(cmdexec.NewReal())
	if err := eng.CreateVolume(context.Background(), dataset, opts); err != nil {
		return fmt.Errorf("create volume: %w", err)
	}
	fmt.Fprintf(stdout, "volume %q created\n", dataset)
	return nil
}

// storageListVolumes runs `kura storage list-volumes [pool]`. Output is a
// table by default; --json emits machine-readable JSON.
func storageListVolumes(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("storage list-volumes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool := ""
	if fs.NArg() >= 1 {
		pool = strings.TrimSpace(fs.Arg(0))
	}
	eng := storage.NewCLI(cmdexec.NewReal())
	vols, err := eng.ListVolumes(context.Background(), pool)
	if err != nil {
		return fmt.Errorf("list volumes: %w", err)
	}
	if *asJSON {
		return json.NewEncoder(stdout).Encode(vols)
	}
	fmt.Fprintln(stdout, "NAME\tUSED\tAVAIL\tREFER\tQUOTA\tRECORDSIZE\tCOMPRESSION\tMOUNT")
	for _, v := range vols {
		quota := "—"
		if v.QuotaBytes > 0 {
			quota = fmt.Sprintf("%d", v.QuotaBytes)
		}
		fmt.Fprintf(stdout, "%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\n",
			v.Name, v.UsedBytes, v.AvailableBytes, v.ReferencedBytes,
			quota, v.RecordSize, v.Compression, v.MountPoint)
	}
	return nil
}

// storageCreateSnapshot runs `kura storage create-snapshot <dataset> <name>`.
func storageCreateSnapshot(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("storage create-snapshot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("kura storage create-snapshot: usage: <dataset> <snapshot-name>")
	}
	dataset := strings.TrimSpace(fs.Arg(0))
	name := strings.TrimSpace(fs.Arg(1))
	eng := storage.NewCLI(cmdexec.NewReal())
	if err := eng.CreateSnapshot(context.Background(), dataset, name); err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}
	fmt.Fprintf(stdout, "snapshot %s@%s created\n", dataset, name)
	return nil
}

// storageListSnapshots runs `kura storage list-snapshots <dataset>`.
func storageListSnapshots(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("storage list-snapshots", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("kura storage list-snapshots: usage: <dataset>")
	}
	eng := storage.NewCLI(cmdexec.NewReal())
	snaps, err := eng.ListSnapshots(context.Background(), strings.TrimSpace(fs.Arg(0)))
	if err != nil {
		return fmt.Errorf("list snapshots: %w", err)
	}
	if *asJSON {
		return json.NewEncoder(stdout).Encode(snaps)
	}
	fmt.Fprintln(stdout, "DATASET\tNAME\tUSED\tREFER\tCREATED")
	for _, s := range snaps {
		created := "—"
		if !s.Created.IsZero() {
			created = s.Created.Format("2006-01-02T15:04:05Z")
		}
		fmt.Fprintf(stdout, "%s\t%s\t%d\t%d\t%s\n",
			s.Dataset, s.Name, s.UsedBytes, s.ReferBytes, created)
	}
	return nil
}

// storageRollback runs `kura storage rollback <dataset> <snapshot-name>`.
// Destructive — does not pass -r so a later snapshot blocks rollback. Force
// recursive cleanup is intentionally not exposed (priority #5: 信頼性 > 機能).
func storageRollback(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("storage rollback", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("kura storage rollback: usage: <dataset> <snapshot-name>")
	}
	dataset := strings.TrimSpace(fs.Arg(0))
	snap := strings.TrimSpace(fs.Arg(1))
	eng := storage.NewCLI(cmdexec.NewReal())
	if err := eng.Rollback(context.Background(), dataset, snap); err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	fmt.Fprintf(stdout, "rolled back %s to @%s\n", dataset, snap)
	return nil
}

// storageSetQuota runs `kura storage set-quota <dataset> <bytes>`.
// Pass 0 to clear the quota.
func storageSetQuota(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("storage set-quota", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("kura storage set-quota: usage: <dataset> <bytes> (0 = unset)")
	}
	dataset := strings.TrimSpace(fs.Arg(0))
	var bytes int64
	if _, err := fmt.Sscanf(fs.Arg(1), "%d", &bytes); err != nil {
		return fmt.Errorf("invalid quota %q: must be an integer (bytes)", fs.Arg(1))
	}
	eng := storage.NewCLI(cmdexec.NewReal())
	if err := eng.SetQuota(context.Background(), dataset, bytes); err != nil {
		return fmt.Errorf("set quota: %w", err)
	}
	if bytes == 0 {
		fmt.Fprintf(stdout, "quota unset on %s\n", dataset)
	} else {
		fmt.Fprintf(stdout, "quota=%d set on %s\n", bytes, dataset)
	}
	return nil
}

// parseVdevFlag parses "<layout>:<disk1>,<disk2>,..." into a storage.VdevSpec.
// Single-disk pools may also use "single:<disk>" or just "<disk>" but the
// explicit form is always accepted.
func parseVdevFlag(s string) (storage.VdevSpec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return storage.VdevSpec{}, fmt.Errorf("empty")
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return storage.VdevSpec{}, fmt.Errorf("expected <layout>:<disks>, got %q", s)
	}
	layout := storage.VdevLayout(strings.TrimSpace(parts[0]))
	var disks []string
	for _, d := range strings.Split(parts[1], ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			disks = append(disks, d)
		}
	}
	return storage.VdevSpec{Layout: layout, Disks: disks}, nil
}
