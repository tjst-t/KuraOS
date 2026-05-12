// oidc-mock — minimal OIDC Provider for federation e2e testing.
//
// Implements just enough of OIDC Core 1.0 to satisfy KuraOS's federation
// Manager: discovery, /authorize (auto-approves and redirects with a
// code), /token (returns access + id_token), /userinfo. id_token is
// alg=none (unsigned) — matching the v1 federation flow which trusts
// the userinfo round-trip; signature verification is v1.x backlog.
//
// Lives at cmd/oidc-mock/ so a kura test fixture can build it via
// `go build ./cmd/oidc-mock`. Configurable via env:
//
//   OIDC_MOCK_PORT       (default 9998)
//   OIDC_MOCK_ISSUER     (default http://127.0.0.1:<port>)
//   OIDC_MOCK_SUBJECT    (default mock-user-1)
//   OIDC_MOCK_EMAIL      (default mock-user-1@oidc-mock.example.com)
//   OIDC_MOCK_NAME       (default Mock User)
//
// Used by tests/e2e/google-federation-flow.e2e.spec.ts. Lives as a
// systemd service `dev-oidc-mock.service` on the dev VM so kura's
// federation Manager can call into it via 127.0.0.1.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	port := envOr("OIDC_MOCK_PORT", "9998")
	issuer := envOr("OIDC_MOCK_ISSUER", "http://127.0.0.1:"+port)
	subject := envOr("OIDC_MOCK_SUBJECT", "mock-user-1")
	email := envOr("OIDC_MOCK_EMAIL", subject+"@oidc-mock.example.com")
	name := envOr("OIDC_MOCK_NAME", "Mock User")

	var (
		mu       sync.Mutex
		lastCode string
	)

	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                issuer + "/authorize",
			"token_endpoint":                        issuer + "/token",
			"userinfo_endpoint":                     issuer + "/userinfo",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"none"},
			"scopes_supported":                      []string{"openid", "email", "profile"},
		})
	})

	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		// Auto-approve: issue a fresh code and bounce back to redirect_uri.
		// A real IdP would render a consent screen here; tests skip that.
		redir := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		if redir == "" {
			http.Error(w, "redirect_uri required", http.StatusBadRequest)
			return
		}
		mu.Lock()
		lastCode = "mock-code-" + state
		code := lastCode
		mu.Unlock()
		u, err := url.Parse(redir)
		if err != nil {
			http.Error(w, "invalid redirect_uri: "+err.Error(), http.StatusBadRequest)
			return
		}
		q := u.Query()
		q.Set("code", code)
		q.Set("state", state)
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		// No code validation — tests don't need it. Issue tokens.
		writeJSON(w, map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     makeUnsignedIDToken(subject, email, issuer),
		})
	})

	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"sub":            subject,
			"email":          email,
			"email_verified": true,
			"name":           name,
		})
	})

	log.Printf("oidc-mock: listening on :%s (issuer=%s, subject=%s, email=%s)", port, issuer, subject, email)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("oidc-mock: listen: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// makeUnsignedIDToken emits an alg=none JWT carrying just `sub`, `email`,
// and `iss`. v1 federation trusts the userinfo round-trip rather than
// verifying id_token signatures, so the bare token suffices.
func makeUnsignedIDToken(sub, email, iss string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, _ := json.Marshal(map[string]any{
		"sub":   sub,
		"email": email,
		"iss":   iss,
	})
	body := base64.RawURLEncoding.EncodeToString(payload)
	return fmt.Sprintf("%s.%s.", header, body)
}
