// Package tls implements KuraOS TLS certificate management.
//
// Two modes are supported:
//   - self_signed: generates a local CA + leaf certificate. Suitable for LAN-only
//     deployments where users can install the CA certificate in their browser.
//   - acme: uses github.com/go-acme/lego for DNS-01 challenges.
//     v1 supports Cloudflare only (other providers are backlog).
//
// Private keys are stored on disk at TLSStorageDir. config.json carries only
// declarative state (mode, provider name, domain list) — never raw secrets.
// Cloudflare API token lives in the vault / env variable.
//
// DESIGN_PRINCIPLES priority #1: config.json = SSOT, no runtime secrets there.
// DESIGN_PRINCIPLES priority #5: verify failures abort; never overwrite a
// working cert with an unverified one.
// DESIGN_PRINCIPLES priority #9: Manager depends on interface DNSProvider so
// lego can be swapped or mocked in tests.
package tls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Mode specifies how TLS certificates are obtained.
type Mode string

const (
	ModeNone       Mode = "none"
	ModeSelfSigned Mode = "self_signed"
	ModeACME       Mode = "acme"
)

// Config is the declarative shape of the TLS section in config.json.
// Private keys and ACME tokens are NOT stored here (DESIGN_PRINCIPLES #1).
type Config struct {
	Mode     Mode     `json:"mode"`
	Port     int      `json:"port,omitempty"`      // default: 8443 for dev, 443 for prod
	Provider string   `json:"provider,omitempty"` // "cloudflare" for acme mode
	Domains  []string `json:"domains,omitempty"`  // required for acme mode
}

// StorageDir is the directory where TLS key material is stored.
// Override via KURA_TLS_DIR env; default /var/lib/kura/tls.
func StorageDir() string {
	if d := os.Getenv("KURA_TLS_DIR"); d != "" {
		return d
	}
	return "/var/lib/kura/tls"
}

// CertPaths returns the paths for the CA cert, leaf cert, and leaf key.
func CertPaths(dir string) (caCert, leafCert, leafKey string) {
	return filepath.Join(dir, "ca.crt"),
		filepath.Join(dir, "server.crt"),
		filepath.Join(dir, "server.key")
}

// CAInstallHint returns a human-readable message about how to install the CA cert.
func CAInstallHint(caCertPath string) string {
	return fmt.Sprintf("ブラウザにCA証明書をインストールするには: %s をダウンロードして、ブラウザの証明書設定から信頼済みルートCAとして追加してください。", caCertPath)
}

// SelfSignedResult holds the paths and hint after generating a self-signed cert.
type SelfSignedResult struct {
	CACertPath   string
	LeafCertPath string
	LeafKeyPath  string
	InstallHint  string
}

// EnsureSelfSigned generates a local CA + leaf certificate if they do not
// already exist, or if the existing cert is within 30 days of expiry.
// Returns the paths to the cert files.
// [AC-Sf92666-1-1]
func EnsureSelfSigned(ctx context.Context, dir string, hosts []string) (*SelfSignedResult, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("tls: mkdir %s: %w", dir, err)
	}

	caCertPath, leafCertPath, leafKeyPath := CertPaths(dir)

	// Check if existing leaf cert is still valid for > 30 days.
	if needsRegen(leafCertPath) == nil {
		return &SelfSignedResult{
			CACertPath:   caCertPath,
			LeafCertPath: leafCertPath,
			LeafKeyPath:  leafKeyPath,
			InstallHint:  CAInstallHint(caCertPath),
		}, nil
	}

	// Generate CA key.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tls: generate CA key: %w", err)
	}

	// CA cert template — 10 years validity.
	caSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			Organization: []string{"KuraOS Local CA"},
			CommonName:   "KuraOS Local CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("tls: create CA cert: %w", err)
	}
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, fmt.Errorf("tls: parse CA cert: %w", err)
	}

	// Write CA cert.
	if err := writePEM(caCertPath, "CERTIFICATE", caCertDER, 0o644); err != nil {
		return nil, fmt.Errorf("tls: write CA cert: %w", err)
	}

	// Generate leaf key.
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tls: generate leaf key: %w", err)
	}

	// Determine SANs.
	if len(hosts) == 0 {
		hosts = []string{"localhost"}
	}
	var ipAddrs []net.IP
	var dnsNames []string
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			ipAddrs = append(ipAddrs, ip)
		} else {
			dnsNames = append(dnsNames, h)
		}
	}
	// Always include localhost and 127.0.0.1.
	dnsNames = append(dnsNames, "localhost")
	ipAddrs = append(ipAddrs, net.ParseIP("127.0.0.1"))

	leafSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	leafTemplate := &x509.Certificate{
		SerialNumber: leafSerial,
		Subject: pkix.Name{
			Organization: []string{"KuraOS"},
			CommonName:   hosts[0],
		},
		NotBefore:   time.Now().Add(-1 * time.Minute),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    dnsNames,
		IPAddresses: ipAddrs,
	}
	leafCertDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("tls: create leaf cert: %w", err)
	}

	// Write leaf cert.
	if err := writePEM(leafCertPath, "CERTIFICATE", leafCertDER, 0o644); err != nil {
		return nil, fmt.Errorf("tls: write leaf cert: %w", err)
	}

	// Write leaf key.
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, fmt.Errorf("tls: marshal leaf key: %w", err)
	}
	if err := writePEM(leafKeyPath, "EC PRIVATE KEY", leafKeyDER, 0o600); err != nil {
		return nil, fmt.Errorf("tls: write leaf key: %w", err)
	}

	return &SelfSignedResult{
		CACertPath:   caCertPath,
		LeafCertPath: leafCertPath,
		LeafKeyPath:  leafKeyPath,
		InstallHint:  CAInstallHint(caCertPath),
	}, nil
}

// LoadTLSConfig loads the TLS configuration from cert/key files.
func LoadTLSConfig(leafCertPath, leafKeyPath string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(leafCertPath, leafKeyPath)
	if err != nil {
		return nil, fmt.Errorf("tls: load key pair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// needsRegen returns non-nil if the cert at path does not exist or expires in < 30 days.
func needsRegen(certPath string) error {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("tls: read %s: %w", certPath, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("tls: no PEM block in %s", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("tls: parse %s: %w", certPath, err)
	}
	if time.Until(cert.NotAfter) < 30*24*time.Hour {
		return fmt.Errorf("tls: cert %s expires soon", certPath)
	}
	return nil
}

func writePEM(path, typ string, der []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}
