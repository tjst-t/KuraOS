package main

import (
	"context"
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
		return fmt.Errorf("kura storage: subcommand required (create-pool)")
	}
	switch args[0] {
	case "create-pool":
		return storageCreatePool(args[1:], os.Stdout, os.Stderr)
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
