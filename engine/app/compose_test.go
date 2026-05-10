package app

import (
	"strings"
	"testing"
)

func TestBuildComposeMinimalManifest(t *testing.T) {
	m := &Manifest{
		APIVersion: "v1",
		Name:       "demo",
		Version:    "1.0",
		Type:       AppTypeNative,
		Containers: map[string]Container{
			"web": {Image: "nginx:1", Ports: []string{"8080:80"}},
		},
		Routing: Routing{Mode: RoutingModePath, StripPrefix: true, Container: "web"},
		Auth:    Auth{Mode: AuthModeNone},
	}
	if err := Validate(m); err != nil {
		t.Fatalf("validate: %v", err)
	}
	in := InstallInputs{
		AppID:        "demo.aaaa",
		PortBindings: map[PortKey]int{{Container: "web", ManifestPort: 80}: 49500},
	}
	proj, err := BuildCompose(m, in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	svc, ok := proj.Services["web"]
	if !ok {
		t.Fatal("missing web service")
	}
	if svc.Image != "nginx:1" {
		t.Fatalf("image %q", svc.Image)
	}
	if len(svc.Ports) != 1 || svc.Ports[0] != "49500:80" {
		t.Fatalf("ports %v", svc.Ports)
	}
	if svc.ContainerNm != "kura-demo_aaaa-web" {
		t.Fatalf("container_name %q", svc.ContainerNm)
	}
	yaml, err := MarshalCompose(proj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(yaml), "nginx:1") {
		t.Fatalf("yaml missing image: %s", yaml)
	}
}

func TestBuildComposeShellSubstitution(t *testing.T) {
	m := &Manifest{
		APIVersion: "v1",
		Name:       "x",
		Version:    "1",
		Type:       AppTypeNative,
		Containers: map[string]Container{
			"a": {Image: "img", Env: map[string]string{"DSN": "postgres://u:${db_password}@h/d"}},
		},
		Settings: []Setting{{Key: "db_password", Type: SettingSecret, Required: true}},
		Routing:  Routing{Mode: RoutingModePort, Port: intPtr(80)},
		Auth:     Auth{Mode: AuthModeNone},
	}
	if err := Validate(m); err != nil {
		t.Fatalf("validate: %v", err)
	}
	in := InstallInputs{
		AppID:        "x.aaaa",
		PortBindings: map[PortKey]int{},
		Secrets:      map[string]string{"db_password": "supersecret"},
	}
	proj, err := BuildCompose(m, in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := proj.Services["a"].Environment["DSN"]
	want := "postgres://u:supersecret@h/d"
	if got != want {
		t.Fatalf("DSN = %q; want %q", got, want)
	}
}

func intPtr(n int) *int { return &n }

// Regression for the dead-code base_path_env injection (2026-05-10):
// the field was parsed from the manifest and threaded through
// AppRoute, but no compose-level code actually wrote it into the
// container env. SPA frontends (filebrowser, immich) sat on a loading
// spinner because their JS asked for /static/... at root instead of
// /apps/<name>/static/.... A 200 response from the root URL hid the
// fault — only a real browser open would surface it.
//
// This test asserts the wiring exists: when manifest sets
// routing.base_path_env, the routing-target container's env contains
// that key with the gateway-relative prefix as value.
func TestBuildCompose_InjectsBasePathEnvOnRoutingTarget(t *testing.T) {
	m := &Manifest{
		APIVersion: "v1",
		Name:       "spa",
		Version:    "1",
		Type:       AppTypeLegacy,
		Containers: map[string]Container{
			"web":   {Image: "spa-frontend:1", Ports: []string{"80:80"}},
			"redis": {Image: "redis:7"},
		},
		Routing: Routing{
			Mode:        RoutingModePath,
			BasePathEnv: "MY_BASE_URL",
			Container:   "web",
		},
		Auth: Auth{Mode: AuthModeNone},
	}
	if err := Validate(m); err != nil {
		t.Fatalf("validate: %v", err)
	}
	in := InstallInputs{
		AppID:        "spa.beef00",
		BasePath:     "/apps/spa",
		PortBindings: map[PortKey]int{{Container: "web", ManifestPort: 80}: 49000},
	}
	proj, err := BuildCompose(m, in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Routing target gets the env injected.
	if got := proj.Services["web"].Environment["MY_BASE_URL"]; got != "/apps/spa" {
		t.Fatalf("web.MY_BASE_URL = %q, want /apps/spa", got)
	}
	// Non-target containers do NOT get it (avoids polluting redis,
	// db, etc. with frontend-only config).
	if _, ok := proj.Services["redis"].Environment["MY_BASE_URL"]; ok {
		t.Fatalf("redis must not receive base_path_env")
	}
}

// Counterpart: when base_path_env is empty, no injection happens.
func TestBuildCompose_NoInjectionWhenBasePathEnvEmpty(t *testing.T) {
	m := &Manifest{
		APIVersion: "v1",
		Name:       "plain",
		Version:    "1",
		Type:       AppTypeLegacy,
		Containers: map[string]Container{
			"web": {Image: "nginx:1", Ports: []string{"80:80"}},
		},
		Routing: Routing{Mode: RoutingModePath, StripPrefix: true, Container: "web"},
		Auth:    Auth{Mode: AuthModeNone},
	}
	in := InstallInputs{
		AppID:        "plain.aaaa",
		BasePath:     "/apps/plain",
		PortBindings: map[PortKey]int{{Container: "web", ManifestPort: 80}: 49000},
	}
	proj, _ := BuildCompose(m, in)
	for k := range proj.Services["web"].Environment {
		if strings.Contains(k, "BASE") {
			t.Fatalf("unexpected BASE-named env on web: %q", k)
		}
	}
}
