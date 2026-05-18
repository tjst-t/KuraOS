package tls_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
	"time"

	tlspkg "github.com/kuraos-org/kura/engine/network/tls"
)

// [AC-Sf92666-1-1] EnsureSelfSigned generates a local CA + leaf cert.
func TestEnsureSelfSigned(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	result, err := tlspkg.EnsureSelfSigned(ctx, dir, []string{"testhost.local"})
	if err != nil {
		t.Fatalf("EnsureSelfSigned: %v", err)
	}

	// CA cert must exist.
	caPEM, err := os.ReadFile(result.CACertPath)
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		t.Fatal("CA cert: no PEM block")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	if !caCert.IsCA {
		t.Fatal("CA cert: IsCA false")
	}
	// CA cert must be valid for > 1 year.
	if time.Until(caCert.NotAfter) < 365*24*time.Hour {
		t.Errorf("CA cert expires too soon: %v", caCert.NotAfter)
	}

	// Leaf cert must be signed by CA and contain expected SAN.
	leafPEM, err := os.ReadFile(result.LeafCertPath)
	if err != nil {
		t.Fatalf("read leaf cert: %v", err)
	}
	leafBlock, _ := pem.Decode(leafPEM)
	leafCert, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	_, err = leafCert.Verify(x509.VerifyOptions{Roots: pool})
	if err != nil {
		t.Errorf("leaf cert verify against local CA: %v", err)
	}

	foundHost := false
	for _, n := range leafCert.DNSNames {
		if n == "testhost.local" {
			foundHost = true
		}
	}
	if !foundHost {
		t.Errorf("leaf cert DNSNames %v missing 'testhost.local'", leafCert.DNSNames)
	}

	// [AC-Sf92666-1-1] LoadTLSConfig must succeed with the generated cert pair.
	tlsCfg, err := tlspkg.LoadTLSConfig(result.LeafCertPath, result.LeafKeyPath)
	if err != nil {
		t.Fatalf("LoadTLSConfig: %v", err)
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Fatal("expected 1 certificate in TLS config")
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected TLS 1.2 minimum, got %v", tlsCfg.MinVersion)
	}
}

// [AC-Sf92666-1-1] EnsureSelfSigned is idempotent — calling again returns
// the same paths without regenerating when cert is fresh.
func TestEnsureSelfSigned_Idempotent(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	r1, err := tlspkg.EnsureSelfSigned(ctx, dir, []string{"testhost.local"})
	if err != nil {
		t.Fatalf("first EnsureSelfSigned: %v", err)
	}

	// Read the leaf cert before second call.
	data1, _ := os.ReadFile(r1.LeafCertPath)

	// Second call — cert is fresh, should not regenerate.
	r2, err := tlspkg.EnsureSelfSigned(ctx, dir, []string{"testhost.local"})
	if err != nil {
		t.Fatalf("second EnsureSelfSigned: %v", err)
	}

	data2, _ := os.ReadFile(r2.LeafCertPath)
	if string(data1) != string(data2) {
		t.Error("leaf cert was regenerated unnecessarily")
	}
}

// [AC-Sf92666-1-1] InstallHint is non-empty and references the CA cert path.
func TestCAInstallHint(t *testing.T) {
	hint := tlspkg.CAInstallHint("/var/lib/kura/tls/ca.crt")
	if hint == "" {
		t.Fatal("CAInstallHint returned empty string")
	}
	if !contains(hint, "/var/lib/kura/tls/ca.crt") {
		t.Errorf("hint does not mention CA path: %q", hint)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchStr(s, sub)
}

func searchStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
