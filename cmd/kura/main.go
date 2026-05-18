package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kuraos-org/kura/engine/app"
	"github.com/kuraos-org/kura/engine/auth/oidc"
	"github.com/kuraos-org/kura/engine/auth/session"
	backupEngine "github.com/kuraos-org/kura/engine/backup"
	"github.com/kuraos-org/kura/engine/monitor"
	"github.com/kuraos-org/kura/engine/notify"
	"github.com/kuraos-org/kura/engine/share"
	"github.com/kuraos-org/kura/engine/storage"
	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
	"github.com/kuraos-org/kura/internal/cmdexec"
	"github.com/kuraos-org/kura/internal/gateway"
	"github.com/kuraos-org/kura/internal/store"
	"github.com/kuraos-org/kura/internal/ui"
)

// Version is overridable at link time: -ldflags "-X main.Version=v0.1.0".
var Version = "dev"

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		// developer-facing error message stays English (DESIGN_PRINCIPLES coding_conventions)
		log.Fatalf("kura: %v", err)
	}
}

// dispatch routes the top-level subcommand. With no args, kura falls through
// to the long-running server (`run`). Subcommands like `kura config export`
// short-circuit before binding a port so they're safe to run from cron jobs
// and CI without colliding with a live instance.
func dispatch(args []string) error {
	if len(args) == 0 {
		return run()
	}
	switch args[0] {
	case "config":
		return configCmd(args[1:])
	case "storage":
		return storageCmd(args[1:])
	case "system":
		return systemCmd(args[1:])
	case "user":
		return userCmd(args[1:])
	case "share":
		return shareCmd(args[1:])
	case "app":
		return appCmd(args[1:])
	case "backup":
		return backupCmd(args[1:])
	case "restore":
		return restoreCmd(args[1:])
	case "version", "--version", "-v":
		fmt.Println(Version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: config | storage | system | user | share | app | backup | restore | version)", args[0])
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tr, err := i18n.New()
	if err != nil {
		return fmt.Errorf("init i18n: %w", err)
	}

	port := os.Getenv("KURA_PORT")
	if port == "" {
		// portman wraps `make serve` and is supposed to inject KURA_PORT.
		// Refuse to invent a default — DESIGN_PRINCIPLES forbids hardcoded
		// magic ports and CLAUDE.md explicitly bans port hardcoding.
		return errors.New("env KURA_PORT not set (expected portman to inject it)")
	}

	dbPath := os.Getenv("KURA_STATE_DB")
	if dbPath == "" {
		dbPath = "/var/lib/kura/state.db"
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	uiRenderer, err := ui.New(tr, Version)
	if err != nil {
		return fmt.Errorf("init ui: %w", err)
	}

	// Storage engine wires the production CmdExecutor that shells out to
	// zpool / zfs / lsblk / smartctl. On a dev box without ZFS the engine's
	// methods will return errors which the UI surfaces as a translated
	// banner rather than crashing.
	storageEngine := storage.NewCLI(cmdexec.NewReal())
	// InstalledAppNames is a closure over the shared state DB so the
	// Storage page can grey out destroy-volume on datasets whose owning
	// app is currently installed. Lives here (not buried in ui pkg) so
	// the SQL stays alongside the other state-DB readers in cmd/kura.
	installedApps := func(ctx context.Context) map[string]bool {
		out := map[string]bool{}
		rows, err := st.DB().QueryContext(ctx, `SELECT DISTINCT name FROM app_installs`)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err == nil {
				out[n] = true
			}
		}
		return out
	}
	storageDeps := ui.StorageDeps{
		Engine:            storageEngine,
		Writer:            storageEngine,
		InstalledAppNames: installedApps,
	}
	uiRenderer.SetStorageHandler(uiRenderer.StorageHandler(storageDeps))
	uiRenderer.SetStorageWriteHandler(uiRenderer.StorageWriteHandler(storageDeps))
	// SSOT (DESIGN_PRINCIPLES priority #1): config.json apply must be able
	// to recreate pools/volumes the UI created. The storage adapter is
	// registered against the global config.registry once per process.
	storage.RegisterApplyAdapter(storageEngine)

	// Share engine — SQLite-backed, regenerates /etc/samba/conf.d/kura.conf
	// + /etc/exports.d/kura.exports on every Apply, then reloads via
	// systemctl. CmdExecutor is the same Real seam Storage uses; on a dev
	// box without smbd, Apply will fail at testparm/reload — the UI surfaces
	// a translated message rather than crashing.
	shareStore := share.NewStore(st.DB())
	shareEngine := share.NewManager(shareStore, cmdexec.NewReal(), share.Options{})
	share.RegisterApplyAdapter(shareEngine)

	hasher := user.NewHasher()
	users := user.NewStore(st.DB(), hasher)
	sessions := session.NewStore(st.DB())
	secureCookies := os.Getenv("KURA_SECURE_COOKIES") == "1"

	// engine/system owns Linux NSS, Samba tdbsam, share path ownership, and
	// the credential vault. Reconcile is invoked at startup; per-event
	// projection (SetUserPassword, ApplyShareOwnership) is wired below.
	sysRoot := os.Getenv("KURA_SYSTEM_ROOT")
	if sysRoot == "" {
		sysRoot = "/"
	}
	sysEng, err := system.New(system.Options{
		DB:               st.DB(),
		FS:               system.NewRealFS(sysRoot),
		Exec:             cmdexec.NewReal(),
		Hasher:           system.NewHasherAdapter(hasher),
		Users:            system.NewUserStoreAdapter(users),
		OnPasswordChange: system.LegacyAuthMethodMirror(st.DB()),
	})
	if err != nil {
		return fmt.Errorf("init system engine: %w", err)
	}
	shareEngine.SetOwnershipApplier(system.NewShareOwnershipAdapter(sysEng))

	// Now that engine/user is available, the Shares page can render the
	// ACL row picker (Sfix001-3) with real user / group dropdowns. The
	// legacy CSV `acl` field is still parsed by parseACLForm fallback.
	sharesDeps := ui.SharesDeps{
		Engine:          shareEngine,
		VolumeLister:    storageEngine,
		PrincipalSource: &principalSourceAdapter{users: users},
	}
	uiRenderer.SetSharesHandlers(
		uiRenderer.SharesHandler(sharesDeps),
		uiRenderer.SharesDeleteHandler(sharesDeps),
	)
	uiRenderer.SetSharesUpdateHandler(uiRenderer.SharesUpdateHandler(sharesDeps))
	uiRenderer.SetSharesACLRowHandler(uiRenderer.SharesACLRowHandler(sharesDeps))

	// Soft-fail on a dev box without /etc/passwd write permission. Production
	// VM has root; dev box logs the failure and continues.
	if recErr := sysEng.Reconcile(ctx); recErr != nil {
		log.Printf("system: startup reconcile failed (non-fatal): %v", recErr)
	}

	authH := uiRenderer.AuthHandler(ui.AuthDeps{
		Users:         users,
		Sessions:      sessions,
		SecureCookies: secureCookies,
		// Federation providers live next to the password form on
		// /login so a linked Google user can sign in without a
		// KuraOS password (Sfix002-2). The lister mirrors the
		// env-var config the Users page already shows; nil-safe
		// when federation isn't configured.
		Providers: &providersListAdapter{db: st.DB()},
	})
	setupH := uiRenderer.SetupHandler(ui.SetupDeps{
		Users:         users,
		Sessions:      sessions,
		SecureCookies: secureCookies,
	})

	// engine/app wiring (S65b510). Falls back to FakeDockerClient on dev
	// boxes where /var/run/docker.sock isn't reachable; production VMs use
	// the real HTTPDockerClient against the docker daemon.
	dockerClient := buildDockerClient(ctx)
	verifier := app.NewLocalVerifier()
	verifier.SetKeysDir(envOr("KURA_APP_KEYS_DIR", "/var/lib/kura/app-keys"))
	if err := verifier.LoadFromDir(); err != nil {
		// Soft-fail: a fresh install has no trusted signers yet and that's
		// expected. Install attempts will fail-closed at verify time with
		// a clear "no trusted key matched" error rather than crashing the
		// daemon. CLI flow at app_lifecycle.go does the same.
		log.Printf("kura: warning: load app signing keys from dir: %v", err)
	}
	registryClient := app.NewHTTPRegistryClient(verifier, &http.Client{Timeout: 30 * time.Second})
	planner := app.NewDatasetPlanner(st.DB(), &app.StaticPoolLister{Pools: discoverPools(ctx, storageEngine)})
	planner.FallbackPool = "tank"
	ports := app.NewPortAllocator(st.DB())
	secrets := app.NewSecretStore(sysEng)
	storageAdapter := &app.StorageAdapter{Engine: storageDatasetAdapter{engine: storageEngine}}
	routeRegistry := app.NewMemoryRouteRegistry()
	lifecycle := app.NewLifecycle(registryClient, planner, ports, secrets, dockerClient, storageAdapter, routeRegistry, st.DB())

	// OIDC OP — built once after sysEng / sessions / users are ready so it
	// can resolve operator sessions at /authorize and persist its signing
	// key in the credential vault. Wiring is best-effort: if the OP can't
	// be built (e.g. RSA generation fails), the daemon still serves the
	// rest of its surface — apps with auth.mode=oidc just won't function.
	oidcProvider := buildOIDCProvider(ctx, st.DB(), sysEng, sessions, users)
	if oidcProvider != nil {
		registrar := newAppOIDCRegistrar(oidcProvider, sysEng, gatewayPublicOrigin())
		lifecycle.OIDC = registrar
	}
	// Federation provider — Google / future external IdPs. Optional in dev.
	// uiRenderer doubles as the federation error renderer so the
	// callback can show an i18n page on unbound subject / provision
	// failure instead of plain http.Error (Sfix002-3).
	federationHandler := buildFederationHandler(ctx, st.DB(), sysEng, users, sessions, uiRenderer)

	// Wire the Users page (S822961 + Sfix001). View carries Users +
	// Groups + Federations + OIDC clients + provider toggles. The
	// CRUD endpoints (POST /ui/admin/users/* and /ui/admin/groups/*)
	// are wired separately so older acceptance tests that only need
	// the read-only view still work without a SystemEngine.
	usersLister := &usersListerAdapter{users: users}
	groupsLister := &groupsListerAdapter{users: users}
	uiRenderer.SetUsersHandler(ui.UsersDeps{
		Users:       usersLister,
		Groups:      groupsLister,
		Federations: &federationLookupAdapter{storage: oidc.NewStorage(st.DB())},
		OIDCClients: oidcClientListAdapter(st.DB()),
		Providers:   &providersListAdapter{db: st.DB()},
		CurrentUser: currentUserFromSession(sessions, users),
	})
	uiRenderer.SetUsersCRUDHandlers(ui.UsersCRUDDeps{
		System:       &systemEngineAdapter{eng: sysEng},
		Lookup:       &shareACLLookupAdapter{shares: shareEngine},
		UsersLister:  usersLister,
		GroupsLister: groupsLister,
		CurrentUser:  currentUserFromSession(sessions, users),
	})
	// Pending-user approval / rejection (S413bd5-3). Uses the same
	// systemEngineAdapter; PendingEngine is a subset of SystemEngine.
	uiRenderer.SetPendingHandlers(ui.PendingDeps{
		Engine: &systemEngineAdapter{eng: sysEng},
	})
	if root := os.Getenv("KURA_APPS_CONFIG_ROOT"); root != "" {
		lifecycle.ConfigRoot = root
	}
	if err := bootstrapAppRegistries(ctx, st.DB(), lifecycle); err != nil {
		log.Printf("apps: bootstrap registries (non-fatal): %v", err)
	}
	appsDeps := ui.AppsDeps{
		Lifecycle:       lifecycle,
		Registry:        registryClient,
		Sources:         loadAppSources(ctx, st.DB()),
		SharePathLister: sharePathListerFromEngine(shareEngine),
	}
	uiRenderer.SetAppsHandler(appsDeps)

	// ── Monitor + Notify wiring (S8a756d) ──────────────────────────────────
	// Ring buffer path: /var/lib/kura/metrics/raw.bin (or KURA_METRICS_PATH).
	rbPath := envOr("KURA_METRICS_PATH", "/var/lib/kura/metrics/raw.bin")
	rb, rbErr := monitor.OpenRingBuffer(rbPath, monitor.DefaultCapacity)
	if rbErr != nil {
		// Non-fatal: a dev box may not have /var/lib/kura writable. The
		// dashboard degrades to the empty-state banner.
		log.Printf("monitor: open ring buffer (non-fatal): %v", rbErr)
		rb = nil
	}
	eventStore := monitor.NewEventStore(st.DB())

	var busRef *monitor.EventBus
	if rb != nil {
		// EventBus with SQLite persistence for every event.
		busRef = monitor.NewEventBus(eventStore.Persist)

		// AlertEvaluator — rules sourced from a later config.json sprint; for
		// now we wire zero rules (alerting is opt-in via config.json `alerts[]`).
		ae := monitor.NewAlertEvaluator(nil, busRef)

		// Metrics collector — collects every 30s, evaluates alert rules.
		probe := monitor.NewSysProbe()
		coll := monitor.NewCollector(rb, &alertingProbe{probe: probe, ae: ae}, nil, 0)
		go coll.Run(ctx, func(err error) {
			log.Printf("monitor: collector: %v", err)
		})

		defer rb.Close()
	}

	// Dashboard deps — graceful degradation when rb == nil.
	uiRenderer.SetDashboardDeps(ui.DashboardDeps{
		RingBuffer: rb,
		EventStore: eventStore,
	})

	// Notify store + settings page.
	notifyStore := notify.NewStore(st.DB())
	uiRenderer.SetSettingsHandler(ui.SettingsDeps{
		NotifyStore: notifyStore,
		EventStore:  eventStore,
	})

	// Notify dispatcher — wired only when EventBus is running.
	if busRef != nil {
		dispatcher := notify.NewDispatcher(notifyStore, nil)
		go dispatcher.Run(ctx, busRef)
	}

	// ── Backup wiring (Se1e7a6) ─────────────────────────────────────────────
	backupStore := backupEngine.NewStore(st.DB())
	backupExec := cmdexec.NewReal()
	backupSnapper := backupEngine.NewZFSCLISnapshotter(backupExec)
	upgradeRootDS := envOr("KURA_UPGRADE_ROOT_DATASET", "tank/rootfs")
	uiRenderer.SetBackupHandler(ui.BackupDeps{
		Store:       backupStore,
		Exec:        backupExec,
		AppStopper:  &lifecycleAppStopper{lc: lifecycle},
		ZFSSnapper:  backupSnapper,
		RootDataset: upgradeRootDS,
	})
	// ── End backup wiring ───────────────────────────────────────────────────

	// /metrics OpenMetrics handler — admin-only via gateway auth middleware.
	var metricsHandler http.Handler
	if rb != nil {
		metricsHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0; charset=utf-8")
			if err := monitor.OpenMetricsWriter(rb, w); err != nil {
				log.Printf("monitor: metrics endpoint: %v", err)
			}
		})
	}
	// ── End monitor wiring ──────────────────────────────────────────────────

	startedAt := time.Now().UTC()
	depsBuild := gateway.Deps{
		Translator:     tr,
		Version:        Version,
		StartedAt:      startedAt,
		UIHandler:      uiRenderer.Routes(),
		AuthHandler:    authH,
		SetupHandler:   setupH,
		Sessions:       sessions,
		Users:          users,
		AppRoutes:      routeRegistry,
		MetricsHandler: metricsHandler,
	}
	if oidcProvider != nil {
		depsBuild.OIDCHandler = oidcProvider.Routes()
	}
	if federationHandler != nil {
		depsBuild.FederationHandler = federationHandler
	}
	depsBuild.PendingApprovalHandler = uiRenderer.PendingApprovalHandler()
	handler := gateway.New(depsBuild)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Println(tr.T(i18n.MsgSystemStartup, port))

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		log.Println(tr.T(i18n.MsgSystemShutdown))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
}
