package config

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRoundTrip_EmptyConfigPreservesEnvelope(t *testing.T) {
	// [AC-S464e47-2-1] empty SQLite -> empty config.json that re-parses back.
	cases := []struct {
		name string
		cfg  *Config
	}{
		{"empty", New()},
		{"nil_marshalled_as_empty", nil},
		{"all_sections_present_but_empty", &Config{
			SchemaVersion: Version,
			System:        &SystemConfig{},
			Storage:       &StorageConfig{},
			Shares:        &SharesConfig{},
			Users:         &UsersConfig{},
			Network:       &NetworkConfig{},
			Apps:          &AppsConfig{},
			Auth:          &AuthConfig{},
			Backup:        &BackupConfig{},
			Notifications: &NotificationsConfig{},
			Monitor:       &MonitorConfig{},
			Logging:       &LoggingConfig{},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := Marshal(c.cfg)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			parsed, err := Unmarshal(raw)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			raw2, err := Marshal(parsed)
			if err != nil {
				t.Fatalf("Marshal#2: %v", err)
			}
			if !reflect.DeepEqual(raw, raw2) {
				t.Fatalf("round-trip mismatch:\nfirst:\n%s\nsecond:\n%s", raw, raw2)
			}
		})
	}
}

func TestMarshal_AlwaysIncludesSchemaVersion(t *testing.T) {
	raw, err := Marshal(New())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"schema_version": 1`) {
		t.Fatalf("schema_version missing from output:\n%s", raw)
	}
}

func TestUnmarshal_RejectsUnknownTopLevelFields(t *testing.T) {
	raw := []byte(`{"schema_version": 1, "ghost_section": {}}`)
	_, err := Unmarshal(raw)
	if err == nil {
		t.Fatalf("Unmarshal should reject unknown field")
	}
	if !strings.Contains(err.Error(), "ghost_section") {
		t.Fatalf("error %v should mention the unknown field", err)
	}
}

func TestDiff_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		old     *Config
		new     *Config
		wantOps map[string]string // section -> expected op (add/remove/update/noop) — sections not listed must be absent
	}{
		{
			name:    "both_empty",
			old:     New(),
			new:     New(),
			wantOps: map[string]string{},
		},
		{
			name: "add_storage",
			old:  New(),
			new:  &Config{SchemaVersion: Version, Storage: &StorageConfig{}},
			wantOps: map[string]string{
				"storage": "add",
			},
		},
		{
			name: "remove_users",
			old:  &Config{SchemaVersion: Version, Users: &UsersConfig{}},
			new:  New(),
			wantOps: map[string]string{
				"users": "remove",
			},
		},
		{
			name: "noop_when_section_unchanged",
			old:  &Config{SchemaVersion: Version, Storage: &StorageConfig{}},
			new:  &Config{SchemaVersion: Version, Storage: &StorageConfig{}},
			wantOps: map[string]string{
				"storage": "noop",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Diff(tt.old, tt.new)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			gotOps := map[string]string{}
			for _, e := range plan.Entries {
				gotOps[e.Section] = e.Op
			}
			for sec, op := range tt.wantOps {
				if gotOps[sec] != op {
					t.Errorf("section %q: op = %q, want %q", sec, gotOps[sec], op)
				}
			}
			for sec := range gotOps {
				if _, want := tt.wantOps[sec]; !want {
					t.Errorf("unexpected section %q in plan (op %q)", sec, gotOps[sec])
				}
			}
		})
	}
}

func TestPlanEmpty(t *testing.T) {
	cases := []struct {
		name string
		p    Plan
		want bool
	}{
		{"no_entries", Plan{}, true},
		{"only_noop", Plan{Entries: []DiffEntry{{Section: "storage", Op: "noop"}}}, true},
		{"has_add", Plan{Entries: []DiffEntry{{Section: "storage", Op: "add"}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.Empty(); got != c.want {
				t.Fatalf("Empty() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFormatPlan_StableOrder(t *testing.T) {
	p := Plan{Entries: []DiffEntry{
		{Section: "storage", Op: "add"},
		{Section: "apps", Op: "remove"},
		{Section: "users", Op: "noop"},
	}}
	out := FormatPlan(p)
	want := "remove  apps\nadd     storage\nnoop    users\n"
	if out != want {
		t.Fatalf("FormatPlan() = %q, want %q", out, want)
	}
}

func TestApplyRegistry_RegisterAndDispatch(t *testing.T) {
	resetRegistryForTest()
	defer resetRegistryForTest()

	fake := &fakeAdapter{section: "storage"}
	Register(fake)

	got := Adapters()
	if len(got) != 1 || got[0].Section() != "storage" {
		t.Fatalf("Adapters() = %#v, want one storage adapter", got)
	}

	if _, err := got[0].Plan(context.Background(), New(), New()); err != nil {
		t.Fatalf("adapter Plan: %v", err)
	}
	if !fake.planCalled {
		t.Errorf("expected Plan to be called on the registered adapter")
	}
}

func TestApplyRegistry_DuplicateRegistrationPanics(t *testing.T) {
	resetRegistryForTest()
	defer resetRegistryForTest()

	Register(&fakeAdapter{section: "storage"})
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on duplicate Register")
		}
	}()
	Register(&fakeAdapter{section: "storage"})
}

func TestApplyRegistry_NilOrEmptyAdapterPanics(t *testing.T) {
	resetRegistryForTest()
	defer resetRegistryForTest()

	cases := []struct {
		name string
		fn   func()
	}{
		{"nil", func() { Register(nil) }},
		{"empty_section", func() { Register(&fakeAdapter{section: ""}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic")
				}
			}()
			c.fn()
		})
	}
}

func TestUnmarshal_ProducesValidJSONOutput(t *testing.T) {
	raw, err := Marshal(New())
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if generic["schema_version"] != float64(1) {
		t.Fatalf("schema_version = %v, want 1", generic["schema_version"])
	}
}

type fakeAdapter struct {
	section    string
	planCalled bool
}

func (f *fakeAdapter) Section() string { return f.section }
func (f *fakeAdapter) Plan(_ context.Context, _, _ *Config) ([]Step, error) {
	f.planCalled = true
	return nil, nil
}
func (f *fakeAdapter) Apply(_ context.Context, _ []Step) error { return nil }
