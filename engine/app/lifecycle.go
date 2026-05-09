package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"
)

// AppLifecycle wires every primitive engine/app exposes (RegistryClient,
// DatasetPlanner, PortAllocator, SecretStore, DockerClient, DatasetWriter)
// into the install / update / uninstall flows the UI / CLI consume.
//
// The lifecycle is the single place "an app's state changes" passes through —
// it owns the ordering rules (validate -> reserve -> materialize -> start ->
// healthcheck -> commit) and the rollback rules (anything reserved before a
// failure is freed before the error returns).
//
// OIDC field added in S822961 — when the manifest declares auth.mode=oidc,
// AppLifecycle.Install hands an OIDCAppClient to OIDC.RegisterAppClient
// and exposes the returned client_id/secret/issuer in the configs template
// data (.Secrets.OIDC*). Nil OIDC is tolerated for legacy or test wiring;
// auth.mode=oidc apps simply skip the registration step.
type AppLifecycle struct {
	Registry RegistryClient
	Planner  *DatasetPlanner
	Ports    *PortAllocator
	Secrets  *SecretStore
	Docker   DockerClient
	Storage  DatasetWriter
	Routes   RouteRegistry
	OIDC     OIDCRegistrar
	DB       *sql.DB

	// HealthTimeout caps how long Install waits for a container's health
	// state to flip to "healthy" before treating it as a failure. Zero means
	// 120s (compose's default).
	HealthTimeout time.Duration
	// HealthPoll is the interval between Inspect polls inside the wait.
	HealthPoll time.Duration

	// ConfigRoot is the host directory where rendered config templates and
	// compose YAML transcripts are written. Defaults to /var/lib/kura/apps.
	ConfigRoot string

	// Now overrides time.Now for deterministic test ordering.
	Now func() time.Time

	mu        sync.Mutex
	listeners map[string][]chan ProgressEvent
}

// DatasetWriter is the slice of engine/storage AppLifecycle needs. Declared
// here (consumer-side interface) so tests inject a fake without importing the
// full storage engine.
type DatasetWriter interface {
	CreateAppDataset(ctx context.Context, dataset string, opts AppDatasetOpts) error
	DestroyAppDataset(ctx context.Context, dataset string) error
	SnapshotAppDataset(ctx context.Context, dataset, snapshot string) error
	RollbackAppDataset(ctx context.Context, dataset, snapshot string) error
	DatasetMountpoint(ctx context.Context, dataset string) (string, error)
}

// AppDatasetOpts is the per-dataset tunable subset DatasetWriter needs.
type AppDatasetOpts struct {
	QuotaBytes int64
	Mountpoint string
}

// RouteRegistry is the slice of internal/gateway AppLifecycle needs to
// (un)register an app's HTTP route. Declared here as a consumer-side
// interface so the gateway can implement it without engine/app importing
// internal/gateway.
type RouteRegistry interface {
	RegisterAppRoute(route AppRoute) error
	UnregisterAppRoute(appID string) error
	ListAppRoutes() []AppRoute
}

// OIDCRegistrar is the slice of engine/auth/oidc AppLifecycle needs to
// auto-register a client_id when manifest.auth.mode == "oidc". The
// adapter cmd/kura wires also persists the secret to the credential
// vault so engine/app does not need to import engine/system.
type OIDCRegistrar interface {
	// RegisterAppClient creates (or refreshes) a confidential client for
	// the app and returns the credentials the manifest configs templates
	// need (.Secrets.OIDC*). Idempotent on the (appID, name) pair: a
	// re-install reuses the same client_id while rotating the secret.
	RegisterAppClient(ctx context.Context, req OIDCAppClient) (OIDCAppClientCreds, error)
	// UnregisterAppClient drops the client and its vault entry.
	UnregisterAppClient(ctx context.Context, appID string) error
}

// OIDCAppClient is what AppLifecycle hands the registrar.
type OIDCAppClient struct {
	AppID        string
	AppName      string
	RedirectURIs []string
}

// OIDCAppClientCreds is the freshly minted client + secret + issuer URL
// returned to AppLifecycle so it can populate the manifest configs
// template data.
type OIDCAppClientCreds struct {
	ClientID     string
	ClientSecret string
	Issuer       string
	RedirectURI  string
}

// AppRoute is the routing manifest -> gateway adapter shape.
//
// AuthMode + HeaderUser were added in S822961 so the gateway can pick
// between forward_auth header injection (legacy apps), OIDC pass-through
// (native apps), and no-auth (rare). Existing routes constructed before
// the field arrived default to AuthModeNone with no header, behaving
// identically to pre-sprint behaviour.
type AppRoute struct {
	AppID       string
	AppName     string
	Mode        RoutingMode
	HostPort    int
	Subdomain   string
	StripPrefix bool
	BasePathEnv string
	Container   string
	AuthMode    AuthMode
	HeaderUser  string
}

// ProgressEvent is one SSE message the install/update/uninstall stream emits.
// Stage is a stable token UI handlers map to translated banners; Detail is a
// human-readable line for the live tail.
type ProgressEvent struct {
	AppID  string    `json:"app_id"`
	Stage  string    `json:"stage"`
	Detail string    `json:"detail"`
	OK     bool      `json:"ok"`
	Final  bool      `json:"final"`
	At     time.Time `json:"at"`
}

// Recognised stage tokens. The UI maps these to MessageIDs; new stages need
// a matching i18n entry.
const (
	StageStart              = "start"
	StagePullImages         = "pull_images"
	StageReservePorts       = "reserve_ports"
	StageProvisionData      = "provision_data"
	StageRenderConfigs      = "render_configs"
	StageStoreSecrets       = "store_secrets"
	StageRegisterOIDCClient = "register_oidc_client"
	StageStartContainers    = "start_containers"
	StageHealthcheck        = "healthcheck"
	StageRegisterRoute      = "register_route"
	StagePersist            = "persist"
	StageRollback           = "rollback"
	StageDone               = "done"
	StageError              = "error"

	StageSnapshot   = "snapshot"
	StagePullUpdate = "pull_update"
	StageStop       = "stop"
	StageRemove     = "remove"
)

// InstallRequest is what the UI / CLI hands the lifecycle. SetupValues are
// keyed by manifest setup.required[].key; Settings are non-secret manifest
// settings (secret values are auto-generated from SecretStore).
type InstallRequest struct {
	Source      RegistrySource
	AppName     string
	Version     string
	SetupValues map[string]string
	Settings    map[string]string
	// AppID is optional — when empty the lifecycle assigns "<name>.<rand>".
	AppID string
}

// AppRecord is one row of the app_installs table. Lifecycle.Install commits
// it; Lifecycle.Uninstall removes it.
type AppRecord struct {
	AppID    string
	Name     string
	Version  string
	Registry string
	Setup    map[string]string
	Settings map[string]string // non-secret values only
	State    string            // "installing" | "running" | "stopped" | "failed" | "uninstalling"
}

// Errors callers may want to detect specifically.
var (
	ErrAppAlreadyInstalled = errors.New("app: already installed")
	ErrAppNotInstalled     = errors.New("app: not installed")
	ErrHealthcheckTimeout  = errors.New("app: healthcheck timed out")
)

// NewLifecycle returns a lifecycle with sensible defaults. Caller may
// override individual fields after construction (e.g. for tests).
func NewLifecycle(reg RegistryClient, planner *DatasetPlanner, ports *PortAllocator,
	secrets *SecretStore, docker DockerClient, storage DatasetWriter, routes RouteRegistry, db *sql.DB) *AppLifecycle {
	return &AppLifecycle{
		Registry:      reg,
		Planner:       planner,
		Ports:         ports,
		Secrets:       secrets,
		Docker:        docker,
		Storage:       storage,
		Routes:        routes,
		DB:            db,
		HealthTimeout: 120 * time.Second,
		HealthPoll:    1 * time.Second,
		ConfigRoot:    "/var/lib/kura/apps",
		listeners:     map[string][]chan ProgressEvent{},
	}
}

// Subscribe returns a channel that receives ProgressEvent for appID. The
// caller MUST drain (or use a buffered consumer) — events are dropped on
// blocked listeners. The returned cancel removes the listener.
func (l *AppLifecycle) Subscribe(appID string) (<-chan ProgressEvent, func()) {
	ch := make(chan ProgressEvent, 32)
	l.mu.Lock()
	l.listeners[appID] = append(l.listeners[appID], ch)
	l.mu.Unlock()
	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		listeners := l.listeners[appID]
		for i, c := range listeners {
			if c == ch {
				close(c)
				l.listeners[appID] = append(listeners[:i], listeners[i+1:]...)
				return
			}
		}
	}
}

// emit fans out an event to subscribers. Best-effort: a slow listener loses
// events rather than stalls the install.
func (l *AppLifecycle) emit(ev ProgressEvent) {
	if ev.At.IsZero() {
		ev.At = l.now()
	}
	l.mu.Lock()
	listeners := append([]chan ProgressEvent(nil), l.listeners[ev.AppID]...)
	l.mu.Unlock()
	for _, ch := range listeners {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (l *AppLifecycle) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// installState carries everything Install acquired so far. On rollback the
// fields tell the cleanup loop what needs to be undone.
type installState struct {
	AppID         string
	NetworkName   string
	Manifest      *Manifest
	DatasetPlans  []DatasetPlan
	CreatedDS     []DatasetPlan
	ReservedPorts []PortReservation
	StoredSecrets []string // ownerIDs
	StartedConts  []string // container names
	WrittenConfig []string // file paths
	RouteRegd     bool
	OIDCRegd      bool
}

// Install runs the full install pipeline:
//  1. Validate request, fetch manifest (verifying signature + hash).
//  2. Reserve datasets (planner + storage CreateAppDataset).
//  3. Reserve ports for every container that exposes them.
//  4. Render config templates to disk.
//  5. Generate vault secrets for required secret settings.
//  6. Pull images.
//  7. Create + start containers.
//  8. Wait for healthcheck.
//  9. Register Gateway route.
//  10. Persist app_installs row.
//
// On failure at any step, the rollback path runs in reverse order.
//
// Returns the AppID assigned to the install. The caller can subscribe via
// Subscribe(appID) BEFORE invoking Install to receive progress events.
func (l *AppLifecycle) Install(ctx context.Context, req InstallRequest) (string, error) {
	if l.Registry == nil {
		return "", errors.New("app: lifecycle.Registry is nil")
	}
	if l.Docker == nil {
		return "", errors.New("app: lifecycle.Docker is nil")
	}
	appID := req.AppID
	if appID == "" {
		appID = newAppID(req.AppName)
	}
	state := &installState{AppID: appID}
	defer func() {
		// On panic or any non-nil error path, the named-return error trips
		// rollback. Install uses err == nil to mean success and skips the
		// rollback in that case.
	}()

	l.emit(ProgressEvent{AppID: appID, Stage: StageStart, OK: true, Detail: req.AppName + "@" + req.Version})

	if err := l.Docker.Ping(ctx); err != nil {
		l.emit(ProgressEvent{AppID: appID, Stage: StageError, OK: false, Final: true,
			Detail: "docker daemon unreachable: " + err.Error()})
		return appID, fmt.Errorf("%w: %v", ErrDockerUnavailable, err)
	}

	// Already installed?
	if existing, err := l.LookupApp(ctx, appID); err == nil && existing.AppID != "" {
		return appID, fmt.Errorf("%w: %s", ErrAppAlreadyInstalled, appID)
	}

	manifest, _, err := l.Registry.FetchManifest(ctx, req.Source, req.AppName, req.Version)
	if err != nil {
		l.emit(ProgressEvent{AppID: appID, Stage: StageError, OK: false, Final: true, Detail: err.Error()})
		return appID, err
	}
	state.Manifest = manifest

	// 1. Datasets.
	l.emit(ProgressEvent{AppID: appID, Stage: StageProvisionData, OK: true, Detail: "planning datasets"})
	plans, err := l.Planner.Plan(ctx, appID, manifest)
	if err != nil {
		return appID, l.fail(state, "plan datasets", err)
	}
	state.DatasetPlans = plans
	for _, plan := range plans {
		opts := AppDatasetOpts{QuotaBytes: parseSize(plan.Quota)}
		if l.Storage != nil {
			if err := l.Storage.CreateAppDataset(ctx, plan.DatasetPath, opts); err != nil {
				return appID, l.fail(state, "create dataset "+plan.DatasetPath, err)
			}
		}
		state.CreatedDS = append(state.CreatedDS, plan)
		l.emit(ProgressEvent{AppID: appID, Stage: StageProvisionData, OK: true,
			Detail: fmt.Sprintf("dataset %s ready", plan.DatasetPath)})
	}
	datasetPaths, err := l.resolveDatasetPaths(ctx, plans)
	if err != nil {
		return appID, l.fail(state, "resolve dataset paths", err)
	}

	// 2. Ports.
	l.emit(ProgressEvent{AppID: appID, Stage: StageReservePorts, OK: true, Detail: "reserving ports"})
	portMap := map[PortKey]int{}
	for _, cname := range containerNames(manifest.Containers) {
		c := manifest.Containers[cname]
		for _, raw := range c.Ports {
			cp, err := parseManifestPort(raw)
			if err != nil {
				return appID, l.fail(state, "parse port", err)
			}
			host, err := l.Ports.Reserve(ctx, PortReservation{
				AppID: appID, Container: cname, ManifestPort: cp,
			})
			if err != nil {
				return appID, l.fail(state, "reserve port", err)
			}
			portMap[PortKey{Container: cname, ManifestPort: cp}] = host
			state.ReservedPorts = append(state.ReservedPorts, PortReservation{
				AppID: appID, Container: cname, ManifestPort: cp, HostPort: host,
			})
			l.emit(ProgressEvent{AppID: appID, Stage: StageReservePorts, OK: true,
				Detail: fmt.Sprintf("%s/%d -> host %d", cname, cp, host)})
		}
	}

	// 3. Secrets — every required type=secret setting goes through the vault.
	l.emit(ProgressEvent{AppID: appID, Stage: StageStoreSecrets, OK: true, Detail: "ensuring secrets"})
	secrets := map[string]string{}
	for _, s := range manifest.Settings {
		if s.Type == SettingSecret {
			if l.Secrets == nil {
				return appID, l.fail(state, "secret store missing", errors.New("SecretStore is nil"))
			}
			val, err := l.Secrets.EnsureSecret(ctx, appID, s.Key)
			if err != nil {
				return appID, l.fail(state, "ensure secret "+s.Key, err)
			}
			secrets[s.Key] = val
			state.StoredSecrets = append(state.StoredSecrets, s.Key)
			continue
		}
		// non-secret default / operator-supplied value
		if v, ok := req.Settings[s.Key]; ok && v != "" {
			secrets[s.Key] = v
		} else if s.Default != "" {
			secrets[s.Key] = s.Default
		}
	}

	// 3.5 OIDC client auto-registration. Only when manifest.auth.mode=oidc
	// and an OIDCRegistrar is wired. The returned creds are exposed in
	// configs template data as .Secrets.OIDCClientID / OIDCClientSecret /
	// OIDCIssuer / OIDCRedirectURI so the manifest's app config template
	// can write them into the container's config file at install time.
	if manifest.Auth.Mode == AuthModeOIDC && l.OIDC != nil {
		l.emit(ProgressEvent{AppID: appID, Stage: StageRegisterOIDCClient, OK: true,
			Detail: "registering OIDC client"})
		redirects := defaultRedirectURIs(manifest)
		creds, err := l.OIDC.RegisterAppClient(ctx, OIDCAppClient{
			AppID:        appID,
			AppName:      manifest.Name,
			RedirectURIs: redirects,
		})
		if err != nil {
			return appID, l.fail(state, "register oidc client", err)
		}
		state.OIDCRegd = true
		secrets["OIDCClientID"] = creds.ClientID
		secrets["OIDCClientSecret"] = creds.ClientSecret
		secrets["OIDCIssuer"] = creds.Issuer
		secrets["OIDCRedirectURI"] = creds.RedirectURI
		l.emit(ProgressEvent{AppID: appID, Stage: StageRegisterOIDCClient, OK: true,
			Detail: "client " + creds.ClientID + " registered"})
	}

	// 4. Share paths from setup.required (share_picker fields).
	sharePaths := map[string]string{}
	for _, sf := range manifest.Setup.Required {
		if sf.Type == SetupSharePicker {
			val := req.SetupValues[sf.Key]
			if val == "" {
				return appID, l.fail(state, "missing setup", fmt.Errorf("setup field %q required", sf.Key))
			}
			// Each share defined in the manifest receives the same picked path
			// when only one share_picker exists. When multiple share_pickers
			// exist, the field key must equal the share name.
			if len(manifest.Shares) == 1 {
				sharePaths[manifest.Shares[0].Name] = val
			} else {
				sharePaths[sf.Key] = val
			}
		}
	}

	// 5. Configs.
	in := InstallInputs{
		AppID:         appID,
		SharePaths:    sharePaths,
		DatasetPaths:  datasetPaths,
		PortBindings:  portMap,
		Secrets:       secrets,
		ConfigOutputs: map[string]string{},
		NetworkName:   "kura-" + manifest.Name,
	}
	state.NetworkName = in.NetworkName
	if len(manifest.Configs) > 0 {
		l.emit(ProgressEvent{AppID: appID, Stage: StageRenderConfigs, OK: true, Detail: "rendering configs"})
		for _, cfg := range manifest.Configs {
			path, err := l.renderConfig(appID, cfg, in)
			if err != nil {
				return appID, l.fail(state, "render config", err)
			}
			in.ConfigOutputs[cfg.Source] = path
			state.WrittenConfig = append(state.WrittenConfig, path)
		}
	}

	// 6. Build compose + persist transcript.
	proj, err := BuildCompose(manifest, in)
	if err != nil {
		return appID, l.fail(state, "build compose", err)
	}
	composeYAML, err := MarshalCompose(proj)
	if err != nil {
		return appID, l.fail(state, "marshal compose", err)
	}
	transcriptPath, err := l.writeTranscript(appID, composeYAML)
	if err != nil {
		return appID, l.fail(state, "write transcript", err)
	}
	state.WrittenConfig = append(state.WrittenConfig, transcriptPath)

	// 7. Pull images.
	l.emit(ProgressEvent{AppID: appID, Stage: StagePullImages, OK: true, Detail: "pulling images"})
	for _, cname := range containerNames(manifest.Containers) {
		c := manifest.Containers[cname]
		if err := l.Docker.PullImage(ctx, c.Image); err != nil {
			return appID, l.fail(state, "pull "+c.Image, err)
		}
		l.emit(ProgressEvent{AppID: appID, Stage: StagePullImages, OK: true, Detail: c.Image + " pulled"})
	}

	// 8. Network + containers.
	if err := l.Docker.EnsureNetwork(ctx, in.NetworkName); err != nil {
		return appID, l.fail(state, "ensure network", err)
	}
	l.emit(ProgressEvent{AppID: appID, Stage: StageStartContainers, OK: true, Detail: "starting containers"})
	createOrder, err := topoSort(manifest.Containers)
	if err != nil {
		return appID, l.fail(state, "topo sort", err)
	}
	for _, cname := range createOrder {
		spec := buildContainerSpec(manifest, cname, in, proj.Services[cname])
		if _, err := l.Docker.CreateContainer(ctx, spec); err != nil {
			return appID, l.fail(state, "create container "+cname, err)
		}
		if err := l.Docker.StartContainer(ctx, spec.Name); err != nil {
			return appID, l.fail(state, "start container "+cname, err)
		}
		state.StartedConts = append(state.StartedConts, spec.Name)
		l.emit(ProgressEvent{AppID: appID, Stage: StageStartContainers, OK: true,
			Detail: cname + " started"})
	}

	// 9. Healthcheck — wait for the manifest's nominated health container
	// (or the first container) to report healthy.
	healthTarget := healthCheckTarget(manifest, createOrder)
	if healthTarget != "" {
		l.emit(ProgressEvent{AppID: appID, Stage: StageHealthcheck, OK: true, Detail: "waiting for " + healthTarget})
		if err := l.waitHealthy(ctx, containerName(appID, healthTarget)); err != nil {
			return appID, l.fail(state, "healthcheck "+healthTarget, err)
		}
		l.emit(ProgressEvent{AppID: appID, Stage: StageHealthcheck, OK: true, Detail: healthTarget + " healthy"})
	}

	// 10. Gateway route.
	if l.Routes != nil {
		route := buildRoute(appID, manifest, portMap)
		if err := l.Routes.RegisterAppRoute(route); err != nil {
			return appID, l.fail(state, "register route", err)
		}
		state.RouteRegd = true
		l.emit(ProgressEvent{AppID: appID, Stage: StageRegisterRoute, OK: true,
			Detail: routeDescription(route)})
	}

	// 11. Persist record.
	if err := l.persistAppRecord(ctx, AppRecord{
		AppID: appID, Name: manifest.Name, Version: manifest.Version,
		Registry: req.Source.Name,
		Setup:    req.SetupValues,
		Settings: nonSecretSettings(manifest, req.Settings),
		State:    "running",
	}); err != nil {
		return appID, l.fail(state, "persist record", err)
	}
	l.emit(ProgressEvent{AppID: appID, Stage: StageDone, OK: true, Final: true,
		Detail: req.AppName + " installed"})
	return appID, nil
}

// fail emits the error event then runs rollback, returning the wrapped error.
func (l *AppLifecycle) fail(state *installState, what string, cause error) error {
	wrapped := fmt.Errorf("%s: %w", what, cause)
	l.emit(ProgressEvent{AppID: state.AppID, Stage: StageError, OK: false, Detail: wrapped.Error()})
	l.rollbackInstall(context.Background(), state)
	l.emit(ProgressEvent{AppID: state.AppID, Stage: StageDone, OK: false, Final: true, Detail: wrapped.Error()})
	return wrapped
}

// rollbackInstall undoes everything installState recorded.
func (l *AppLifecycle) rollbackInstall(ctx context.Context, state *installState) {
	l.emit(ProgressEvent{AppID: state.AppID, Stage: StageRollback, OK: true, Detail: "rolling back"})
	// Containers (reverse start order).
	for i := len(state.StartedConts) - 1; i >= 0; i-- {
		name := state.StartedConts[i]
		_ = l.Docker.StopContainer(ctx, name, 5*time.Second)
		_ = l.Docker.RemoveContainer(ctx, name, true)
	}
	// Route.
	if state.RouteRegd && l.Routes != nil {
		_ = l.Routes.UnregisterAppRoute(state.AppID)
	}
	// OIDC client.
	if state.OIDCRegd && l.OIDC != nil {
		_ = l.OIDC.UnregisterAppClient(ctx, state.AppID)
	}
	// Configs / transcripts.
	for _, p := range state.WrittenConfig {
		_ = os.Remove(p)
	}
	// Secrets — leave the vault entries (they're idempotent, tied to AppID;
	// next install re-uses the same secret to avoid re-issuing). Removal on
	// uninstall is a separate concern.
	// Ports.
	for _, r := range state.ReservedPorts {
		_, _ = l.DB.ExecContext(ctx,
			`DELETE FROM app_port_reservations WHERE app_id = ? AND container = ? AND manifest_port = ?`,
			r.AppID, r.Container, r.ManifestPort)
	}
	// Datasets — only the ones we created (not pre-existing ones).
	for i := len(state.CreatedDS) - 1; i >= 0; i-- {
		plan := state.CreatedDS[i]
		if l.Storage != nil {
			_ = l.Storage.DestroyAppDataset(ctx, plan.DatasetPath)
		}
		_, _ = l.DB.ExecContext(ctx,
			`DELETE FROM app_dataset_plan WHERE app_id = ? AND dataset_name = ?`,
			plan.AppID, plan.DatasetName)
	}
}

// waitHealthy polls Inspect until container reports Healthy, the timeout
// expires, or the context is cancelled.
func (l *AppLifecycle) waitHealthy(ctx context.Context, name string) error {
	timeout := l.HealthTimeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	poll := l.HealthPoll
	if poll == 0 {
		poll = time.Second
	}
	deadline := l.now().Add(timeout)
	for {
		info, err := l.Docker.InspectContainer(ctx, name)
		if err != nil {
			return err
		}
		switch info.Health {
		case "healthy":
			return nil
		case "unhealthy":
			return fmt.Errorf("%w: %s reported unhealthy", ErrHealthcheckTimeout, name)
		}
		// Container without a HEALTHCHECK reports State=running and Health=""
		// — accept that as healthy after one poll.
		if info.Health == "" && info.State == "running" {
			return nil
		}
		if !l.now().Before(deadline) {
			return fmt.Errorf("%w: %s never reported healthy", ErrHealthcheckTimeout, name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// resolveDatasetPaths consults DatasetWriter for the on-host mountpoint of
// each dataset. Returns dataset_name -> host_path.
func (l *AppLifecycle) resolveDatasetPaths(ctx context.Context, plans []DatasetPlan) (map[string]string, error) {
	out := map[string]string{}
	for _, plan := range plans {
		if l.Storage == nil {
			out[plan.DatasetName] = filepath.Join("/", plan.DatasetPath)
			continue
		}
		mp, err := l.Storage.DatasetMountpoint(ctx, plan.DatasetPath)
		if err != nil {
			return nil, err
		}
		out[plan.DatasetName] = mp
	}
	return out, nil
}

// renderConfig substitutes the manifest config template and writes it to
// <ConfigRoot>/<appID>/configs/<basename>. Returns the absolute path.
func (l *AppLifecycle) renderConfig(appID string, cfg ConfigTemplate, in InstallInputs) (string, error) {
	dir := filepath.Join(l.configRoot(), appID, "configs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	srcPath := cfg.Source
	if !filepath.IsAbs(srcPath) {
		srcPath = filepath.Join(l.configRoot(), appID, "manifest-configs", srcPath)
	}
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		// Tolerate missing template source on a dev box — write the raw
		// mountpoint name as a placeholder so the install does not abort.
		// Production registries ship the file; this is a smoke-test cushion.
		raw = []byte(fmt.Sprintf("# template %s not provided\n", cfg.Source))
	}
	tmpl, err := template.New(cfg.Source).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", cfg.Source, err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, buildTemplateData(in)); err != nil {
		return "", fmt.Errorf("execute %s: %w", cfg.Source, err)
	}
	dst := filepath.Join(dir, filepath.Base(cfg.Source))
	if err := os.WriteFile(dst, []byte(buf.String()), 0o640); err != nil {
		return "", fmt.Errorf("write %s: %w", dst, err)
	}
	return dst, nil
}

// writeTranscript stores the rendered compose YAML at
// <ConfigRoot>/<appID>/compose.yaml so the operator can audit what was
// actually installed.
func (l *AppLifecycle) writeTranscript(appID string, composeYAML []byte) (string, error) {
	dir := filepath.Join(l.configRoot(), appID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, composeYAML, 0o640); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

func (l *AppLifecycle) configRoot() string {
	if l.ConfigRoot != "" {
		return l.ConfigRoot
	}
	return "/var/lib/kura/apps"
}

// persistAppRecord stores the install row.
func (l *AppLifecycle) persistAppRecord(ctx context.Context, rec AppRecord) error {
	setupJSON, err := mapToJSON(rec.Setup)
	if err != nil {
		return err
	}
	settingsJSON, err := mapToJSON(rec.Settings)
	if err != nil {
		return err
	}
	_, err = l.DB.ExecContext(ctx, `
		INSERT INTO app_installs
		    (app_id, name, version, registry, state, setup_json, settings_json, installed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
		        strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		ON CONFLICT(app_id) DO UPDATE SET
		    version = excluded.version,
		    state = excluded.state,
		    settings_json = excluded.settings_json,
		    updated_at = excluded.updated_at
	`, rec.AppID, rec.Name, rec.Version, rec.Registry, rec.State, setupJSON, settingsJSON)
	return err
}

// LookupApp returns the persisted AppRecord for appID. Returns AppRecord{}
// with no error when the app is absent — install uses this to detect
// "already installed".
func (l *AppLifecycle) LookupApp(ctx context.Context, appID string) (AppRecord, error) {
	row := l.DB.QueryRowContext(ctx, `
		SELECT app_id, name, version, registry, state, setup_json, settings_json
		FROM app_installs WHERE app_id = ?
	`, appID)
	var rec AppRecord
	var setupJSON, settingsJSON string
	if err := row.Scan(&rec.AppID, &rec.Name, &rec.Version, &rec.Registry, &rec.State, &setupJSON, &settingsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AppRecord{}, nil
		}
		return AppRecord{}, err
	}
	rec.Setup = jsonToMap(setupJSON)
	rec.Settings = jsonToMap(settingsJSON)
	return rec, nil
}

// ListApps returns all installed apps.
func (l *AppLifecycle) ListApps(ctx context.Context) ([]AppRecord, error) {
	rows, err := l.DB.QueryContext(ctx, `
		SELECT app_id, name, version, registry, state, setup_json, settings_json
		FROM app_installs ORDER BY name, app_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppRecord
	for rows.Next() {
		var rec AppRecord
		var setupJSON, settingsJSON string
		if err := rows.Scan(&rec.AppID, &rec.Name, &rec.Version, &rec.Registry, &rec.State, &setupJSON, &settingsJSON); err != nil {
			return nil, err
		}
		rec.Setup = jsonToMap(setupJSON)
		rec.Settings = jsonToMap(settingsJSON)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// nonSecretSettings filters out type=secret settings before persisting; the
// vault holds the secret, config.json carries only the placeholder.
func nonSecretSettings(m *Manifest, supplied map[string]string) map[string]string {
	out := map[string]string{}
	secretSet := map[string]bool{}
	for _, s := range m.Settings {
		if s.Type == SettingSecret {
			secretSet[s.Key] = true
		}
	}
	for k, v := range supplied {
		if !secretSet[k] {
			out[k] = v
		}
	}
	return out
}

// healthCheckTarget picks the manifest container the install pipeline should
// wait on. Manifest.Health.Container takes precedence; else the first
// container in topological order.
func healthCheckTarget(m *Manifest, order []string) string {
	if m.Health != nil && m.Health.Container != "" {
		return m.Health.Container
	}
	if len(order) > 0 {
		return order[len(order)-1] // start with last-started (deepest dep)
	}
	return ""
}

// buildContainerSpec maps manifest container + compose service into the
// DockerClient ContainerSpec. Compose service supplies env / volumes / ports
// (already substituted), manifest supplies command/entrypoint/healthcheck.
func buildContainerSpec(m *Manifest, cname string, in InstallInputs, svc ComposeService) ContainerSpec {
	c := m.Containers[cname]
	mounts := make([]MountSpec, 0, len(svc.Volumes))
	for _, v := range svc.Volumes {
		// "src:dst[:ro]"
		colon := strings.Split(v, ":")
		if len(colon) < 2 {
			continue
		}
		spec := MountSpec{Type: "bind", Source: colon[0], Target: colon[1]}
		if len(colon) >= 3 && colon[2] == "ro" {
			spec.ReadOnly = true
		}
		mounts = append(mounts, spec)
	}
	pm := []PortBinding{}
	for _, p := range svc.Ports {
		// "host:container"
		var host, cont int
		_, _ = fmt.Sscanf(p, "%d:%d", &host, &cont)
		if host > 0 && cont > 0 {
			pm = append(pm, PortBinding{HostPort: host, ContainerPort: cont, Protocol: "tcp"})
		}
	}
	hc := (*HealthcheckSpec)(nil)
	if c.Healthcheck != nil {
		hc = &HealthcheckSpec{
			Test:     append([]string{}, c.Healthcheck.Test...),
			Interval: parseDuration(c.Healthcheck.Interval),
			Timeout:  parseDuration(c.Healthcheck.Timeout),
			Retries:  c.Healthcheck.Retries,
		}
	}
	return ContainerSpec{
		Name:        svc.ContainerNm,
		Image:       svc.Image,
		Env:         svc.Environment,
		Cmd:         svc.Command,
		Entrypoint:  svc.Entrypoint,
		WorkingDir:  svc.WorkingDir,
		Mounts:      mounts,
		PortMap:     pm,
		Network:     in.NetworkName,
		Labels:      svc.Labels,
		Restart:     svc.Restart,
		DependsOn:   c.DependsOn,
		Healthcheck: hc,
	}
}

// topoSort returns container names in dependency order (depends_on first).
// Cycle detection returns an error.
func topoSort(cs map[string]Container) ([]string, error) {
	indeg := map[string]int{}
	out := make([]string, 0, len(cs))
	for n := range cs {
		indeg[n] = 0
	}
	for n, c := range cs {
		_ = n
		for _, dep := range c.DependsOn {
			if _, ok := cs[dep]; ok {
				indeg[n]++
			}
		}
	}
	// Stable Kahn: pick the lex-smallest in-degree-0 node each iteration.
	for len(out) < len(cs) {
		picks := []string{}
		for n, d := range indeg {
			if d == 0 {
				picks = append(picks, n)
			}
		}
		if len(picks) == 0 {
			return nil, fmt.Errorf("cycle detected in containers")
		}
		sort.Strings(picks)
		pick := picks[0]
		indeg[pick] = -1
		out = append(out, pick)
		for n, c := range cs {
			for _, dep := range c.DependsOn {
				if dep == pick {
					indeg[n]--
				}
			}
		}
	}
	return out, nil
}

// buildRoute is the manifest -> AppRoute adapter.
func buildRoute(appID string, m *Manifest, ports map[PortKey]int) AppRoute {
	r := AppRoute{
		AppID:       appID,
		AppName:     m.Name,
		Mode:        m.Routing.Mode,
		StripPrefix: m.Routing.StripPrefix,
		BasePathEnv: m.Routing.BasePathEnv,
		Container:   m.Routing.Container,
		Subdomain:   m.Routing.Subdomain,
		AuthMode:    m.Auth.Mode,
		HeaderUser:  m.Auth.HeaderUser,
	}
	// Resolve target host port: prefer routing.port, else first port of the
	// nominated container.
	if m.Routing.Port != nil {
		// translate manifest port to host port
		container := m.Routing.Container
		if container == "" && len(m.Containers) > 0 {
			for n := range m.Containers {
				container = n
				break
			}
		}
		if host, ok := ports[PortKey{Container: container, ManifestPort: *m.Routing.Port}]; ok {
			r.HostPort = host
		}
	} else {
		container := m.Routing.Container
		if container == "" {
			for n := range m.Containers {
				container = n
				break
			}
		}
		// pick first port for the container
		var lowestKey PortKey
		var lowestSet bool
		for k, host := range ports {
			if k.Container == container {
				if !lowestSet || k.ManifestPort < lowestKey.ManifestPort {
					lowestKey = k
					lowestSet = true
					r.HostPort = host
				}
			}
		}
	}
	return r
}

func routeDescription(r AppRoute) string {
	switch r.Mode {
	case RoutingModePath:
		return fmt.Sprintf("/apps/%s -> :%d (strip=%t)", r.AppName, r.HostPort, r.StripPrefix)
	case RoutingModePort:
		return fmt.Sprintf("port %d", r.HostPort)
	case RoutingModeSubdomain:
		return fmt.Sprintf("%s.* -> :%d", r.Subdomain, r.HostPort)
	}
	return fmt.Sprintf("mode=%s host=%d", r.Mode, r.HostPort)
}

// NewAppIDForName is the public wrapper around newAppID used by UI/CLI when
// the caller wants to assign the AppID up-front (so it can subscribe to
// progress events before invoking Install).
func NewAppIDForName(name string) string { return newAppID(name) }

// newAppID returns "<name>.<6-hex-bytes>" — lower-cased ASCII letters/digits
// from name with a short random suffix to permit multiple installs of the
// same app.
func newAppID(name string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return -1
	}, name)
	var b [3]byte
	_, _ = rand.Read(b[:])
	return clean + "." + hex.EncodeToString(b[:])
}

// parseSize accepts "1G", "200M", "5T" — decimal suffix multipliers (1000-based),
// matching ZFS quota convention. Returns 0 for the empty string.
func parseSize(s string) int64 {
	if s == "" {
		return 0
	}
	mul := int64(1)
	num := s
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'K', 'k':
			mul = 1000
			num = s[:n-1]
		case 'M', 'm':
			mul = 1000 * 1000
			num = s[:n-1]
		case 'G', 'g':
			mul = 1000 * 1000 * 1000
			num = s[:n-1]
		case 'T', 't':
			mul = 1000 * 1000 * 1000 * 1000
			num = s[:n-1]
		}
	}
	var v int64
	if _, err := fmt.Sscanf(num, "%d", &v); err != nil {
		return 0
	}
	return v * mul
}

// defaultRedirectURIs returns the redirect_uri set the gateway exposes
// for an app's OIDC code flow. v1 ships one URL per app (the path-mode
// callback at /apps/<name>/oidc/callback) — apps that need additional
// URIs (extra subdomains, etc.) can ship a manifest setting in v1.x.
func defaultRedirectURIs(m *Manifest) []string {
	return []string{"/apps/" + m.Name + "/oidc/callback"}
}

// parseDuration is a permissive wrapper for time.ParseDuration that returns
// 0 on parse failure (the docker SDK treats 0 as "use default").
func parseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
