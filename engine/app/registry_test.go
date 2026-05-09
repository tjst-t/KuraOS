package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fakeServer wires an in-memory registry layout into an httptest.Server.
// Tests construct one, set body bytes for known paths, then point the
// HTTPRegistryClient at server.URL.
type fakeServer struct {
	mux  *http.ServeMux
	srv  *httptest.Server
	body map[string][]byte
}

func newFakeServer() *fakeServer {
	fs := &fakeServer{
		mux:  http.NewServeMux(),
		body: map[string][]byte{},
	}
	fs.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, ok := fs.body[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	})
	fs.srv = httptest.NewServer(fs.mux)
	return fs
}

func (f *fakeServer) put(path string, body []byte) {
	f.body[path] = body
}

func (f *fakeServer) close() { f.srv.Close() }

// fixtureManifest returns a minimal valid manifest YAML used by the
// registry tests (we are not retesting full validation here, just the
// fetch/verify flow).
func fixtureManifest() []byte {
	raw, err := os.ReadFile("testdata/immich.yaml")
	if err != nil {
		panic(fmt.Sprintf("read fixture: %v", err))
	}
	return raw
}

// fixtureRegistryJSON builds a registry.json byte slice that points at
// the fixture manifest, with the SHA256 set to the actual sha256 of the
// fixture body so the dual-check happy path resolves.
func fixtureRegistryJSON(manifestBody []byte) []byte {
	sum := sha256.Sum256(manifestBody)
	tpl := `{
  "schema_version": "1",
  "updated_at": "2026-04-15T10:00:00Z",
  "apps": {
    "immich": {
      "versions": {
        "1.111.0": {
          "manifest_sha256": "%s",
          "released_at": "2026-04-01T00:00:00Z"
        }
      },
      "latest": "1.111.0"
    }
  }
}`
	return []byte(fmt.Sprintf(tpl, hex.EncodeToString(sum[:])))
}

// TestFetchManifestHappyPath covers AC-S1bccf5-2-1: registry.json and
// manifest.yaml are accepted only when the cosign keyless identity
// matches identity_regex. We simulate a successful match here.
func TestFetchManifestHappyPath(t *testing.T) {
	// [AC-S1bccf5-2-1]
	manifestBody := fixtureManifest()
	registryBody := fixtureRegistryJSON(manifestBody)

	server := newFakeServer()
	defer server.close()
	server.put("/registry.json", registryBody)
	server.put("/registry.json.sig", []byte("fake-bundle-bytes"))
	server.put("/apps/immich/1.111.0/manifest.yaml", manifestBody)
	server.put("/apps/immich/1.111.0/manifest.yaml.sig", []byte("fake-bundle-bytes"))

	verifier := NewFakeVerifier()
	verifier.Allow(registryBody, "https://github.com/kuraos-org/registry/.github/workflows/release.yml@refs/tags/v1", "https://token.actions.githubusercontent.com")
	verifier.Allow(manifestBody, "https://github.com/kuraos-org/registry/.github/workflows/release.yml@refs/tags/v1", "https://token.actions.githubusercontent.com")

	src := RegistrySource{
		Name:          "official",
		URL:           server.srv.URL,
		IdentityRegex: `^https://github\.com/kuraos-org/`,
		Issuer:        "https://token.actions.githubusercontent.com",
	}
	client := NewHTTPRegistryClient(verifier, server.srv.Client())
	m, raw, err := client.FetchManifest(context.Background(), src, "immich", "1.111.0")
	if err != nil {
		t.Fatalf("FetchManifest happy path: %v", err)
	}
	if m == nil || m.Name != "immich" {
		t.Errorf("unexpected manifest: %+v", m)
	}
	if !strings.Contains(string(raw), "name: immich") {
		t.Errorf("raw bytes did not contain expected fixture content")
	}
}

// TestFetchManifestIdentityMismatch covers AC-S1bccf5-2-1 negative path:
// when the signing identity does not match identity_regex, the fetch is
// rejected with ErrIdentityMismatch (wrapped).
func TestFetchManifestIdentityMismatch(t *testing.T) {
	// [AC-S1bccf5-2-1]
	manifestBody := fixtureManifest()
	registryBody := fixtureRegistryJSON(manifestBody)

	server := newFakeServer()
	defer server.close()
	server.put("/registry.json", registryBody)
	server.put("/registry.json.sig", []byte("fake-bundle-bytes"))
	server.put("/apps/immich/1.111.0/manifest.yaml", manifestBody)
	server.put("/apps/immich/1.111.0/manifest.yaml.sig", []byte("fake-bundle-bytes"))

	verifier := NewFakeVerifier()
	// Identity here is from a totally different org — won't match the
	// kuraos-org regex.
	verifier.Allow(registryBody, "https://github.com/attacker-org/evil/.github/workflows/x.yml@main", "https://token.actions.githubusercontent.com")
	verifier.Allow(manifestBody, "https://github.com/attacker-org/evil/.github/workflows/x.yml@main", "https://token.actions.githubusercontent.com")

	src := RegistrySource{
		Name:          "official",
		URL:           server.srv.URL,
		IdentityRegex: `^https://github\.com/kuraos-org/`,
		Issuer:        "https://token.actions.githubusercontent.com",
	}
	client := NewHTTPRegistryClient(verifier, server.srv.Client())
	_, _, err := client.FetchManifest(context.Background(), src, "immich", "1.111.0")
	if err == nil {
		t.Fatalf("expected identity mismatch, got nil")
	}
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Errorf("err = %v, want wraps ErrIdentityMismatch", err)
	}
}

// TestFetchManifestMissingSignatureRecord ensures a payload the verifier
// has never seen is rejected as a signature-verification failure.
func TestFetchManifestMissingSignatureRecord(t *testing.T) {
	// [AC-S1bccf5-2-1]
	manifestBody := fixtureManifest()
	registryBody := fixtureRegistryJSON(manifestBody)

	server := newFakeServer()
	defer server.close()
	server.put("/registry.json", registryBody)
	server.put("/registry.json.sig", []byte("fake-bundle-bytes"))

	// No verifier records at all — signature verification fails on
	// registry.json before we ever touch the manifest.
	verifier := NewFakeVerifier()

	src := RegistrySource{
		Name:          "official",
		URL:           server.srv.URL,
		IdentityRegex: `^https://github\.com/kuraos-org/`,
		Issuer:        "https://token.actions.githubusercontent.com",
	}
	client := NewHTTPRegistryClient(verifier, server.srv.Client())
	_, err := client.FetchRegistry(context.Background(), src)
	if err == nil {
		t.Fatalf("expected signature failure, got nil")
	}
	if !errors.Is(err, ErrSignatureVerification) {
		t.Errorf("err = %v, want wraps ErrSignatureVerification", err)
	}
}

// TestFetchManifestHashMismatch covers AC-S1bccf5-2-2: when the actual
// manifest bytes hash differs from registry.json's manifest_sha256, the
// fetch is aborted with ErrManifestHashMismatch.
func TestFetchManifestHashMismatch(t *testing.T) {
	// [AC-S1bccf5-2-2]
	originalManifest := fixtureManifest()
	registryBody := fixtureRegistryJSON(originalManifest)

	// Server serves a tampered manifest (single byte appended) but the
	// registry.json still pins the original hash. The verifier is told
	// the tampered body is "validly signed" so signature alone would
	// pass — proving the hash check is the line of defense here.
	tampered := append([]byte{}, originalManifest...)
	tampered = append(tampered, '\n')

	server := newFakeServer()
	defer server.close()
	server.put("/registry.json", registryBody)
	server.put("/registry.json.sig", []byte("fake-bundle-bytes"))
	server.put("/apps/immich/1.111.0/manifest.yaml", tampered)
	server.put("/apps/immich/1.111.0/manifest.yaml.sig", []byte("fake-bundle-bytes"))

	verifier := NewFakeVerifier()
	verifier.Allow(registryBody, "https://github.com/kuraos-org/registry/.github/workflows/release.yml@refs/tags/v1", "https://token.actions.githubusercontent.com")
	verifier.Allow(tampered, "https://github.com/kuraos-org/registry/.github/workflows/release.yml@refs/tags/v1", "https://token.actions.githubusercontent.com")

	src := RegistrySource{
		Name:          "official",
		URL:           server.srv.URL,
		IdentityRegex: `^https://github\.com/kuraos-org/`,
		Issuer:        "https://token.actions.githubusercontent.com",
	}
	client := NewHTTPRegistryClient(verifier, server.srv.Client())
	_, _, err := client.FetchManifest(context.Background(), src, "immich", "1.111.0")
	if err == nil {
		t.Fatalf("expected hash mismatch, got nil")
	}
	if !errors.Is(err, ErrManifestHashMismatch) {
		t.Errorf("err = %v, want wraps ErrManifestHashMismatch", err)
	}
	// AC-S1bccf5-2-2 promises a "明確なエラー" — confirm the message
	// includes both the expected and actual hashes so the operator can
	// debug.
	if !strings.Contains(err.Error(), "expected=") || !strings.Contains(err.Error(), "got=") {
		t.Errorf("err message lacks expected/got hashes: %v", err)
	}
}

// TestFetchRegistryUnknownTopLevelField guards against a tampered
// registry.json that adds attacker-controlled fields.
func TestFetchRegistryUnknownTopLevelField(t *testing.T) {
	body := []byte(`{"schema_version":"1","apps":{"immich":{"versions":{}}},"injected":"yes"}`)

	server := newFakeServer()
	defer server.close()
	server.put("/registry.json", body)
	server.put("/registry.json.sig", []byte("fake-bundle-bytes"))

	verifier := NewFakeVerifier()
	verifier.Allow(body, "https://github.com/kuraos-org/x", "https://token.actions.githubusercontent.com")

	src := RegistrySource{
		Name:          "official",
		URL:           server.srv.URL,
		IdentityRegex: `^https://github\.com/kuraos-org/`,
	}
	client := NewHTTPRegistryClient(verifier, server.srv.Client())
	_, err := client.FetchRegistry(context.Background(), src)
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
	if !errors.Is(err, ErrRegistryParse) {
		t.Errorf("err = %v, want wraps ErrRegistryParse", err)
	}
}

// TestLoadRegistrySources covers the config.json → runtime conversion
// rules: explicit trust type required, identity regex must compile,
// missing fields rejected.
func TestLoadRegistrySources(t *testing.T) {
	cases := []struct {
		name    string
		cfgs    []AppRegistryConfig
		wantErr string
	}{
		{
			name: "official cosign_keyless ok",
			cfgs: []AppRegistryConfig{
				{
					Name: "official",
					URL:  "https://registry.kuraos.org",
					Trust: AppRegistryTrustConfig{
						Type:          "cosign_keyless",
						IdentityRegex: `^https://github\.com/kuraos-org/`,
					},
				},
			},
		},
		{
			name: "rejects unknown trust type",
			cfgs: []AppRegistryConfig{
				{
					Name: "shady",
					URL:  "https://shady.example/",
					Trust: AppRegistryTrustConfig{
						Type:          "shared_secret",
						IdentityRegex: `^.*$`,
					},
				},
			},
			wantErr: "trust.type",
		},
		{
			name: "rejects empty url",
			cfgs: []AppRegistryConfig{
				{
					Name:  "official",
					Trust: AppRegistryTrustConfig{Type: "cosign_keyless", IdentityRegex: `^x`},
				},
			},
			wantErr: "url is empty",
		},
		{
			name: "rejects bad regex",
			cfgs: []AppRegistryConfig{
				{
					Name: "official",
					URL:  "https://x/",
					Trust: AppRegistryTrustConfig{
						Type:          "cosign_keyless",
						IdentityRegex: `(unbalanced`,
					},
				},
			},
			wantErr: "identity_regex",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadRegistrySources(tc.cfgs)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected err: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected err containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}
