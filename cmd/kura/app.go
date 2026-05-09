package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/kuraos-org/kura/engine/app"
)

// appCmd dispatches `kura app <subcommand>`. Subcommands cover the
// pre-install primitives this Sprint introduces: lint a manifest, fetch a
// signed manifest from a registry (smoke test for AC-S1bccf5-2-1), plan
// dataset paths (smoke test for AC-S1bccf5-3-1), reserve a host port
// (smoke test for AC-S1bccf5-3-2). install/update/uninstall arrive in
// S65b510.
func appCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("kura app: subcommand required (lint | fetch | dataset-plan | reserve-port)")
	}
	switch args[0] {
	case "lint":
		return appLint(args[1:], os.Stdout)
	case "fetch":
		return appFetch(args[1:], os.Stdout)
	case "dataset-plan":
		return appDatasetPlan(args[1:], os.Stdout)
	case "reserve-port":
		return appReservePort(args[1:], os.Stdout)
	default:
		return fmt.Errorf("kura app: unknown subcommand %q", args[0])
	}
}

func appLint(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app lint", flag.ContinueOnError)
	path := fs.String("file", "", "path to manifest.yaml (- for stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return fmt.Errorf("kura app lint: --file required")
	}
	body, err := readFileOrStdin(*path)
	if err != nil {
		return err
	}
	m, err := app.ParseManifest(body)
	if err != nil {
		return err
	}
	if err := app.Validate(m); err != nil {
		return err
	}
	fmt.Fprintf(out, "manifest %s@%s ok (%d containers, %d datasets)\n",
		m.Name, m.Version, len(m.Containers), len(m.Storage.Datasets))
	return nil
}

// appFetch implements `kura app fetch`. It downloads + verifies a manifest
// against the registry trust policy and prints the rendered manifest as
// JSON for inspection. Used in VM smoke tests (success + failure paths).
func appFetch(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app fetch", flag.ContinueOnError)
	url := fs.String("url", "", "base URL of the registry (e.g. https://registry.kuraos.org)")
	identity := fs.String("identity", "", "identity_regex for cosign keyless verify")
	issuer := fs.String("issuer", "https://token.actions.githubusercontent.com", "OIDC issuer")
	appName := fs.String("app", "", "app name")
	version := fs.String("version", "", "app version")
	allowFakeOK := fs.String("fake-allow-payload", "", "[testing] file path; when set, a FakeVerifier accepts the listed payloads; when empty, the production verifier is used (which fails closed in v1)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" || *appName == "" || *version == "" || *identity == "" {
		return fmt.Errorf("kura app fetch: --url, --identity, --app, --version are required")
	}
	src := app.RegistrySource{
		Name:          "cli",
		URL:           *url,
		IdentityRegex: *identity,
		Issuer:        *issuer,
	}

	verifier := pickCLIVerifier(*allowFakeOK, *identity, *issuer)
	client := app.NewHTTPRegistryClient(verifier, &http.Client{Timeout: 30 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m, _, err := client.FetchManifest(ctx, src, *appName, *version)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// pickCLIVerifier returns a CosignVerifier suited for the invocation. When
// --fake-allow-payload <path> is supplied (VM smoke testing) the fake
// approves only the contents of <path> with the configured identity. Any
// other invocation uses the production verifier — which currently
// fail-closes (S65b510 wires the real sigstore-go path).
func pickCLIVerifier(allowFile, identity, issuer string) app.CosignVerifier {
	if allowFile == "" {
		return app.NewSigstoreVerifier()
	}
	body, err := os.ReadFile(allowFile)
	if err != nil {
		return app.NewSigstoreVerifier() // fall through to fail-closed
	}
	v := app.NewFakeVerifier()
	v.Allow(body, identity, issuer)
	return v
}

// appDatasetPlan: parse the manifest and print the planner output. The
// pool list comes from --pools=<name:role,...> so we don't need a live
// ZFS environment for the smoke test.
func appDatasetPlan(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app dataset-plan", flag.ContinueOnError)
	manifestPath := fs.String("file", "", "path to manifest.yaml")
	appID := fs.String("app-id", "", "app instance id")
	poolsCSV := fs.String("pools", "tank:tank", "comma-separated pool list as name:role (role: ssd|tank)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *appID == "" {
		return fmt.Errorf("kura app dataset-plan: --file and --app-id are required")
	}
	body, err := readFileOrStdin(*manifestPath)
	if err != nil {
		return err
	}
	m, err := app.ParseManifest(body)
	if err != nil {
		return err
	}
	if err := app.Validate(m); err != nil {
		return err
	}
	pools, err := parsePoolsArg(*poolsCSV)
	if err != nil {
		return err
	}

	ctx := context.Background()
	sys, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer sys.Close()

	planner := app.NewDatasetPlanner(sys.store.DB(), &app.StaticPoolLister{Pools: pools})
	plans, err := planner.Plan(ctx, *appID, m)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(plans)
}

// parsePoolsArg parses "fast:ssd,tank:tank" into []app.PoolInfo. Errors
// on bad role names so a typo fails loudly.
func parsePoolsArg(s string) ([]app.PoolInfo, error) {
	if s == "" {
		return nil, nil
	}
	out := []app.PoolInfo{}
	for _, entry := range splitCSV(s) {
		name, role, ok := splitColon(entry)
		if !ok {
			return nil, fmt.Errorf("kura: pool entry %q must be name:role", entry)
		}
		switch app.PoolHint(role) {
		case app.PoolHintSSD, app.PoolHintTank:
		default:
			return nil, fmt.Errorf("kura: pool %q: unknown role %q (want ssd|tank)", name, role)
		}
		out = append(out, app.PoolInfo{Name: name, Role: app.PoolHint(role)})
	}
	return out, nil
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func splitColon(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// appReservePort exercises the port allocator twice and prints both
// host_port values. Same tuple → same port (AC-S1bccf5-3-2). Used by VM
// smoke tests; install flow uses Reserve directly.
func appReservePort(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app reserve-port", flag.ContinueOnError)
	appID := fs.String("app-id", "", "app instance id")
	container := fs.String("container", "", "container name from manifest")
	manifestPort := fs.Int("manifest-port", 0, "in-container port the manifest declared")
	rangeMin := fs.Int("range-min", 0, "optional: override default port range min")
	rangeMax := fs.Int("range-max", 0, "optional: override default port range max")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *appID == "" || *container == "" || *manifestPort == 0 {
		return fmt.Errorf("kura app reserve-port: --app-id, --container, --manifest-port required")
	}

	ctx := context.Background()
	sys, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer sys.Close()

	alloc := app.NewPortAllocator(sys.store.DB())
	if *rangeMin > 0 && *rangeMax > 0 {
		alloc.Range = app.PortRange{Min: *rangeMin, Max: *rangeMax}
	}
	host, err := alloc.Reserve(ctx, app.PortReservation{
		AppID: *appID, Container: *container, ManifestPort: *manifestPort,
	})
	if err != nil {
		return err
	}
	// Call Reserve again to demonstrate the determinism property.
	host2, err := alloc.Reserve(ctx, app.PortReservation{
		AppID: *appID, Container: *container, ManifestPort: *manifestPort,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "first=%d second=%d stable=%t\n", host, host2, host == host2)
	return nil
}

func readFileOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// strconv shim so this file does not need to import strconv directly when
// only used in error messages — keeps the import block tidy.
var _ = strconv.Itoa
