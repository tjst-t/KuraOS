// Package app owns the schema, parser, validator, registry client, and
// runtime allocators (dataset / port / secret) for KuraOS managed apps.
//
// design.md §7.2 defines the YAML manifest shape. This package mirrors that
// shape into Go structs (snake_case JSON / YAML tags, CamelCase fields per
// CLAUDE.md) and exposes a strict Parse + Validate so a malformed manifest
// fails at install time rather than at container start. Subsequent sprints
// (S65b510 install/update/uninstall) consume the validated Manifest and
// the allocator output to materialise compose YAML and Gateway routes.
package app

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AppType partitions apps into the two integration models from design.md
// §7.1: native (KuraOS File API + OIDC) and legacy (bind mount + forward_auth).
type AppType string

const (
	AppTypeNative AppType = "native"
	AppTypeLegacy AppType = "legacy"
)

// RoutingMode mirrors the four supported gateway routing modes from
// design.md §7.8. Path-based modes need either base_path_env (the app
// reads its own base path from env) or strip_prefix (Gateway rewrites).
type RoutingMode string

const (
	RoutingModePath      RoutingMode = "path"
	RoutingModePort      RoutingMode = "port"
	RoutingModeSubdomain RoutingMode = "subdomain"
)

// AuthMode picks how the app receives identity from the Gateway. native
// apps use oidc (OIDC code flow against the KuraOS OP); legacy apps use
// forward_auth (Gateway injects X-Forwarded-User after session lookup).
type AuthMode string

const (
	AuthModeOIDC        AuthMode = "oidc"
	AuthModeForwardAuth AuthMode = "forward_auth"
	AuthModeNone        AuthMode = "none"
)

// SetupRequiredKind enumerates the design.md §7.3 input types the install
// wizard can render. Constrained to a closed set so the UI sprint can
// switch on it exhaustively.
type SetupRequiredKind string

const (
	SetupSharePicker SetupRequiredKind = "share_picker"
	SetupSelect      SetupRequiredKind = "select"
	SetupToggle      SetupRequiredKind = "toggle"
	SetupText        SetupRequiredKind = "text"
)

// SettingType distinguishes the per-app settings the manifest declares.
// 'secret' is the only kind that gets routed into the credential vault
// (priority #1) — the others live in the runtime config block.
type SettingType string

const (
	SettingSecret SettingType = "secret"
	SettingString SettingType = "string"
	SettingNumber SettingType = "number"
	SettingBool   SettingType = "bool"
)

// VolumeKind tells the dataset planner whether the volume is backed by an
// app-private dataset (kind=dataset) or a user share that is bind-mounted
// in (kind=share). The two come from different storage trees.
type VolumeKind string

const (
	VolumeKindDataset VolumeKind = "dataset"
	VolumeKindShare   VolumeKind = "share"
	VolumeKindBind    VolumeKind = "bind"
)

// PoolHint asks the dataset planner to prefer a particular pool role. v1
// recognises 'ssd' (small-block / fast tier) and 'tank' (default capacity
// tier). Unrecognised values are tolerated as opaque strings — planners
// fall back to the default pool when no match is found.
type PoolHint string

const (
	PoolHintSSD  PoolHint = "ssd"
	PoolHintTank PoolHint = "tank"
)

// Manifest is the in-memory representation of an app's manifest.yaml.
// Field order mirrors design.md §7.2 to make diff-reading easier.
type Manifest struct {
	APIVersion  string            `yaml:"apiVersion" json:"api_version"`
	Name        string            `yaml:"name" json:"name"`
	Version     string            `yaml:"version" json:"version"`
	Type        AppType           `yaml:"type" json:"type"`
	DisplayName map[string]string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	Description map[string]string `yaml:"description,omitempty" json:"description,omitempty"`
	Homepage    string            `yaml:"homepage,omitempty" json:"homepage,omitempty"`

	Containers map[string]Container `yaml:"containers" json:"containers"`
	Shares     []ShareMount         `yaml:"shares,omitempty" json:"shares,omitempty"`
	Storage    StorageBlock         `yaml:"storage,omitempty" json:"storage,omitempty"`
	Setup      SetupBlock           `yaml:"setup,omitempty" json:"setup,omitempty"`
	Settings   []Setting            `yaml:"settings,omitempty" json:"settings,omitempty"`
	Resources  *Resources           `yaml:"resources,omitempty" json:"resources,omitempty"`
	Routing    Routing              `yaml:"routing" json:"routing"`
	Auth       Auth                 `yaml:"auth" json:"auth"`
	Health     *Health              `yaml:"health,omitempty" json:"health,omitempty"`
	Backup     *BackupSpec          `yaml:"backup,omitempty" json:"backup,omitempty"`
	Configs    []ConfigTemplate     `yaml:"configs,omitempty" json:"configs,omitempty"`
}

// Container is one entry of the manifest's `containers:` map. The map key
// (e.g. "server", "db", "redis") becomes the docker compose service name.
type Container struct {
	Image       string            `yaml:"image" json:"image"`
	Ports       []string          `yaml:"ports,omitempty" json:"ports,omitempty"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	DependsOn   []string          `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Volumes     []ContainerVolume `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	Command     []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Entrypoint  []string          `yaml:"entrypoint,omitempty" json:"entrypoint,omitempty"`
	WorkingDir  string            `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
	Restart     string            `yaml:"restart,omitempty" json:"restart,omitempty"`
	Healthcheck *Healthcheck      `yaml:"healthcheck,omitempty" json:"healthcheck,omitempty"`
}

// ContainerVolume is one mount inside a container. `name` references either
// a storage.datasets entry (when type=dataset) or a shares[] entry name
// (when type=share); type=bind takes a host path in `source`.
type ContainerVolume struct {
	Type       VolumeKind `yaml:"type" json:"type"`
	Name       string     `yaml:"name,omitempty" json:"name,omitempty"`
	Source     string     `yaml:"source,omitempty" json:"source,omitempty"`
	Mountpoint string     `yaml:"mountpoint" json:"mountpoint"`
	ReadOnly   bool       `yaml:"read_only,omitempty" json:"read_only,omitempty"`
}

// ShareMount is one entry of the top-level `shares:` block. The user
// picks the actual KuraOS share at install time via setup.required's
// share_picker; this entry just declares the contract (name + access
// + where to mount it inside the chosen container).
type ShareMount struct {
	Name       string `yaml:"name" json:"name"`
	Access     string `yaml:"access" json:"access"`
	Mountpoint string `yaml:"mountpoint" json:"mountpoint"`
	Container  string `yaml:"container,omitempty" json:"container,omitempty"`
}

// StorageBlock holds the app-private datasets the planner creates.
type StorageBlock struct {
	Datasets []Dataset `yaml:"datasets,omitempty" json:"datasets,omitempty"`
}

// Dataset is one entry of storage.datasets. pool_hint asks the planner
// to prefer a tier; if the hint is unsatisfiable we fall back to tank.
type Dataset struct {
	Name      string   `yaml:"name" json:"name"`
	PoolHint  PoolHint `yaml:"pool_hint,omitempty" json:"pool_hint,omitempty"`
	Backup    bool     `yaml:"backup,omitempty" json:"backup,omitempty"`
	Quota     string   `yaml:"quota,omitempty" json:"quota,omitempty"`
	Recordize string   `yaml:"recordsize,omitempty" json:"recordsize,omitempty"`
}

// SetupBlock holds the wizard inputs the operator answers at install time.
// design.md §7.3 emphasises 'required' should typically be a single
// share_picker — the validator does not enforce that quantitative goal but
// the schema makes it explicit by separating required from optional.
type SetupBlock struct {
	Required []SetupField `yaml:"required,omitempty" json:"required,omitempty"`
	Optional []SetupField `yaml:"optional,omitempty" json:"optional,omitempty"`
}

// SetupField is one wizard input. Label is i18n locale-keyed.
type SetupField struct {
	Key     string            `yaml:"key" json:"key"`
	Type    SetupRequiredKind `yaml:"type" json:"type"`
	Label   map[string]string `yaml:"label" json:"label"`
	Default string            `yaml:"default,omitempty" json:"default,omitempty"`
	Options []string          `yaml:"options,omitempty" json:"options,omitempty"`
	Help    map[string]string `yaml:"help,omitempty" json:"help,omitempty"`
}

// Setting is one entry of `settings:`. Required+secret implies the
// installer auto-generates a value at install time and stores it in the
// vault (priority #1) — the operator never types it.
type Setting struct {
	Key      string      `yaml:"key" json:"key"`
	Type     SettingType `yaml:"type" json:"type"`
	Required bool        `yaml:"required,omitempty" json:"required,omitempty"`
	Default  string      `yaml:"default,omitempty" json:"default,omitempty"`
	Min      *int        `yaml:"min,omitempty" json:"min,omitempty"`
	Max      *int        `yaml:"max,omitempty" json:"max,omitempty"`
}

// Resources mirrors the design.md `resources:` block. Used as install-time
// pre-flight check (free space, RAM head-room).
type Resources struct {
	Storage string `yaml:"storage,omitempty" json:"storage,omitempty"`
	Memory  string `yaml:"memory,omitempty" json:"memory,omitempty"`
}

// Routing chooses the gateway path/port/subdomain shape. base_path_env is
// the env-var name the container reads to learn its own base path when
// strip_prefix is false; strip_prefix=true asks the gateway to rewrite.
type Routing struct {
	Mode        RoutingMode `yaml:"mode" json:"mode"`
	BasePathEnv string      `yaml:"base_path_env,omitempty" json:"base_path_env,omitempty"`
	StripPrefix bool        `yaml:"strip_prefix,omitempty" json:"strip_prefix,omitempty"`
	Port        *int        `yaml:"port,omitempty" json:"port,omitempty"`
	Container   string      `yaml:"container,omitempty" json:"container,omitempty"`
	Subdomain   string      `yaml:"subdomain,omitempty" json:"subdomain,omitempty"`
}

// Auth is the top-level auth block. forward_auth needs header_user; oidc
// needs nothing because the Gateway already knows the OP details.
type Auth struct {
	Mode       AuthMode `yaml:"mode" json:"mode"`
	HeaderUser string   `yaml:"header_user,omitempty" json:"header_user,omitempty"`
}

// Health mirrors the manifest health block. interval is parsed by the
// container scheduler; the validator keeps it as a string so we don't
// pull in time.ParseDuration semantics here.
type Health struct {
	Endpoint  string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	Container string `yaml:"container,omitempty" json:"container,omitempty"`
	Interval  string `yaml:"interval,omitempty" json:"interval,omitempty"`
	Cmd       string `yaml:"cmd,omitempty" json:"cmd,omitempty"`
}

// Healthcheck is the per-container override variant. design.md uses both
// shapes — top-level health for the public probe, per-container
// healthcheck for compose-level liveness.
type Healthcheck struct {
	Test     []string `yaml:"test,omitempty" json:"test,omitempty"`
	Interval string   `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout  string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Retries  int      `yaml:"retries,omitempty" json:"retries,omitempty"`
}

// BackupSpec lists which datasets the per-update snapshot should cover.
type BackupSpec struct {
	Datasets []string `yaml:"datasets,omitempty" json:"datasets,omitempty"`
	Strategy string   `yaml:"strategy,omitempty" json:"strategy,omitempty"`
}

// ConfigTemplate is a config file the installer renders from the
// manifest's templated source and bind-mounts at mountpoint.
type ConfigTemplate struct {
	Source     string `yaml:"source" json:"source"`
	Mountpoint string `yaml:"mountpoint" json:"mountpoint"`
}

// ParseManifest decodes raw YAML into a Manifest. It rejects unknown
// fields so a typo in a hand-authored manifest fails loudly rather than
// being silently dropped at install (DESIGN_PRINCIPLES priority #8 明示的
// > 暗黙的). Caller must follow with Validate to enforce the structural
// invariants the YAML decoder cannot express.
func ParseManifest(raw []byte) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("app: parse manifest: %w", err)
	}
	return &m, nil
}

// ValidationError aggregates every validator finding so the operator sees
// the whole list, not just the first. Each Issue carries a path that
// resolves to the manifest YAML path that failed.
type ValidationError struct {
	Issues []ValidationIssue
}

// ValidationIssue is one validator finding.
type ValidationIssue struct {
	Path    string
	Message string
}

// Error implements error. The render is "<path>: <message>; <path>: <message>"
// to keep CLI output single-line-ish but greppable.
func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, i := range e.Issues {
		parts = append(parts, fmt.Sprintf("%s: %s", i.Path, i.Message))
	}
	return "manifest validation failed: " + strings.Join(parts, "; ")
}

// Is lets callers detect ValidationError via errors.Is.
func (e *ValidationError) Is(target error) bool {
	_, ok := target.(*ValidationError)
	return ok
}

// ErrManifestEmpty signals a manifest with no parseable content.
var ErrManifestEmpty = errors.New("app: manifest is empty")

// Validate enforces the structural invariants the YAML decoder cannot.
// Returns *ValidationError when one or more issues are found, nil when
// the manifest is acceptable.
func Validate(m *Manifest) error {
	if m == nil {
		return ErrManifestEmpty
	}
	v := &validator{m: m}
	v.requireString("api_version", m.APIVersion, "v1")
	v.requireString("name", m.Name, "")
	v.requireString("version", m.Version, "")
	v.requireAppType("type", m.Type)
	v.requireContainers("containers", m.Containers)
	v.requireRouting("routing", m.Routing, m.Containers)
	v.requireAuth("auth", m.Auth)
	v.checkSettings("settings", m.Settings)
	v.checkSetup("setup", m.Setup)
	v.checkVolumes("containers", m.Containers, m.Storage.Datasets, m.Shares)
	v.checkDatasets("storage.datasets", m.Storage.Datasets)
	v.checkHealth("health", m.Health, m.Containers)
	v.checkBackup("backup", m.Backup, m.Storage.Datasets)
	if len(v.issues) > 0 {
		return &ValidationError{Issues: v.issues}
	}
	return nil
}

type validator struct {
	m      *Manifest
	issues []ValidationIssue
}

func (v *validator) issue(path, msg string) {
	v.issues = append(v.issues, ValidationIssue{Path: path, Message: msg})
}

func (v *validator) requireString(path, value, want string) {
	if value == "" {
		v.issue(path, "is required")
		return
	}
	if want != "" && value != want {
		v.issue(path, fmt.Sprintf("expected %q, got %q", want, value))
	}
}

func (v *validator) requireAppType(path string, t AppType) {
	if t == "" {
		v.issue(path, "is required (native|legacy)")
		return
	}
	if t != AppTypeNative && t != AppTypeLegacy {
		v.issue(path, fmt.Sprintf("unknown app type %q (want native|legacy)", t))
	}
}

func (v *validator) requireContainers(path string, cs map[string]Container) {
	if len(cs) == 0 {
		v.issue(path, "at least one container is required")
		return
	}
	for name, c := range cs {
		base := path + "." + name
		if c.Image == "" {
			v.issue(base+".image", "is required")
		}
		for _, dep := range c.DependsOn {
			if _, ok := cs[dep]; !ok {
				v.issue(base+".depends_on", fmt.Sprintf("references unknown container %q", dep))
			}
		}
	}
}

func (v *validator) requireRouting(path string, r Routing, cs map[string]Container) {
	switch r.Mode {
	case "":
		v.issue(path+".mode", "is required (path|port|subdomain)")
		return
	case RoutingModePath:
		if r.BasePathEnv == "" && !r.StripPrefix {
			v.issue(path, "mode=path requires either base_path_env or strip_prefix=true")
		}
	case RoutingModePort:
		if r.Port == nil {
			v.issue(path+".port", "mode=port requires routing.port")
		}
	case RoutingModeSubdomain:
		if r.Subdomain == "" {
			v.issue(path+".subdomain", "mode=subdomain requires routing.subdomain")
		}
	default:
		v.issue(path+".mode", fmt.Sprintf("unknown routing mode %q (want path|port|subdomain)", r.Mode))
		return
	}
	if r.Container != "" {
		if _, ok := cs[r.Container]; !ok {
			v.issue(path+".container", fmt.Sprintf("references unknown container %q", r.Container))
		}
	}
}

func (v *validator) requireAuth(path string, a Auth) {
	switch a.Mode {
	case "":
		v.issue(path+".mode", "is required (oidc|forward_auth|none)")
	case AuthModeOIDC, AuthModeNone:
		// no extra fields required
	case AuthModeForwardAuth:
		if a.HeaderUser == "" {
			v.issue(path+".header_user", "mode=forward_auth requires header_user")
		}
	default:
		v.issue(path+".mode", fmt.Sprintf("unknown auth mode %q", a.Mode))
	}
}

func (v *validator) checkSettings(path string, ss []Setting) {
	seen := map[string]bool{}
	for i, s := range ss {
		base := fmt.Sprintf("%s[%d]", path, i)
		if s.Key == "" {
			v.issue(base+".key", "is required")
			continue
		}
		if seen[s.Key] {
			v.issue(base+".key", fmt.Sprintf("duplicate setting key %q", s.Key))
		}
		seen[s.Key] = true
		switch s.Type {
		case SettingSecret, SettingString, SettingNumber, SettingBool:
		case "":
			v.issue(base+".type", "is required")
		default:
			v.issue(base+".type", fmt.Sprintf("unknown setting type %q", s.Type))
		}
	}
}

func (v *validator) checkSetup(path string, s SetupBlock) {
	for i, f := range s.Required {
		v.checkSetupField(fmt.Sprintf("%s.required[%d]", path, i), f)
	}
	for i, f := range s.Optional {
		v.checkSetupField(fmt.Sprintf("%s.optional[%d]", path, i), f)
	}
}

func (v *validator) checkSetupField(path string, f SetupField) {
	if f.Key == "" {
		v.issue(path+".key", "is required")
	}
	switch f.Type {
	case SetupSharePicker, SetupSelect, SetupToggle, SetupText:
	case "":
		v.issue(path+".type", "is required")
	default:
		v.issue(path+".type", fmt.Sprintf("unknown setup type %q", f.Type))
	}
	if f.Type == SetupSelect && len(f.Options) == 0 {
		v.issue(path+".options", "type=select requires options")
	}
}

func (v *validator) checkVolumes(path string, cs map[string]Container, datasets []Dataset, shares []ShareMount) {
	dsByName := map[string]bool{}
	for _, d := range datasets {
		dsByName[d.Name] = true
	}
	shByName := map[string]bool{}
	for _, sh := range shares {
		shByName[sh.Name] = true
	}
	for cname, c := range cs {
		for i, vol := range c.Volumes {
			base := fmt.Sprintf("%s.%s.volumes[%d]", path, cname, i)
			if vol.Mountpoint == "" {
				v.issue(base+".mountpoint", "is required")
			}
			switch vol.Type {
			case VolumeKindDataset:
				if vol.Name == "" {
					v.issue(base+".name", "type=dataset requires name")
				} else if !dsByName[vol.Name] {
					v.issue(base+".name", fmt.Sprintf("references unknown dataset %q", vol.Name))
				}
			case VolumeKindShare:
				if vol.Name == "" {
					v.issue(base+".name", "type=share requires name")
				} else if !shByName[vol.Name] {
					v.issue(base+".name", fmt.Sprintf("references unknown share %q", vol.Name))
				}
			case VolumeKindBind:
				if vol.Source == "" {
					v.issue(base+".source", "type=bind requires source path")
				}
			case "":
				v.issue(base+".type", "is required")
			default:
				v.issue(base+".type", fmt.Sprintf("unknown volume type %q", vol.Type))
			}
		}
	}
}

func (v *validator) checkDatasets(path string, ds []Dataset) {
	seen := map[string]bool{}
	for i, d := range ds {
		base := fmt.Sprintf("%s[%d]", path, i)
		if d.Name == "" {
			v.issue(base+".name", "is required")
			continue
		}
		if seen[d.Name] {
			v.issue(base+".name", fmt.Sprintf("duplicate dataset %q", d.Name))
		}
		seen[d.Name] = true
	}
}

func (v *validator) checkHealth(path string, h *Health, cs map[string]Container) {
	if h == nil {
		return
	}
	if h.Container != "" {
		if _, ok := cs[h.Container]; !ok {
			v.issue(path+".container", fmt.Sprintf("references unknown container %q", h.Container))
		}
	}
}

func (v *validator) checkBackup(path string, b *BackupSpec, ds []Dataset) {
	if b == nil {
		return
	}
	dsByName := map[string]bool{}
	for _, d := range ds {
		dsByName[d.Name] = true
	}
	for i, name := range b.Datasets {
		if !dsByName[name] {
			v.issue(fmt.Sprintf("%s.datasets[%d]", path, i),
				fmt.Sprintf("references unknown dataset %q", name))
		}
	}
}
