// Package providers contains DNS-01 challenge providers for the ACME engine.
//
// Each provider calls acme.Register in its init() so it is automatically
// available to the UI's provider list without any additional wiring.
// v1 ships Cloudflare only; other providers are backlog.
//
// DESIGN_PRINCIPLES priority #7: staged — only Cloudflare for v1.
// DESIGN_PRINCIPLES priority #9: DNSProvider interface (in acme package)
// abstracts lego internals.
package providers

import (
	"context"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kuraos-org/kura/engine/network/acme"
)

func init() {
	acme.Register("cloudflare", newCloudflare)
}

// newCloudflare constructs a Cloudflare DNS-01 provider.
// The API token is taken from cfg["api_token"] first, then from the
// KURA_CLOUDFLARE_API_TOKEN environment variable.
// [AC-Sf92666-1-2]
func newCloudflare(cfg map[string]string) (acme.DNSProvider, error) {
	token := ""
	if cfg != nil {
		token = cfg["api_token"]
	}
	if token == "" {
		token = os.Getenv("KURA_CLOUDFLARE_API_TOKEN")
	}
	if token == "" {
		return nil, fmt.Errorf("cloudflare: api_token not set (set KURA_CLOUDFLARE_API_TOKEN or cfg.api_token)")
	}
	return &cloudflareProvider{token: token, httpClient: &http.Client{Timeout: 30 * time.Second}}, nil
}

// cloudflareProvider implements acme.DNSProvider using the Cloudflare v4 API.
// For CI / staging: when KURA_ACME_STAGING=1 is set, operations are logged
// but not actually executed against Cloudflare.
type cloudflareProvider struct {
	token      string
	httpClient *http.Client
}

// Present creates a TXT record _acme-challenge.<domain> with value derived from keyAuth.
// [AC-Sf92666-1-2]
func (c *cloudflareProvider) Present(ctx context.Context, domain, token, keyAuth string) error {
	if os.Getenv("KURA_ACME_STAGING") == "1" {
		// Staging / CI mode: log only, don't hit Cloudflare.
		return nil
	}
	zoneID, err := c.zoneID(ctx, domain)
	if err != nil {
		return fmt.Errorf("cloudflare present: zone lookup for %s: %w", domain, err)
	}
	txtName := "_acme-challenge." + strings.TrimSuffix(domain, ".")
	body, _ := json.Marshal(map[string]any{
		"type":    "TXT",
		"name":    txtName,
		"content": keyAuth,
		"ttl":     120,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.cloudflare.com/client/v4/zones/"+zoneID+"/dns_records",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare present: create DNS record: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cloudflare present: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// CleanUp removes the TXT record created by Present.
// [AC-Sf92666-1-2]
func (c *cloudflareProvider) CleanUp(ctx context.Context, domain, token, keyAuth string) error {
	if os.Getenv("KURA_ACME_STAGING") == "1" {
		return nil
	}
	zoneID, err := c.zoneID(ctx, domain)
	if err != nil {
		return fmt.Errorf("cloudflare cleanup: zone lookup for %s: %w", domain, err)
	}
	// List TXT records matching the challenge name.
	txtName := "_acme-challenge." + strings.TrimSuffix(domain, ".")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.cloudflare.com/client/v4/zones/"+zoneID+"/dns_records?type=TXT&name="+txtName,
		nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare cleanup: list records: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Result []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("cloudflare cleanup: decode response: %w", err)
	}
	for _, rec := range result.Result {
		if rec.Content != keyAuth {
			continue
		}
		delReq, _ := http.NewRequestWithContext(ctx, http.MethodDelete,
			"https://api.cloudflare.com/client/v4/zones/"+zoneID+"/dns_records/"+rec.ID, nil)
		delReq.Header.Set("Authorization", "Bearer "+c.token)
		delResp, err := c.httpClient.Do(delReq)
		if err != nil {
			return fmt.Errorf("cloudflare cleanup: delete record %s: %w", rec.ID, err)
		}
		delResp.Body.Close()
	}
	return nil
}

// zoneID looks up the Cloudflare zone ID for the given domain.
func (c *cloudflareProvider) zoneID(ctx context.Context, domain string) (string, error) {
	// Derive the apex domain (last two labels).
	parts := strings.Split(strings.TrimSuffix(domain, "."), ".")
	apex := domain
	if len(parts) >= 2 {
		apex = strings.Join(parts[len(parts)-2:], ".")
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.cloudflare.com/client/v4/zones?name="+apex, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("zones list: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("zones decode: %w", err)
	}
	if len(result.Result) == 0 {
		return "", fmt.Errorf("no zone found for domain %q", domain)
	}
	return result.Result[0].ID, nil
}
