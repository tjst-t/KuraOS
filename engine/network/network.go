// Package network implements network configuration management for KuraOS.
//
// engine/network owns writing /etc/netplan/*.yaml and calling hostnamectl.
// No other package writes netplan directly (DESIGN_PRINCIPLES priority #10:
// SSOT projection — one engine owns each config file).
//
// Writes are gated behind KURA_NETWORK_APPLY=1 so tests and dry-run callers
// don't actually reconfigure the VM's network. The Engine itself always
// generates the YAML (testable without the flag) but only calls `netplan apply`
// and `hostnamectl` when the flag is set.
//
// DESIGN_PRINCIPLES priority #9: Executor interface lets tests mock the shell.
// DESIGN_PRINCIPLES priority #1: config.json carries declarative state only.
package network

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuraos-org/kura/internal/cmdexec"
	"gopkg.in/yaml.v3"
)

// NetworkInfo holds the current network state for display in the UI.
type NetworkInfo struct {
	Hostname   string
	Interfaces []InterfaceInfo
}

// InterfaceInfo holds info about one NIC.
type InterfaceInfo struct {
	Name      string
	Addresses []string
	Gateway   string
}

// NetplanConfig is the declarative network config stored in config.json
// under network.netplan.
type NetplanConfig struct {
	// Hostname is the desired system hostname.
	Hostname string `json:"hostname,omitempty"`
	// Interfaces maps NIC names to their static config.
	Interfaces map[string]InterfaceConfig `json:"interfaces,omitempty"`
	// DNSServers is the global list of DNS resolvers.
	DNSServers []string `json:"dns_servers,omitempty"`
}

// InterfaceConfig is the config for one NIC.
type InterfaceConfig struct {
	// Addresses is a list of CIDR addresses, e.g. ["192.168.1.10/24"].
	Addresses []string `json:"addresses,omitempty"`
	// Gateway4 is the IPv4 default route.
	Gateway4 string `json:"gateway4,omitempty"`
	// DHCP4 enables DHCP on this interface.
	DHCP4 bool `json:"dhcp4,omitempty"`
}

// Manager manages network configuration.
type Manager struct {
	exec       cmdexec.Executor
	netplanDir string
}

// NewManager creates a Manager.
// netplanDir defaults to /etc/netplan; override for testing.
func NewManager(exec cmdexec.Executor, netplanDir string) *Manager {
	if netplanDir == "" {
		netplanDir = "/etc/netplan"
	}
	return &Manager{exec: exec, netplanDir: netplanDir}
}

// GetInfo returns the current hostname and network interfaces using the OS.
func (m *Manager) GetInfo(ctx context.Context) (*NetworkInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("network: get hostname: %w", err)
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("network: list interfaces: %w", err)
	}
	var ifInfos []InterfaceInfo
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		var addrStrs []string
		for _, a := range addrs {
			addrStrs = append(addrStrs, a.String())
		}
		ifInfos = append(ifInfos, InterfaceInfo{
			Name:      iface.Name,
			Addresses: addrStrs,
		})
	}
	return &NetworkInfo{
		Hostname:   hostname,
		Interfaces: ifInfos,
	}, nil
}

// GenerateNetplan produces a netplan YAML from the given config.
// This is always safe to call (no filesystem writes; no apply).
// [AC-Sf92666-2-1]
func GenerateNetplan(cfg NetplanConfig) ([]byte, error) {
	type netplanAddr struct {
		Addresses  []string `yaml:"addresses,omitempty"`
		Gateway4   string   `yaml:"gateway4,omitempty"`
		DHCP4      bool     `yaml:"dhcp4,omitempty"`
		Nameservers *struct {
			Addresses []string `yaml:"addresses"`
		} `yaml:"nameservers,omitempty"`
	}
	type netplanDoc struct {
		Network struct {
			Version   int                     `yaml:"version"`
			Ethernets map[string]netplanAddr  `yaml:"ethernets,omitempty"`
		} `yaml:"network"`
	}

	doc := netplanDoc{}
	doc.Network.Version = 2
	if len(cfg.Interfaces) > 0 {
		doc.Network.Ethernets = make(map[string]netplanAddr)
		for name, ifCfg := range cfg.Interfaces {
			a := netplanAddr{
				Addresses: ifCfg.Addresses,
				Gateway4:  ifCfg.Gateway4,
				DHCP4:     ifCfg.DHCP4,
			}
			if len(cfg.DNSServers) > 0 {
				a.Nameservers = &struct {
					Addresses []string `yaml:"addresses"`
				}{Addresses: cfg.DNSServers}
			}
			doc.Network.Ethernets[name] = a
		}
	}

	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("network: marshal netplan: %w", err)
	}
	return data, nil
}

// ApplyConfig applies network configuration:
//   1. Generates /etc/netplan/99-kura.yaml
//   2. Sets hostname via hostnamectl (if changed)
//   3. Calls `netplan apply`
//
// Steps 2 and 3 only execute when KURA_NETWORK_APPLY=1 is set.
// [AC-Sf92666-2-1]
func (m *Manager) ApplyConfig(ctx context.Context, cfg NetplanConfig) error {
	// Always generate the YAML (validates config; testable without the flag).
	data, err := GenerateNetplan(cfg)
	if err != nil {
		return err
	}

	netplanPath := filepath.Join(m.netplanDir, "99-kura.yaml")
	if os.Getenv("KURA_NETWORK_APPLY") != "1" {
		// Dry-run: just validate that the config is coherent. No filesystem write.
		_ = data
		return nil
	}

	// Write netplan file.
	if err := os.MkdirAll(m.netplanDir, 0o755); err != nil {
		return fmt.Errorf("network: mkdir %s: %w", m.netplanDir, err)
	}
	if err := os.WriteFile(netplanPath, data, 0o644); err != nil {
		return fmt.Errorf("network: write %s: %w", netplanPath, err)
	}

	// Set hostname if provided and different from current.
	if cfg.Hostname != "" {
		current, _ := os.Hostname()
		if strings.TrimSpace(current) != strings.TrimSpace(cfg.Hostname) {
			_, _, err := m.exec.Run(ctx, "hostnamectl", "set-hostname", cfg.Hostname)
			if err != nil {
				return fmt.Errorf("network: hostnamectl: %w", err)
			}
		}
	}

	// Apply netplan.
	_, _, err = m.exec.Run(ctx, "netplan", "apply")
	if err != nil {
		return fmt.Errorf("network: netplan apply: %w", err)
	}
	return nil
}
