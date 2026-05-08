package share

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// goldenUpdate, when true (-update), rewrites the testdata fixtures with
// whatever the current renderer produces. Used during template iteration.
var goldenUpdate = flag.Bool("update", false, "update golden files in testdata/")

func TestRenderSMBConfPerPreset(t *testing.T) {
	cases := []struct {
		name   string
		share  Share
		golden string
	}{
		{
			name: "general",
			share: Share{
				Name:       "photos",
				Path:       "/tank/photos",
				Protocol:   ProtocolSMB,
				Preset:     PresetGeneral,
				AccessMode: AccessReadWrite,
				ACL: []ACLEntry{
					{Kind: PrincipalUser, Name: "alice", Mode: ACLModeReadWrite},
					{Kind: PrincipalGroup, Name: "family", Mode: ACLModeRead},
				},
			},
			golden: "golden-smb-general.conf",
		},
		{
			name: "media",
			share: Share{
				Name:       "media",
				Path:       "/tank/media",
				Protocol:   ProtocolSMB,
				Preset:     PresetMedia,
				AccessMode: AccessReadWrite,
				ACL:        []ACLEntry{{Kind: PrincipalGroup, Name: "family", Mode: ACLModeReadWrite}},
			},
			golden: "golden-smb-media.conf",
		},
		{
			name: "time_machine",
			share: Share{
				Name:       "tm",
				Path:       "/tank/tm",
				Protocol:   ProtocolSMB,
				Preset:     PresetTimeMachine,
				AccessMode: AccessReadWrite,
				ACL:        []ACLEntry{{Kind: PrincipalUser, Name: "alice", Mode: ACLModeReadWrite}},
			},
			golden: "golden-smb-time_machine.conf",
		},
		{
			name: "database",
			share: Share{
				Name:       "db",
				Path:       "/tank/db",
				Protocol:   ProtocolSMB,
				Preset:     PresetDatabase,
				AccessMode: AccessReadWrite,
				ACL:        []ACLEntry{{Kind: PrincipalUser, Name: "service", Mode: ACLModeReadWrite}},
			},
			golden: "golden-smb-database.conf",
		},
	}
	m := newTestManager(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, _, err := m.render([]Share{c.share})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			compareGolden(t, c.golden, body)
		})
	}
}

func TestRenderSMBConfMandatoryPerformanceDefaults(t *testing.T) {
	// AC-Sd64f38-1-2: smb.conf に server multi channel support / use sendfile /
	// fruit:* の推奨設定が含まれている — verify against [global] for every
	// non-empty render.
	m := newTestManager(t)
	share := Share{
		Name:       "any",
		Path:       "/tank/any",
		Protocol:   ProtocolSMB,
		Preset:     PresetMedia,
		AccessMode: AccessReadWrite,
	}
	body, _, err := m.render([]Share{share})
	if err != nil {
		t.Fatal(err)
	}
	mustContain := []string{
		"server multi channel support = yes",
		"use sendfile = yes",
		"aio read size = 1",
		"aio write size = 1",
		"server min protocol = SMB2",
		"smb encrypt = desired",
		"fruit:metadata = stream",
		"vfs objects = catia fruit streams_xattr",
	}
	for _, s := range mustContain {
		if !contains(body, s) {
			t.Errorf("rendered smb.conf missing %q\n--- body ---\n%s", s, string(body))
		}
	}
}

func TestRenderNFSExports(t *testing.T) {
	m := newTestManager(t)
	shares := []Share{
		{
			Name:       "media",
			Path:       "/tank/media",
			Protocol:   ProtocolNFS,
			Preset:     PresetMedia,
			AccessMode: AccessReadWrite,
		},
		{
			Name:       "archive",
			Path:       "/tank/archive",
			Protocol:   ProtocolNFS,
			Preset:     PresetGeneral,
			AccessMode: AccessReadOnly,
		},
	}
	_, exports, err := m.render(shares)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	compareGolden(t, "golden-nfs.exports", exports)
}

func TestRenderBothProtocol(t *testing.T) {
	m := newTestManager(t)
	share := Share{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   ProtocolBoth,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
		ACL:        []ACLEntry{{Kind: PrincipalUser, Name: "alice", Mode: ACLModeReadWrite}},
	}
	smb, nfs, err := m.render([]Share{share})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(smb, "[photos]") {
		t.Errorf("expected SMB section [photos], not in body:\n%s", smb)
	}
	if !contains(nfs, "/tank/photos") {
		t.Errorf("expected /tank/photos in exports, not in body:\n%s", nfs)
	}
}

func TestRenderDisabledShareSkipped(t *testing.T) {
	m := newTestManager(t)
	shares := []Share{
		{Name: "active", Path: "/tank/a", Protocol: ProtocolSMB, Preset: PresetGeneral, AccessMode: AccessReadWrite},
		{Name: "inactive", Path: "/tank/b", Protocol: ProtocolSMB, Preset: PresetGeneral, AccessMode: AccessReadWrite, Disabled: true},
	}
	body, _, err := m.render(shares)
	if err != nil {
		t.Fatal(err)
	}
	if contains(body, "[inactive]") {
		t.Error("disabled share leaked into rendered smb.conf")
	}
	if !contains(body, "[active]") {
		t.Error("active share missing from rendered smb.conf")
	}
}

// newTestManager builds a Manager with no SQLite, exposing render() through
// the package-private field. Tests that exercise the persistence layer use
// the dedicated store_test.go.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return &Manager{}
}

func contains(b []byte, s string) bool {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return true
		}
	}
	return false
}

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *goldenUpdate {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch %s\n--- got ---\n%s\n--- want ---\n%s", name, string(got), string(want))
	}
}
