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
