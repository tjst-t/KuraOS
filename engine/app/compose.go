package app

import (
	"bytes"
	"fmt"
	"sort"
	"text/template"

	"gopkg.in/yaml.v3"
)

// ComposeProject is the in-memory shape of a docker-compose v3 project that
// engine/app generates from a Manifest plus the install-time inputs (resolved
// dataset paths, share mounts, port reservations, secret values). We emit it
// as YAML for audit/operator readability and as ContainerSpec list for the
// DockerClient.
//
// Field names follow compose-spec snake_case.
type ComposeProject struct {
	Version  string                     `yaml:"version,omitempty"`
	Name     string                     `yaml:"name,omitempty"`
	Services map[string]ComposeService  `yaml:"services"`
	Networks map[string]ComposeNetEntry `yaml:"networks,omitempty"`
}

// ComposeService is one entry of services:.
type ComposeService struct {
	Image       string            `yaml:"image"`
	Ports       []string          `yaml:"ports,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty"`
	DependsOn   []string          `yaml:"depends_on,omitempty"`
	Restart     string            `yaml:"restart,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Networks    []string          `yaml:"networks,omitempty"`
	Healthcheck *ComposeHealth    `yaml:"healthcheck,omitempty"`
	Command     []string          `yaml:"command,omitempty"`
	Entrypoint  []string          `yaml:"entrypoint,omitempty"`
	WorkingDir  string            `yaml:"working_dir,omitempty"`
	ContainerNm string            `yaml:"container_name,omitempty"`
}

// ComposeHealth mirrors compose's healthcheck block.
type ComposeHealth struct {
	Test        []string `yaml:"test,omitempty"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
}

// ComposeNetEntry mirrors networks: <name>: { driver: bridge }.
type ComposeNetEntry struct {
	Driver string `yaml:"driver,omitempty"`
}

// InstallInputs aggregates everything compose generation + lifecycle needs
// beyond the Manifest itself. Built by the install pipeline once datasets,
// ports, secrets, share paths, and config templates have been resolved.
type InstallInputs struct {
	// AppID is the install-instance identifier (e.g. "immich.0001").
	AppID string

	// SharePaths maps manifest share name -> host path the operator picked
	// at install time (resolved from share_picker setup field).
	SharePaths map[string]string

	// DatasetPaths maps manifest dataset name -> host path the dataset is
	// mounted at (e.g. "/tank/apps/immich/db"). Provided by the planner +
	// engine/storage.CreateVolume.
	DatasetPaths map[string]string

	// PortBindings maps (container, manifest_port) -> host_port. From the
	// PortAllocator.
	PortBindings map[PortKey]int

	// Secrets maps setting key -> resolved plaintext value (vault-stored
	// for type=secret, raw default or operator input for non-secret types).
	Secrets map[string]string

	// ConfigOutputs maps the manifest configs[].mountpoint -> the rendered
	// config file's host path (the installer wrote the rendered template
	// to disk before invoking compose).
	ConfigOutputs map[string]string

	// NetworkName overrides the default per-app bridge network name.
	NetworkName string

	// BasePath is the gateway-relative prefix where the app is mounted
	// (e.g. "/apps/filebrowser"). Empty for non-path routing modes.
	// Combined with manifest.Routing.BasePathEnv it becomes an env var
	// the app reads to render correct asset / API URLs in its served
	// HTML — required for SPA frontends behind a path-prefix proxy.
	BasePath string
}

// PortKey identifies one port-binding lookup target.
type PortKey struct {
	Container    string
	ManifestPort int
}

// BuildCompose renders the manifest + inputs into a ComposeProject. Errors
// arise from missing inputs (a manifest references a share/dataset/port that
// InstallInputs did not resolve).
func BuildCompose(m *Manifest, in InstallInputs) (*ComposeProject, error) {
	if m == nil {
		return nil, ErrManifestEmpty
	}
	if in.AppID == "" {
		return nil, fmt.Errorf("app: compose: AppID required")
	}
	netName := in.NetworkName
	if netName == "" {
		netName = "kura-" + m.Name
	}
	proj := &ComposeProject{
		Version:  "3.8",
		Name:     "kura-" + m.Name,
		Services: map[string]ComposeService{},
		Networks: map[string]ComposeNetEntry{
			netName: {Driver: "bridge"},
		},
	}
	// stable iteration order — important for the audit YAML being diffable.
	names := containerNames(m.Containers)
	for _, cname := range names {
		c := m.Containers[cname]
		svc := ComposeService{
			Image:       c.Image,
			Restart:     c.Restart,
			DependsOn:   append([]string{}, c.DependsOn...),
			Networks:    []string{netName},
			Command:     append([]string{}, c.Command...),
			Entrypoint:  append([]string{}, c.Entrypoint...),
			WorkingDir:  c.WorkingDir,
			ContainerNm: containerName(in.AppID, cname),
			Labels: map[string]string{
				"kuraos.app":       m.Name,
				"kuraos.app_id":    in.AppID,
				"kuraos.container": cname,
			},
		}
		if svc.Restart == "" {
			svc.Restart = "unless-stopped"
		}
		// Env: substitute settings via Go templates (.KuraOS.Settings.<key>).
		env, err := renderEnv(c.Env, in)
		if err != nil {
			return nil, fmt.Errorf("app: compose: %s.env: %w", cname, err)
		}
		// Inject base_path_env for the routing target container so
		// SPA frontends behind a path-prefix proxy (e.g. filebrowser
		// FB_BASEURL) emit correct asset / API URLs. Only applies
		// when routing.mode=path AND routing.base_path_env is set
		// AND this container is the routing target.
		if m.Routing.BasePathEnv != "" && in.BasePath != "" && cname == routingTarget(m) {
			if env == nil {
				env = map[string]string{}
			}
			env[m.Routing.BasePathEnv] = in.BasePath
		}
		svc.Environment = env

		// Ports.
		for _, raw := range c.Ports {
			cp, err := parseManifestPort(raw)
			if err != nil {
				return nil, fmt.Errorf("app: compose: %s.ports: %w", cname, err)
			}
			host, ok := in.PortBindings[PortKey{Container: cname, ManifestPort: cp}]
			if !ok {
				return nil, fmt.Errorf("app: compose: no port binding for %s:%d", cname, cp)
			}
			svc.Ports = append(svc.Ports, fmt.Sprintf("%d:%d", host, cp))
		}

		// Volumes — type=dataset / type=share / type=bind; configs land here too.
		for _, vol := range c.Volumes {
			vstr, err := composeVolumeString(vol, in)
			if err != nil {
				return nil, fmt.Errorf("app: compose: %s.volumes: %w", cname, err)
			}
			svc.Volumes = append(svc.Volumes, vstr)
		}
		// Top-level shares attach as bind mounts on the named container.
		for _, sh := range m.Shares {
			if sh.Container != "" && sh.Container != cname {
				continue
			}
			host, ok := in.SharePaths[sh.Name]
			if !ok {
				return nil, fmt.Errorf("app: compose: no share path for %q", sh.Name)
			}
			ro := ""
			if sh.Access == "readonly" {
				ro = ":ro"
			}
			svc.Volumes = append(svc.Volumes, fmt.Sprintf("%s:%s%s", host, sh.Mountpoint, ro))
		}
		// Configs (rendered templates) — bind file into container.
		for _, cfg := range m.Configs {
			host, ok := in.ConfigOutputs[cfg.Source]
			if !ok {
				continue
			}
			svc.Volumes = append(svc.Volumes, fmt.Sprintf("%s:%s:ro", host, cfg.Mountpoint))
		}

		if c.Healthcheck != nil {
			svc.Healthcheck = &ComposeHealth{
				Test:        append([]string{}, c.Healthcheck.Test...),
				Interval:    c.Healthcheck.Interval,
				Timeout:     c.Healthcheck.Timeout,
				Retries:     c.Healthcheck.Retries,
				StartPeriod: "",
			}
		}
		proj.Services[cname] = svc
	}
	return proj, nil
}

// MarshalCompose returns the YAML form. Indented 2 spaces, deterministic key
// order (yaml.v3 maintains field declaration order, and our maps go through a
// helper that sorts).
func MarshalCompose(p *ComposeProject) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("app: marshal compose: nil project")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("app: marshal compose: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("app: marshal compose close: %w", err)
	}
	return buf.Bytes(), nil
}

// composeVolumeString turns one ContainerVolume + InstallInputs into the
// "src:dst[:ro]" string compose expects.
func composeVolumeString(vol ContainerVolume, in InstallInputs) (string, error) {
	target := vol.Mountpoint
	ro := ""
	if vol.ReadOnly {
		ro = ":ro"
	}
	switch vol.Type {
	case VolumeKindDataset:
		host, ok := in.DatasetPaths[vol.Name]
		if !ok {
			return "", fmt.Errorf("dataset %q not provisioned", vol.Name)
		}
		return fmt.Sprintf("%s:%s%s", host, target, ro), nil
	case VolumeKindShare:
		host, ok := in.SharePaths[vol.Name]
		if !ok {
			return "", fmt.Errorf("share %q not provisioned", vol.Name)
		}
		return fmt.Sprintf("%s:%s%s", host, target, ro), nil
	case VolumeKindBind:
		if vol.Source == "" {
			return "", fmt.Errorf("bind volume requires source")
		}
		return fmt.Sprintf("%s:%s%s", vol.Source, target, ro), nil
	default:
		return "", fmt.Errorf("unknown volume type %q", vol.Type)
	}
}

// renderEnv substitutes {{ .KuraOS.Settings.X }} in the env value strings,
// returning the fully-resolved env map.
func renderEnv(env map[string]string, in InstallInputs) (map[string]string, error) {
	if len(env) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		expanded, err := renderTemplate(v, in)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		out[k] = expanded
	}
	return out, nil
}

// renderTemplate runs s through a Go text/template with the .KuraOS namespace
// bound to InstallInputs-derived data. Two reasons we use Go templates:
//  1. design.md §7.4 explicitly references Go-style {{ .KuraOS.Datasets.db.Path }}
//  2. legacy ${var} substitution via os.Expand would collide with shell-style
//     env files apps embed in their own configs — keeping the namespace
//     prefixed avoids accidental hits.
//
// We also support the convenience syntax ${name} (legacy compose env-file
// style) by expanding it BEFORE template execution: ${db_password} ->
// {{ .KuraOS.Settings.db_password }}. This keeps the immich.yaml fixture
// (which uses ${db_password}) working without rewriting it.
func renderTemplate(s string, in InstallInputs) (string, error) {
	if s == "" {
		return "", nil
	}
	rewritten := rewriteShellSubst(s)
	tmpl, err := template.New("env").Option("missingkey=error").Parse(rewritten)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	data := buildTemplateData(in)
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// rewriteShellSubst converts every legacy ${name} occurrence into the Go
// template form {{ .KuraOS.Settings.<name> }}. Names containing only
// [A-Za-z0-9_] are accepted; anything else is left intact (so docker compose's
// own ${VAR:-default} idiom would not be touched, but we don't generate it).
func rewriteShellSubst(s string) string {
	var b bytes.Buffer
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			end := -1
			for j := i + 2; j < len(s); j++ {
				if s[j] == '}' {
					end = j
					break
				}
			}
			if end > 0 {
				name := s[i+2 : end]
				if isSimpleIdent(name) {
					b.WriteString("{{ .KuraOS.Settings.")
					b.WriteString(name)
					b.WriteString(" }}")
					i = end
					continue
				}
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isSimpleIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// buildTemplateData exposes InstallInputs through the .KuraOS namespace.
type templateData struct {
	KuraOS templateKuraOS
}
type templateKuraOS struct {
	AppID    string
	Settings map[string]string
	Datasets map[string]templateDataset
	Shares   map[string]string
	Ports    map[string]int
}
type templateDataset struct {
	Path string
}

func buildTemplateData(in InstallInputs) templateData {
	ds := make(map[string]templateDataset, len(in.DatasetPaths))
	for k, v := range in.DatasetPaths {
		ds[k] = templateDataset{Path: v}
	}
	ports := make(map[string]int, len(in.PortBindings))
	for k, v := range in.PortBindings {
		ports[fmt.Sprintf("%s_%d", k.Container, k.ManifestPort)] = v
	}
	return templateData{KuraOS: templateKuraOS{
		AppID:    in.AppID,
		Settings: copyLabels(in.Secrets),
		Datasets: ds,
		Shares:   copyLabels(in.SharePaths),
		Ports:    ports,
	}}
}

// containerNames returns the manifest container names sorted so YAML output
// is deterministic.
func containerNames(cs map[string]Container) []string {
	out := make([]string, 0, len(cs))
	for n := range cs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// containerName joins app instance id + manifest container name into a
// docker container name. The "kura-" prefix lets operators spot KuraOS-managed
// containers in `docker ps`. AppID typically contains a dot ("immich.0001"),
// which docker disallows in container names — replace with '_'.
func containerName(appID, cname string) string {
	clean := make([]byte, 0, len(appID))
	for i := 0; i < len(appID); i++ {
		c := appID[i]
		if c == '.' || c == ':' || c == '/' {
			clean = append(clean, '_')
		} else {
			clean = append(clean, c)
		}
	}
	return "kura-" + string(clean) + "-" + cname
}

// parseManifestPort accepts either "<port>" or "<host>:<port>" (the manifest
// uses the latter as a hint for Compose, but we override host with the
// allocator's binding) and returns the container-side port.
func parseManifestPort(raw string) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty port")
	}
	// Strip optional "host:" prefix.
	colon := -1
	for i := 0; i < len(raw); i++ {
		if raw[i] == ':' {
			colon = i
			break
		}
	}
	value := raw
	if colon >= 0 {
		value = raw[colon+1:]
	}
	// Strip optional "/proto" suffix.
	slash := -1
	for i := 0; i < len(value); i++ {
		if value[i] == '/' {
			slash = i
			break
		}
	}
	if slash >= 0 {
		value = value[:slash]
	}
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
		return 0, fmt.Errorf("parse port %q: %w", raw, err)
	}
	if n <= 0 || n > 65535 {
		return 0, fmt.Errorf("port %d out of range", n)
	}
	return n, nil
}

// routingTarget returns the container name the gateway proxies to.
// Defaults to the explicit Routing.Container when set, otherwise the
// first container in the manifest (deterministic via map iteration
// being unordered is acceptable since most apps with a single
// container have only one entry).
func routingTarget(m *Manifest) string {
	if m.Routing.Container != "" {
		return m.Routing.Container
	}
	for n := range m.Containers {
		return n
	}
	return ""
}
