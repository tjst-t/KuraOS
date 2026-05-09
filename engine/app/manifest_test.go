package app

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestParseImmichManifest covers AC-S1bccf5-1-1: the canonical immich
// sample from design.md §7.2 must round-trip into Manifest with all
// sections populated.
func TestParseImmichManifest(t *testing.T) {
	// [AC-S1bccf5-1-1]
	raw, err := os.ReadFile("testdata/immich.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	m, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Validate(m); err != nil {
		t.Fatalf("validate: %v", err)
	}

	if m.Name != "immich" {
		t.Errorf("name = %q, want immich", m.Name)
	}
	if m.Version != "1.111.0" {
		t.Errorf("version = %q, want 1.111.0", m.Version)
	}
	if m.Type != AppTypeLegacy {
		t.Errorf("type = %q, want legacy", m.Type)
	}
	if got := m.DisplayName["ja"]; got != "Immich" {
		t.Errorf("display_name.ja = %q", got)
	}
	if len(m.Containers) != 3 {
		t.Errorf("containers = %d, want 3", len(m.Containers))
	}
	if got := m.Containers["server"].Image; got != "ghcr.io/immich-app/immich-server:v1.111.0" {
		t.Errorf("server image = %q", got)
	}
	if got := m.Containers["db"].Volumes[0].Name; got != "db" {
		t.Errorf("db volume[0].name = %q", got)
	}
	if got := m.Containers["db"].Volumes[0].Type; got != VolumeKindDataset {
		t.Errorf("db volume[0].type = %q", got)
	}
	if got := m.Storage.Datasets[0].PoolHint; got != PoolHintSSD {
		t.Errorf("dataset[0].pool_hint = %q", got)
	}
	if !m.Storage.Datasets[0].Backup {
		t.Errorf("dataset[0].backup = false, want true")
	}
	if m.Routing.Mode != RoutingModePath {
		t.Errorf("routing.mode = %q", m.Routing.Mode)
	}
	if m.Routing.BasePathEnv != "IMMICH_BASE_URL" {
		t.Errorf("routing.base_path_env = %q", m.Routing.BasePathEnv)
	}
	if !m.Routing.StripPrefix {
		t.Errorf("routing.strip_prefix = false, want true")
	}
	if m.Auth.Mode != AuthModeForwardAuth {
		t.Errorf("auth.mode = %q", m.Auth.Mode)
	}
	if m.Auth.HeaderUser != "X-Forwarded-User" {
		t.Errorf("auth.header_user = %q", m.Auth.HeaderUser)
	}
	if got := m.Setup.Required[0].Type; got != SetupSharePicker {
		t.Errorf("setup.required[0].type = %q", got)
	}
	if got := m.Settings[0].Type; got != SettingSecret {
		t.Errorf("settings[0].type = %q", got)
	}
	if got := m.Health.Endpoint; got != "/api/server/ping" {
		t.Errorf("health.endpoint = %q", got)
	}
	if got := m.Backup.Datasets[0]; got != "db" {
		t.Errorf("backup.datasets[0] = %q", got)
	}
}

// TestValidateBadManifests covers AC-S1bccf5-1-2: missing required
// fields, unknown enum values, and incompatible routing combinations
// must produce ValidationError.
func TestValidateBadManifests(t *testing.T) {
	// [AC-S1bccf5-1-2]
	cases := []struct {
		name    string
		yaml    string
		wantSub string // substring expected in the joined error message
	}{
		{
			name: "missing name and version",
			yaml: `
apiVersion: v1
type: native
containers:
  app:
    image: nginx
routing:
  mode: subdomain
  subdomain: app
auth:
  mode: none
`,
			wantSub: "name: is required",
		},
		{
			name: "unknown app type",
			yaml: `
apiVersion: v1
name: x
version: "1"
type: weird
containers:
  app:
    image: nginx
routing:
  mode: subdomain
  subdomain: x
auth:
  mode: none
`,
			wantSub: "unknown app type",
		},
		{
			name: "routing mode=path without base_path_env or strip_prefix",
			yaml: `
apiVersion: v1
name: x
version: "1"
type: legacy
containers:
  app:
    image: nginx
routing:
  mode: path
auth:
  mode: forward_auth
  header_user: X-Forwarded-User
`,
			wantSub: "mode=path requires either base_path_env",
		},
		{
			name: "routing mode=port without port number",
			yaml: `
apiVersion: v1
name: x
version: "1"
type: legacy
containers:
  app:
    image: nginx
routing:
  mode: port
auth:
  mode: forward_auth
  header_user: X-Forwarded-User
`,
			wantSub: "mode=port requires routing.port",
		},
		{
			name: "auth forward_auth without header_user",
			yaml: `
apiVersion: v1
name: x
version: "1"
type: legacy
containers:
  app:
    image: nginx
routing:
  mode: subdomain
  subdomain: x
auth:
  mode: forward_auth
`,
			wantSub: "header_user",
		},
		{
			name: "container depends_on unknown service",
			yaml: `
apiVersion: v1
name: x
version: "1"
type: legacy
containers:
  app:
    image: nginx
    depends_on: [ghost]
routing:
  mode: subdomain
  subdomain: x
auth:
  mode: none
`,
			wantSub: `references unknown container "ghost"`,
		},
		{
			name: "container volume references unknown dataset",
			yaml: `
apiVersion: v1
name: x
version: "1"
type: legacy
containers:
  app:
    image: nginx
    volumes:
      - type: dataset
        name: phantom
        mountpoint: /data
routing:
  mode: subdomain
  subdomain: x
auth:
  mode: none
`,
			wantSub: `references unknown dataset "phantom"`,
		},
		{
			name: "duplicate dataset name",
			yaml: `
apiVersion: v1
name: x
version: "1"
type: legacy
containers:
  app:
    image: nginx
storage:
  datasets:
    - name: db
    - name: db
routing:
  mode: subdomain
  subdomain: x
auth:
  mode: none
`,
			wantSub: `duplicate dataset "db"`,
		},
		{
			name: "setup select without options",
			yaml: `
apiVersion: v1
name: x
version: "1"
type: legacy
containers:
  app:
    image: nginx
setup:
  required:
    - key: quality
      type: select
      label: {ja: q, en: q}
routing:
  mode: subdomain
  subdomain: x
auth:
  mode: none
`,
			wantSub: "type=select requires options",
		},
		{
			name: "unknown top-level field",
			yaml: `
apiVersion: v1
name: x
version: "1"
type: legacy
containers:
  app:
    image: nginx
routing:
  mode: subdomain
  subdomain: x
auth:
  mode: none
mysteryField: foo
`,
			wantSub: "field mysteryField",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, parseErr := ParseManifest([]byte(tc.yaml))
			var validateErr error
			if parseErr == nil {
				validateErr = Validate(m)
			}
			if parseErr == nil && validateErr == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			joined := ""
			if parseErr != nil {
				joined = parseErr.Error()
			}
			if validateErr != nil {
				joined += "; " + validateErr.Error()
			}
			if !strings.Contains(joined, tc.wantSub) {
				t.Errorf("error %q does not contain %q", joined, tc.wantSub)
			}
			// Ensure ValidationError detection works via errors.Is for
			// the validator-side errors.
			if validateErr != nil {
				var ve *ValidationError
				if !errors.As(validateErr, &ve) {
					t.Errorf("validateErr is not *ValidationError: %T", validateErr)
				}
			}
		})
	}
}

// TestValidateNilManifestReturnsEmptyError ensures a nil manifest is
// rejected up-front rather than panicking inside the validator.
func TestValidateNilManifestReturnsEmptyError(t *testing.T) {
	if err := Validate(nil); !errors.Is(err, ErrManifestEmpty) {
		t.Errorf("Validate(nil) = %v, want ErrManifestEmpty", err)
	}
}
