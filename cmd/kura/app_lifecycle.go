package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuraos-org/kura/engine/app"
)

// appInstall is `kura app install`. Operator-facing primitive that mirrors
// what the UI install button does: validate, fetch, dataset-plan + create,
// reserve ports, ensure secrets, render configs, pull, start, healthcheck,
// register route, persist record. Install runs synchronously; progress events
// are streamed to the terminal so the operator can see exactly which stage
// failed when something goes wrong (DESIGN_PRINCIPLES priority #5: error
// messages must be specific).
func appInstall(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app install", flag.ContinueOnError)
	registry := fs.String("registry", "", "registry name from config.json apps.registries[]")
	appName := fs.String("app", "", "app name from registry")
	version := fs.String("version", "", "version (default: latest from registry)")
	setupCSV := fs.String("setup", "", "comma-separated key=value pairs for setup.required fields")
	settingCSV := fs.String("setting", "", "comma-separated key=value pairs for non-secret settings")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *registry == "" || *appName == "" {
		return fmt.Errorf("kura app install: --registry and --app required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	lc, sys, err := openLifecycle(ctx)
	if err != nil {
		return err
	}
	defer sys.Close()
	src, err := lifecycleSourceByName(ctx, sys.store.DB(), *registry)
	if err != nil {
		return err
	}
	if *version == "" {
		reg, err := lc.Registry.FetchRegistry(ctx, src)
		if err != nil {
			return err
		}
		ra, ok := reg.Apps[*appName]
		if !ok {
			return fmt.Errorf("kura app install: app %q not in registry", *appName)
		}
		*version = ra.Latest
	}
	req := app.InstallRequest{
		Source:      src,
		AppName:     *appName,
		Version:     *version,
		SetupValues: parseKVCSV(*setupCSV),
		Settings:    parseKVCSV(*settingCSV),
		AppID:       app.NewAppIDForName(*appName),
	}

	// Subscribe and tail to stdout. Subscribe BEFORE Install starts so the
	// initial 'start' event isn't missed.
	ch, unsub := lc.Subscribe(req.AppID)
	defer unsub()
	done := make(chan error, 1)
	go func() {
		_, err := lc.Install(ctx, req)
		done <- err
	}()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				continue
			}
			mark := "✓"
			if !ev.OK {
				mark = "✗"
			}
			fmt.Fprintf(out, "[%s] %-22s %s\n", mark, ev.Stage, ev.Detail)
			if ev.Final {
				err := <-done
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "installed app_id=%s\n", req.AppID)
				return nil
			}
		case err := <-done:
			if err != nil {
				return err
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// appUpdate is `kura app update`.
func appUpdate(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app update", flag.ContinueOnError)
	appID := fs.String("app-id", "", "installed app id (from `kura app list`)")
	registry := fs.String("registry", "", "registry name (defaults to record.registry)")
	version := fs.String("version", "", "new version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *appID == "" || *version == "" {
		return fmt.Errorf("kura app update: --app-id and --version required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	lc, sys, err := openLifecycle(ctx)
	if err != nil {
		return err
	}
	defer sys.Close()
	rec, err := lc.LookupApp(ctx, *appID)
	if err != nil {
		return err
	}
	if rec.AppID == "" {
		return fmt.Errorf("kura app update: app %s not installed", *appID)
	}
	regName := *registry
	if regName == "" {
		regName = rec.Registry
	}
	src, err := lifecycleSourceByName(ctx, sys.store.DB(), regName)
	if err != nil {
		return err
	}
	ch, unsub := lc.Subscribe(*appID)
	defer unsub()
	done := make(chan error, 1)
	go func() {
		done <- lc.Update(ctx, app.UpdateRequest{AppID: *appID, Source: src, NewVersion: *version})
	}()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				continue
			}
			mark := "✓"
			if !ev.OK {
				mark = "✗"
			}
			fmt.Fprintf(out, "[%s] %-22s %s\n", mark, ev.Stage, ev.Detail)
			if ev.Final {
				return <-done
			}
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// appUninstall is `kura app uninstall`.
func appUninstall(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app uninstall", flag.ContinueOnError)
	appID := fs.String("app-id", "", "installed app id")
	deleteData := fs.Bool("delete-data", false, "destroy ZFS datasets too (irreversible)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *appID == "" {
		return fmt.Errorf("kura app uninstall: --app-id required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	lc, sys, err := openLifecycle(ctx)
	if err != nil {
		return err
	}
	defer sys.Close()
	if err := lc.Uninstall(ctx, app.UninstallRequest{AppID: *appID, DeleteData: *deleteData}); err != nil {
		return err
	}
	fmt.Fprintf(out, "uninstalled %s\n", *appID)
	return nil
}

// appList is `kura app list`.
func appList(args []string, out io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("kura app list: unexpected arguments")
	}
	ctx := context.Background()
	lc, sys, err := openLifecycle(ctx)
	if err != nil {
		return err
	}
	defer sys.Close()
	recs, err := lc.ListApps(ctx)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n",
			rec.AppID, rec.Name, rec.Version, rec.Registry, rec.State)
	}
	return nil
}

// appRegistry is `kura app registry add|list|remove`.
func appRegistry(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("kura app registry: subcommand required (add | list | remove)")
	}
	switch args[0] {
	case "add":
		return appRegistryAdd(args[1:], out)
	case "list":
		return appRegistryList(args[1:], out)
	case "remove":
		return appRegistryRemove(args[1:], out)
	default:
		return fmt.Errorf("kura app registry: unknown subcommand %q", args[0])
	}
}

func appRegistryAdd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app registry add", flag.ContinueOnError)
	name := fs.String("name", "", "registry name")
	url := fs.String("url", "", "base URL (file:// or https://)")
	identity := fs.String("identity", "", "identity_regex for cosign / local verifier")
	issuer := fs.String("issuer", "kuraos-local", "expected OIDC issuer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *url == "" || *identity == "" {
		return fmt.Errorf("kura app registry add: --name, --url, --identity required")
	}
	ctx := context.Background()
	sys, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer sys.Close()
	cur := loadAppSources(ctx, sys.store.DB())
	for _, c := range cur {
		if c.Name == *name {
			return fmt.Errorf("kura app registry: %q already registered", *name)
		}
	}
	cfgs := convertToConfigs(cur)
	cfgs = append(cfgs, app.AppRegistryConfig{
		Name: *name,
		URL:  *url,
		Trust: app.AppRegistryTrustConfig{
			Type:          "cosign_keyless",
			IdentityRegex: *identity,
			Issuer:        *issuer,
		},
	})
	if err := saveAppRegistries(ctx, sys.store.DB(), cfgs); err != nil {
		return err
	}
	fmt.Fprintf(out, "registry %s added\n", *name)
	return nil
}

func appRegistryList(_ []string, out io.Writer) error {
	ctx := context.Background()
	sys, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer sys.Close()
	cur := loadAppSources(ctx, sys.store.DB())
	for _, c := range cur {
		fmt.Fprintf(out, "%s\t%s\t%s\n", c.Name, c.URL, c.IdentityRegex)
	}
	return nil
}

func appRegistryRemove(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app registry remove", flag.ContinueOnError)
	name := fs.String("name", "", "registry name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("kura app registry remove: --name required")
	}
	ctx := context.Background()
	sys, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer sys.Close()
	cur := loadAppSources(ctx, sys.store.DB())
	out2 := []app.RegistrySource{}
	for _, c := range cur {
		if c.Name != *name {
			out2 = append(out2, c)
		}
	}
	if err := saveAppRegistries(ctx, sys.store.DB(), convertToConfigs(out2)); err != nil {
		return err
	}
	fmt.Fprintf(out, "registry %s removed\n", *name)
	return nil
}

// appKeygen is `kura app keygen` — generates an ed25519 keypair for signing
// a local registry. The private key is printed to stdout (operator stores it
// safely); the public key is written to /var/lib/kura/app-keys/<keyID>.pub.
func appKeygen(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app keygen", flag.ContinueOnError)
	dir := fs.String("dir", "/var/lib/kura/app-keys", "trusted keys directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, priv, keyID, err := app.GenerateLocalKeypair()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o750); err != nil {
		return err
	}
	pubPath := filepath.Join(*dir, keyID+".pub")
	if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "key_id=%s\n", keyID)
	fmt.Fprintf(out, "pub_path=%s\n", pubPath)
	fmt.Fprintf(out, "priv_b64=%s\n", base64.StdEncoding.EncodeToString(priv))
	fmt.Fprintf(out, "# Save the private key safely; it is required to sign manifests.\n")
	return nil
}

// appSign is `kura app sign --priv <b64> --identity <s> --issuer <s> <file>`.
func appSign(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kura app sign", flag.ContinueOnError)
	privB64 := fs.String("priv", "", "base64-encoded ed25519 private key")
	identity := fs.String("identity", "", "identity to record in the bundle")
	issuer := fs.String("issuer", "kuraos-local", "issuer to record")
	keyID := fs.String("key-id", "", "key id (matches <keyID>.pub on the verifier)")
	file := fs.String("file", "", "payload file to sign")
	sigOut := fs.String("out", "", "output bundle path (default <file>.sig)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *privB64 == "" || *identity == "" || *keyID == "" || *file == "" {
		return fmt.Errorf("kura app sign: --priv, --identity, --key-id, --file required")
	}
	priv, err := base64.StdEncoding.DecodeString(*privB64)
	if err != nil {
		return fmt.Errorf("decode priv: %w", err)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("priv must be %d bytes (got %d)", ed25519.PrivateKeySize, len(priv))
	}
	payload, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	bundle, err := app.SignBlobLocal(payload, ed25519.PrivateKey(priv), *keyID, *identity, *issuer)
	if err != nil {
		return err
	}
	dst := *sigOut
	if dst == "" {
		dst = *file + ".sig"
	}
	if err := os.WriteFile(dst, bundle, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\n", dst)
	return nil
}

// openLifecycle returns a wired AppLifecycle suitable for CLI use. Uses the
// same env vars as the daemon so `kura app install` and the daemon agree on
// state DB / docker socket / etc. Tests override KURA_DOCKER_FAKE=1 to skip
// the daemon dependency.
func openLifecycle(ctx context.Context) (*app.AppLifecycle, *systemHandle, error) {
	sys, err := openSystemEngine(ctx)
	if err != nil {
		return nil, nil, err
	}
	verifier := app.NewLocalVerifier()
	verifier.SetKeysDir(envOr("KURA_APP_KEYS_DIR", "/var/lib/kura/app-keys"))
	if err := verifier.LoadFromDir(); err != nil {
		// Non-fatal: when no keys exist, every fetch will fail with "unknown
		// key_id" — the operator gets a specific error rather than a silent
		// success.
		fmt.Fprintln(os.Stderr, "kura app: warning loading verifier keys:", err)
	}
	registryClient := app.NewHTTPRegistryClient(verifier, &http.Client{Timeout: 30 * time.Second})
	planner := app.NewDatasetPlanner(sys.store.DB(), &app.StaticPoolLister{Pools: nil})
	planner.FallbackPool = "tank"
	ports := app.NewPortAllocator(sys.store.DB())
	secrets := app.NewSecretStore(sys.eng)
	docker := buildDockerClient(ctx)
	// CLI uses a no-op storage adapter when no engine/storage CLI is
	// available — the install still records DatasetPlan rows in SQLite so the
	// daemon can reconcile later. VM tests inject a real storage adapter via
	// the daemon process.
	storage := app.NewFakeStorageWriter(envOr("KURA_APP_FAKE_STORAGE_ROOT", "/tmp/kura-cli-data"))
	routes := app.NewMemoryRouteRegistry()
	lc := app.NewLifecycle(registryClient, planner, ports, secrets, docker, storage, routes, sys.store.DB())
	if root := os.Getenv("KURA_APPS_CONFIG_ROOT"); root != "" {
		lc.ConfigRoot = root
	}
	return lc, sys, nil
}

// lifecycleSourceByName returns the trusted RegistrySource matching name from
// the kura_kv-persisted operator list. Returns an error if absent so the
// install fails fast with a clear message.
func lifecycleSourceByName(ctx context.Context, db *sql.DB, name string) (app.RegistrySource, error) {
	for _, src := range loadAppSources(ctx, db) {
		if src.Name == name {
			return src, nil
		}
	}
	return app.RegistrySource{}, fmt.Errorf("registry %q not in apps.registries (run `kura app registry add`)", name)
}

// parseKVCSV parses "k1=v1,k2=v2" → map. Tolerates empty input.
func parseKVCSV(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	for _, part := range strings.Split(s, ",") {
		eq := strings.Index(part, "=")
		if eq <= 0 {
			continue
		}
		out[part[:eq]] = part[eq+1:]
	}
	return out
}

// envOr returns env value or fallback.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// convertToConfigs reverses the loadAppSources mapping (RegistrySource ->
// AppRegistryConfig) for persistence.
func convertToConfigs(src []app.RegistrySource) []app.AppRegistryConfig {
	out := make([]app.AppRegistryConfig, 0, len(src))
	for _, s := range src {
		out = append(out, app.AppRegistryConfig{
			Name: s.Name, URL: s.URL,
			Trust: app.AppRegistryTrustConfig{
				Type:          "cosign_keyless",
				IdentityRegex: s.IdentityRegex,
				Issuer:        s.Issuer,
			},
		})
	}
	return out
}

// saveAppRegistries persists the trusted registries into kura_kv. config.json
// apply also writes through here so the SSOT round-trip stays clean.
func saveAppRegistries(ctx context.Context, db *sql.DB, cfgs []app.AppRegistryConfig) error {
	body, err := json.Marshal(cfgs)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO kura_kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		"apps.registries", string(body))
	return err
}
