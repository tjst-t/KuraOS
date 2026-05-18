package providers_test

import (
	"testing"

	"github.com/kuraos-org/kura/engine/network/acme"
	_ "github.com/kuraos-org/kura/engine/network/acme/providers" // register cloudflare via init()
)

// [AC-Sf92666-1-3] Importing the providers package registers cloudflare
// automatically via init(); the UI List() reflects it without code changes.
func TestCloudflareRegistered(t *testing.T) {
	names := acme.List()
	found := false
	for _, n := range names {
		if n == "cloudflare" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cloudflare provider not registered; got %v", names)
	}
}

// [AC-Sf92666-1-2] newCloudflare returns an error when no API token is set.
func TestCloudflare_NoToken(t *testing.T) {
	// Clear env to ensure no token leaks from the test environment.
	t.Setenv("KURA_CLOUDFLARE_API_TOKEN", "")
	_, err := acme.New("cloudflare", map[string]string{})
	if err == nil {
		t.Fatal("expected error when API token is empty, got nil")
	}
}

// [AC-Sf92666-1-2] newCloudflare succeeds when the token is in the config map.
func TestCloudflare_TokenFromCfg(t *testing.T) {
	t.Setenv("KURA_CLOUDFLARE_API_TOKEN", "")
	t.Setenv("KURA_ACME_STAGING", "1") // staging mode: no real API calls
	p, err := acme.New("cloudflare", map[string]string{"api_token": "test-token"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

// [AC-Sf92666-1-2] newCloudflare picks the token from env when cfg is empty.
func TestCloudflare_TokenFromEnv(t *testing.T) {
	t.Setenv("KURA_CLOUDFLARE_API_TOKEN", "env-test-token")
	t.Setenv("KURA_ACME_STAGING", "1")
	p, err := acme.New("cloudflare", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}
