package network_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/engine/network"
	"github.com/kuraos-org/kura/internal/cmdexec"
)

// [AC-Sf92666-2-1] GenerateNetplan produces valid YAML with expected fields.
func TestGenerateNetplan(t *testing.T) {
	cfg := network.NetplanConfig{
		Hostname: "nas.local",
		Interfaces: map[string]network.InterfaceConfig{
			"eth0": {
				Addresses: []string{"192.168.1.10/24"},
				Gateway4:  "192.168.1.1",
			},
		},
		DNSServers: []string{"8.8.8.8", "1.1.1.1"},
	}

	data, err := network.GenerateNetplan(cfg)
	if err != nil {
		t.Fatalf("GenerateNetplan: %v", err)
	}
	yml := string(data)

	mustContain := []string{
		"version: 2",
		"eth0",
		"192.168.1.10/24",
		"192.168.1.1",
		"8.8.8.8",
	}
	for _, s := range mustContain {
		if !strings.Contains(yml, s) {
			t.Errorf("netplan YAML missing %q:\n%s", s, yml)
		}
	}
}

// [AC-Sf92666-2-1] ApplyConfig in dry-run mode (no KURA_NETWORK_APPLY) does
// not call any executor command.
func TestApplyConfig_DryRun(t *testing.T) {
	t.Setenv("KURA_NETWORK_APPLY", "")
	fake := cmdexec.NewFake()
	// Do NOT register any commands — if any are called, Run will return error.
	m := network.NewManager(fake, t.TempDir())
	cfg := network.NetplanConfig{
		Hostname: "test.local",
	}
	if err := m.ApplyConfig(context.Background(), cfg); err != nil {
		t.Fatalf("ApplyConfig dry-run: %v", err)
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("expected 0 executor calls in dry-run mode, got %v", fake.Calls())
	}
}

// [AC-Sf92666-2-1] ApplyConfig with KURA_NETWORK_APPLY=1 calls hostnamectl
// and netplan apply when hostname changed.
func TestApplyConfig_Real(t *testing.T) {
	t.Setenv("KURA_NETWORK_APPLY", "1")
	dir := t.TempDir()

	fake := cmdexec.NewFake()
	// Register both expected commands.
	fake.Register("hostnamectl", []string{"set-hostname", "definitely-unique-TestApplyConfig_Real"},
		cmdexec.FakeResponse{})
	fake.Register("netplan", []string{"apply"}, cmdexec.FakeResponse{})

	m := network.NewManager(fake, dir)
	cfg := network.NetplanConfig{
		Hostname: "definitely-unique-TestApplyConfig_Real",
		Interfaces: map[string]network.InterfaceConfig{
			"eth0": {Addresses: []string{"10.0.0.1/24"}},
		},
	}
	if err := m.ApplyConfig(context.Background(), cfg); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}

	calls := fake.Calls()
	found := map[string]bool{}
	for _, c := range calls {
		found[c.Name] = true
	}
	if !found["hostnamectl"] {
		t.Errorf("expected hostnamectl call, got %v", calls)
	}
	if !found["netplan"] {
		t.Errorf("expected netplan call, got %v", calls)
	}
}
