package selfupdate_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/selfupdate"
)

// stubChecker returns a fixed ReleaseInfo.
type stubChecker struct {
	info *selfupdate.ReleaseInfo
	err  error
}

func (s *stubChecker) Latest(_ context.Context) (*selfupdate.ReleaseInfo, error) {
	return s.info, s.err
}

// stubHealth always succeeds.
type stubHealth struct{ err error }

func (s *stubHealth) Check(_ context.Context, _ string) error { return s.err }

// [AC-Sf92666-4-1] CheckForUpdate returns nil when already up to date.
func TestCheckForUpdate_UpToDate(t *testing.T) {
	checker := &stubChecker{info: &selfupdate.ReleaseInfo{Version: "v1.0.0"}}
	u := selfupdate.NewUpdater("/tmp/kura", checker, "http://localhost/healthz")
	got, err := u.CheckForUpdate(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil (up to date), got %+v", got)
	}
}

// [AC-Sf92666-4-1] CheckForUpdate returns ReleaseInfo when a new version is available.
func TestCheckForUpdate_NewVersion(t *testing.T) {
	checker := &stubChecker{info: &selfupdate.ReleaseInfo{Version: "v2.0.0"}}
	u := selfupdate.NewUpdater("/tmp/kura", checker, "http://localhost/healthz")
	got, err := u.CheckForUpdate(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if got == nil {
		t.Fatal("expected ReleaseInfo, got nil")
	}
	if got.Version != "v2.0.0" {
		t.Errorf("expected v2.0.0, got %s", got.Version)
	}
}

// [AC-Sf92666-4-1] Apply verifies SHA256 and atomically replaces the binary.
func TestApply_SuccessPath(t *testing.T) {
	dir := t.TempDir()

	// Create a "current" binary.
	currentBin := filepath.Join(dir, "kura")
	if err := os.WriteFile(currentBin, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("write current: %v", err)
	}

	// Create a "new" binary to serve over HTTP.
	newBinContent := []byte("new binary content")
	hash := sha256.Sum256(newBinContent)
	hashHex := hex.EncodeToString(hash[:])

	// Serve the binary via a test HTTP server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(newBinContent)
	}))
	defer srv.Close()

	checker := &stubChecker{info: &selfupdate.ReleaseInfo{
		Version:   "v2.0.0",
		BinaryURL: srv.URL + "/kura",
		SHA256:    hashHex,
	}}

	u := selfupdate.NewUpdater(currentBin, checker, "http://localhost/healthz")
	u.Health = &stubHealth{err: nil}           // health OK
	u.RestartFunc = func(_ context.Context) error { return nil } // no-op restart

	info := &selfupdate.ReleaseInfo{
		Version:   "v2.0.0",
		BinaryURL: srv.URL + "/kura",
		SHA256:    hashHex,
	}
	if err := u.Apply(context.Background(), info); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Current binary must now have the new content.
	got, err := os.ReadFile(currentBin)
	if err != nil {
		t.Fatalf("read new binary: %v", err)
	}
	if string(got) != string(newBinContent) {
		t.Errorf("binary not replaced: got %q", string(got))
	}

	// kura.bak must exist.
	if _, err := os.Stat(filepath.Join(dir, "kura.bak")); err != nil {
		t.Errorf("kura.bak not found: %v", err)
	}
}

// [AC-Sf92666-4-2] Apply rolls back when SHA256 is wrong.
func TestApply_SHA256Mismatch_Rollback(t *testing.T) {
	dir := t.TempDir()
	currentBin := filepath.Join(dir, "kura")
	originalContent := []byte("working binary")
	if err := os.WriteFile(currentBin, originalContent, 0o755); err != nil {
		t.Fatalf("write current: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("bad binary"))
	}))
	defer srv.Close()

	checker := &stubChecker{}
	u := selfupdate.NewUpdater(currentBin, checker, "http://localhost/healthz")
	u.Health = &stubHealth{}
	u.RestartFunc = func(_ context.Context) error { return nil }

	info := &selfupdate.ReleaseInfo{
		Version:   "v2.0.0",
		BinaryURL: srv.URL + "/kura",
		SHA256:    "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}
	err := u.Apply(context.Background(), info)
	if err == nil {
		t.Fatal("expected SHA256 mismatch error, got nil")
	}

	// Original binary must still be intact (no rollback needed — kura.bak never created).
	got, err2 := os.ReadFile(currentBin)
	if err2 != nil {
		t.Fatalf("read binary after failed apply: %v", err2)
	}
	if string(got) != string(originalContent) {
		t.Errorf("original binary corrupted: got %q", string(got))
	}
}

// [AC-Sf92666-4-2] Apply rolls back to kura.bak when healthcheck fails.
func TestApply_HealthCheckFail_Rollback(t *testing.T) {
	dir := t.TempDir()
	currentBin := filepath.Join(dir, "kura")
	originalContent := []byte("original binary")
	if err := os.WriteFile(currentBin, originalContent, 0o755); err != nil {
		t.Fatalf("write current: %v", err)
	}

	newBinContent := []byte("new binary — health fails")
	hash := sha256.Sum256(newBinContent)
	hashHex := hex.EncodeToString(hash[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(newBinContent)
	}))
	defer srv.Close()

	checker := &stubChecker{}
	u := selfupdate.NewUpdater(currentBin, checker, "http://localhost/healthz")
	u.Health = &stubHealth{err: context.DeadlineExceeded} // health fails
	u.RestartFunc = func(_ context.Context) error { return nil }

	info := &selfupdate.ReleaseInfo{
		Version:   "v2.0.0",
		BinaryURL: srv.URL + "/kura",
		SHA256:    hashHex,
	}
	err := u.Apply(context.Background(), info)
	if err == nil {
		t.Fatal("expected healthcheck failure, got nil")
	}

	// After rollback, the original content must be restored.
	got, err2 := os.ReadFile(currentBin)
	if err2 != nil {
		t.Fatalf("read binary after rollback: %v", err2)
	}
	if string(got) != string(originalContent) {
		t.Errorf("rollback failed: got %q", string(got))
	}
}

// [AC-Sf92666-4-1] Ed25519 signature verification works correctly.
func TestApply_SignatureVerify(t *testing.T) {
	dir := t.TempDir()
	currentBin := filepath.Join(dir, "kura")
	if err := os.WriteFile(currentBin, []byte("old"), 0o755); err != nil {
		t.Fatalf("write current: %v", err)
	}

	// Generate an ed25519 key pair for testing.
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	newBinContent := []byte("signed binary content")
	sig := ed25519.Sign(privKey, newBinContent)
	sigHex := hex.EncodeToString(sig)
	hash := sha256.Sum256(newBinContent)
	hashHex := hex.EncodeToString(hash[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(newBinContent)
	}))
	defer srv.Close()

	u := selfupdate.NewUpdater(currentBin, &stubChecker{}, "http://localhost/healthz")
	u.PubKey = pubKey
	u.Health = &stubHealth{err: nil}
	u.RestartFunc = func(_ context.Context) error { return nil }

	info := &selfupdate.ReleaseInfo{
		Version:   "v2.0.0",
		BinaryURL: srv.URL + "/kura",
		SHA256:    hashHex,
		Signature: sigHex,
	}
	if err := u.Apply(context.Background(), info); err != nil {
		t.Fatalf("Apply with valid signature: %v", err)
	}
}

// [AC-Sf92666-4-2] Ed25519 signature mismatch aborts (never overwrites binary).
func TestApply_SignatureMismatch(t *testing.T) {
	dir := t.TempDir()
	currentBin := filepath.Join(dir, "kura")
	originalContent := []byte("safe binary")
	if err := os.WriteFile(currentBin, originalContent, 0o755); err != nil {
		t.Fatalf("write current: %v", err)
	}

	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	newBinContent := []byte("tampered binary")
	hash := sha256.Sum256(newBinContent)
	hashHex := hex.EncodeToString(hash[:])
	// Use an obviously wrong signature.
	badSig := hex.EncodeToString(make([]byte, 64))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(newBinContent)
	}))
	defer srv.Close()

	u := selfupdate.NewUpdater(currentBin, &stubChecker{}, "http://localhost/healthz")
	u.PubKey = pubKey
	u.Health = &stubHealth{}
	u.RestartFunc = func(_ context.Context) error { return nil }

	info := &selfupdate.ReleaseInfo{
		Version:   "v2.0.0",
		BinaryURL: srv.URL + "/kura",
		SHA256:    hashHex,
		Signature: badSig,
	}
	err = u.Apply(context.Background(), info)
	if err == nil {
		t.Fatal("expected signature mismatch error, got nil")
	}

	// Original binary must be intact.
	got, _ := os.ReadFile(currentBin)
	if string(got) != string(originalContent) {
		t.Errorf("original binary overwritten despite signature mismatch")
	}
	_ = time.Second // suppress unused import warning
}
