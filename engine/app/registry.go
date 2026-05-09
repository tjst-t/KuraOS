package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Registry is the parsed contents of registry.json (design.md §7.9).
// Apps are keyed by name; per-version entries carry the manifest_sha256
// the client must match at fetch time.
type Registry struct {
	SchemaVersion string                 `json:"schema_version"`
	UpdatedAt     time.Time              `json:"updated_at"`
	Apps          map[string]RegistryApp `json:"apps"`
}

// RegistryApp is the per-app block: a map of versions and the "latest"
// pointer used by the install UI default.
type RegistryApp struct {
	Versions map[string]RegistryVersion `json:"versions"`
	Latest   string                     `json:"latest"`
}

// RegistryVersion is one row of versions[]. ManifestSHA256 is the bound
// hash; the client refuses to load any manifest whose bytes do not
// reproduce this digest.
type RegistryVersion struct {
	ManifestSHA256 string    `json:"manifest_sha256"`
	DriverSHA256   string    `json:"driver_sha256,omitempty"`
	ReleasedAt     time.Time `json:"released_at,omitempty"`
}

// RegistrySource is the trust + location pair for one registry. Mirrors
// config.json's app_registries[] shape (design.md §7.9).
type RegistrySource struct {
	Name          string
	URL           string
	IdentityRegex string
	Issuer        string
}

// HTTPDoer abstracts net/http.Client so tests pass an httptest server's
// Client without monkey-patching globals.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// RegistryClient interface from design.md §7.9. FetchRegistry retrieves
// and verifies registry.json; FetchManifestRaw retrieves manifest.yaml,
// dual-checks (hash + signature), and returns the raw bytes plus the
// parsed manifest. Returning the raw bytes lets callers re-write them
// into the install transcript so the operator can inspect what was
// actually installed.
type RegistryClient interface {
	FetchRegistry(ctx context.Context, src RegistrySource) (*Registry, error)
	FetchManifest(ctx context.Context, src RegistrySource, app, version string) (*Manifest, []byte, error)
}

// Errors callers may want to detect specifically.
var (
	ErrRegistryFetch         = errors.New("app: registry fetch failed")
	ErrRegistryParse         = errors.New("app: registry parse failed")
	ErrManifestNotInRegistry = errors.New("app: manifest not in registry")
	ErrManifestHashMismatch  = errors.New("app: manifest hash mismatch")
)

// HTTPRegistryClient is the production client. It downloads
// `<base>/registry.json` and `<base>/registry.json.sig`, verifies the
// signature, parses the JSON, then for FetchManifest downloads
// `<base>/apps/<app>/<version>/manifest.yaml(.sig)`, performs the
// dual hash + signature check, and returns the parsed Manifest.
type HTTPRegistryClient struct {
	HTTP     HTTPDoer
	Verifier CosignVerifier
}

// NewHTTPRegistryClient defaults the HTTP client to a 30s-timeout
// http.Client when one is not provided. Verifier is required.
func NewHTTPRegistryClient(verifier CosignVerifier, httpDoer HTTPDoer) *HTTPRegistryClient {
	if httpDoer == nil {
		httpDoer = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPRegistryClient{HTTP: httpDoer, Verifier: verifier}
}

// FetchRegistry downloads + verifies the registry index. Order:
//  1. download body and signature
//  2. verify signature against the trust policy in src
//  3. parse JSON
//
// Failure at any step returns a wrapped error; never returns the parsed
// registry alongside an error.
func (c *HTTPRegistryClient) FetchRegistry(ctx context.Context, src RegistrySource) (*Registry, error) {
	body, err := c.download(ctx, src.URL, "registry.json")
	if err != nil {
		return nil, err
	}
	sig, err := c.download(ctx, src.URL, "registry.json.sig")
	if err != nil {
		return nil, err
	}
	if err := c.Verifier.VerifyBlob(ctx, body, sig, src.IdentityRegex, src.Issuer); err != nil {
		return nil, fmt.Errorf("registry %s: %w", src.Name, err)
	}
	reg, err := parseRegistry(body)
	if err != nil {
		return nil, err
	}
	return reg, nil
}

// FetchManifest downloads + verifies manifest.yaml for (app, version).
// design.md §7.9 mandates the dual check: signature AND hash. Either
// failure aborts immediately; we never return a partially-trusted
// manifest. Order matches design.md sample code (hash first, then
// signature) so the integration tests in S65b510 can reproduce the
// expected error from a known-bad bundle.
func (c *HTTPRegistryClient) FetchManifest(ctx context.Context, src RegistrySource, app, version string) (*Manifest, []byte, error) {
	reg, err := c.FetchRegistry(ctx, src)
	if err != nil {
		return nil, nil, err
	}
	a, ok := reg.Apps[app]
	if !ok {
		return nil, nil, fmt.Errorf("%w: app %q", ErrManifestNotInRegistry, app)
	}
	v, ok := a.Versions[version]
	if !ok {
		return nil, nil, fmt.Errorf("%w: app %q version %q", ErrManifestNotInRegistry, app, version)
	}

	relPath := fmt.Sprintf("apps/%s/%s/manifest.yaml", app, version)
	body, err := c.download(ctx, src.URL, relPath)
	if err != nil {
		return nil, nil, err
	}
	sig, err := c.download(ctx, src.URL, relPath+".sig")
	if err != nil {
		return nil, nil, err
	}

	// 1. Hash check — protects against a registry that signs registry.json
	//    correctly but serves a tampered manifest.yaml afterwards.
	actual := sha256.Sum256(body)
	actualHex := hex.EncodeToString(actual[:])
	if !strings.EqualFold(actualHex, v.ManifestSHA256) {
		return nil, nil, fmt.Errorf("%w: app=%s version=%s expected=%s got=%s",
			ErrManifestHashMismatch, app, version, v.ManifestSHA256, actualHex)
	}

	// 2. Signature check — independent of (1). Two failure paths so a
	//    compromise in one channel still fails closed.
	if err := c.Verifier.VerifyBlob(ctx, body, sig, src.IdentityRegex, src.Issuer); err != nil {
		return nil, nil, fmt.Errorf("manifest %s/%s: %w", app, version, err)
	}

	m, err := ParseManifest(body)
	if err != nil {
		return nil, nil, err
	}
	if err := Validate(m); err != nil {
		return nil, nil, err
	}
	return m, body, nil
}

// download fetches base/path with context. Returns wrapped errors so
// callers can errors.Is(ErrRegistryFetch, ...) without parsing the
// message.
func (c *HTTPRegistryClient) download(ctx context.Context, base, path string) ([]byte, error) {
	u, err := joinURL(base, path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRegistryFetch, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrRegistryFetch, err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrRegistryFetch, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s: HTTP %d", ErrRegistryFetch, path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: read: %v", ErrRegistryFetch, path, err)
	}
	return body, nil
}

// joinURL appends path to base, tolerant of trailing slashes.
func joinURL(base, path string) (string, error) {
	if base == "" {
		return "", errors.New("base url is empty")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base %q: %w", base, err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return u.String(), nil
}

func parseRegistry(body []byte) (*Registry, error) {
	var r Registry
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRegistryParse, err)
	}
	if r.SchemaVersion == "" {
		return nil, fmt.Errorf("%w: schema_version missing", ErrRegistryParse)
	}
	if len(r.Apps) == 0 {
		return nil, fmt.Errorf("%w: apps map empty", ErrRegistryParse)
	}
	return &r, nil
}

// sha256Hex is a small helper used by the HTTP client and the FakeVerifier.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// AppRegistryConfig is the in-memory mirror of config.json's
// app_registries[]. Loaded by the AppsConfig adapter (S65b510); declared
// here so this sprint's allocator and FetchManifest path have a stable
// shape to consume. Trust must be `cosign_keyless` for v1; any other
// value is rejected at config-load.
type AppRegistryConfig struct {
	Name  string                 `json:"name"`
	URL   string                 `json:"url"`
	Trust AppRegistryTrustConfig `json:"trust"`
}

// AppRegistryTrustConfig mirrors design.md §7.9 trust block.
type AppRegistryTrustConfig struct {
	Type          string `json:"type"`
	IdentityRegex string `json:"identity_regex"`
	Issuer        string `json:"issuer,omitempty"`
}

// ToSource collapses the config view into the runtime RegistrySource
// shape the RegistryClient consumes. Validates the trust type per
// DESIGN_PRINCIPLES priority #8 (明示的 > 暗黙的: any unknown trust
// type rejects rather than silently downgrades).
func (a AppRegistryConfig) ToSource() (RegistrySource, error) {
	if a.Name == "" {
		return RegistrySource{}, errors.New("app: registry name is empty")
	}
	if a.URL == "" {
		return RegistrySource{}, fmt.Errorf("app: registry %q url is empty", a.Name)
	}
	if a.Trust.Type != "cosign_keyless" {
		return RegistrySource{}, fmt.Errorf("app: registry %q trust.type %q unsupported (want cosign_keyless)",
			a.Name, a.Trust.Type)
	}
	if _, err := CompileIdentityRegex(a.Trust.IdentityRegex); err != nil {
		return RegistrySource{}, fmt.Errorf("app: registry %q: %w", a.Name, err)
	}
	return RegistrySource{
		Name:          a.Name,
		URL:           a.URL,
		IdentityRegex: a.Trust.IdentityRegex,
		Issuer:        a.Trust.Issuer,
	}, nil
}

// LoadRegistrySources turns the config-side []AppRegistryConfig into the
// runtime []RegistrySource list, failing on the first invalid entry.
// Empty input is acceptable and returns an empty list (a fresh KuraOS
// install starts with no trusted registries; the operator adds 'official'
// in the setup wizard).
func LoadRegistrySources(cfgs []AppRegistryConfig) ([]RegistrySource, error) {
	out := make([]RegistrySource, 0, len(cfgs))
	for _, c := range cfgs {
		s, err := c.ToSource()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
