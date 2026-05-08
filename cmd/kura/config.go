package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kuraos-org/kura/internal/config"
)

// configCmd dispatches `kura config <subcommand>`.
//
// Two subcommands ship in S464e47:
//
//   - export: print the current config.json to stdout. With no engines
//     wired yet, the output is just the empty envelope, which is exactly
//     the round-trip invariant we want enforced from day one.
//   - apply <file> [--dry-run]: read a config.json file and either show
//     the diff (--dry-run) or call into the (currently empty) ApplyAdapter
//     registry. Without --dry-run + no adapters, apply is a no-op that
//     prints "no plan entries".
func configCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("kura config: subcommand required (export | apply)")
	}
	switch args[0] {
	case "export":
		return configExport(args[1:], os.Stdout)
	case "apply":
		return configApply(args[1:], os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("kura config: unknown subcommand %q", args[0])
	}
}

func configExport(args []string, out io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("kura config export: unexpected arguments %v", args)
	}
	// No engines registered yet — produce the empty envelope. When engines
	// arrive, each will contribute its current state via a hook this CLI
	// will gain. For now the empty Config satisfies AC-S464e47-2-1.
	cfg := config.New()
	raw, err := config.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("kura config export: %w", err)
	}
	if _, err := out.Write(raw); err != nil {
		return fmt.Errorf("kura config export: write: %w", err)
	}
	return nil
}

func configApply(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("config apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "print plan without applying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("kura config apply: expected exactly one file argument, got %v", rest)
	}
	path := rest[0]

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("kura config apply: open %q: %w", path, err)
	}
	defer f.Close()

	newCfg, err := config.ReadFrom(f)
	if err != nil {
		return fmt.Errorf("kura config apply: %w", err)
	}

	// Old state is the export of the running system. With no engines wired
	// yet the old state is empty, so any non-empty section in newCfg shows
	// up as "add" in the plan — which is the correct preview.
	oldCfg := config.New()

	plan, err := config.Diff(oldCfg, newCfg)
	if err != nil {
		return fmt.Errorf("kura config apply: diff: %w", err)
	}

	if _, err := io.WriteString(stdout, config.FormatPlan(plan)); err != nil {
		return fmt.Errorf("kura config apply: write plan: %w", err)
	}

	if *dryRun {
		return nil
	}

	// Apply: dispatch each plan entry to the matching ApplyAdapter. With no
	// engines registered this is a noop, but the structure is in place so
	// engine sprints can hook in by calling config.Register(...).
	ctx := context.Background()
	for _, e := range plan.Entries {
		if e.Op == "noop" {
			continue
		}
		var found bool
		for _, a := range config.Adapters() {
			if a.Section() != e.Section {
				continue
			}
			steps, planErr := a.Plan(ctx, oldCfg, newCfg)
			if planErr != nil {
				return fmt.Errorf("kura config apply: plan %s: %w", e.Section, planErr)
			}
			if applyErr := a.Apply(ctx, steps); applyErr != nil {
				return fmt.Errorf("kura config apply: apply %s: %w", e.Section, applyErr)
			}
			found = true
			break
		}
		if !found {
			fmt.Fprintf(stderr, "skipped: no adapter registered for section %q\n", e.Section)
		}
	}
	return nil
}
