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
//   OIDC_MOCK_PORT          (default 9998)
//   OIDC_MOCK_ISSUER        (default http://127.0.0.1:<port>)
//   OIDC_MOCK_SUBJECT       (default mock-user-1) — used when SUBJECT_MODE=fixed
//   OIDC_MOCK_EMAIL         (default <subject>@oidc-mock.example.com when fixed)
//   OIDC_MOCK_NAME          (default Mock User)
//   OIDC_MOCK_SUBJECT_MODE  fixed | per-flow (default fixed)
//
// per-flow mode derives a unique subject from the OAuth state parameter on
// every /authorize call, so each federation flow produces a fresh
// (subject, email) pair. Used by the S413bd5 pending-user e2e tests so
// re-runs don't trip the "already linked" branch. fixed mode preserves
// the original behavior for tests that depend on subject stability
// (e.g. link / role-redirect specs).
//
// Used by tests/e2e/google-federation-*.e2e.spec.ts. Lives as a
// systemd service `dev-oidc-mock.service` on the dev VM so kura's
// federation Manager can call into it via 127.0.0.1.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// identity is the (sub, email, name) tuple a single flow resolves to.
// Stored keyed by the authorization code at /authorize, looked up by code
// at /token (which then derives a per-flow access_token so /userinfo can
// recover the same identity).
type identity struct {
	Sub   string
	Email string
	Name  string
}

func main() {
	port := envOr("OIDC_MOCK_PORT", "9998")
	issuer := envOr("OIDC_MOCK_ISSUER", "http://127.0.0.1:"+port)
	fixedSubject := envOr("OIDC_MOCK_SUBJECT", "mock-user-1")
	fixedEmail := envOr("OIDC_MOCK_EMAIL", fixedSubject+"@oidc-mock.example.com")
	name := envOr("OIDC_MOCK_NAME", "Mock User")
	mode := envOr("OIDC_MOCK_SUBJECT_MODE", "fixed")

	// codeIdents and tokenIdents bridge the three OIDC endpoints: /authorize
	// stores the identity by code, /token swaps it onto an access_token, and
	// /userinfo reads back via access_token. A real OP would do this with a
	// signed JWT access_token; we keep it as an in-memory map.
	var (
		mu           sync.Mutex
		codeIdents   = map[string]identity{}
		tokenIdents  = map[string]identity{}
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

		id := identity{Name: name}
		if mode == "per-flow" {
			// Derive a stable-per-flow subject from state. State is opaque
			// random per /federation/<p>/start invocation, so each flow
			// resolves to a different mock user — no cross-run contamination.
			h := sha256.Sum256([]byte(state))
			suffix := hex.EncodeToString(h[:6])
			id.Sub = "mock-eph-" + suffix
			id.Email = id.Sub + "@oidc-mock.example.com"
		} else {
			id.Sub = fixedSubject
			id.Email = fixedEmail
		}

		code := "mock-code-" + state
		mu.Lock()
		codeIdents[code] = id
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
		// Look up the identity that /authorize parked under this code, then
		// move it onto a fresh access_token so /userinfo can recover it.
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		code := r.PostForm.Get("code")
		mu.Lock()
		id, ok := codeIdents[code]
		if !ok {
			// Backward compat: tests that hit /token without going through
			// /authorize (rare; the federation Manager always calls /authorize
			// first) get the env-configured fixed identity.
			id = identity{Sub: fixedSubject, Email: fixedEmail, Name: name}
		}
		delete(codeIdents, code)
		accessToken := "mock-at-" + code
		tokenIdents[accessToken] = id
		mu.Unlock()
		writeJSON(w, map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     makeUnsignedIDToken(id.Sub, id.Email, issuer),
		})
	})

	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		mu.Lock()
		id, ok := tokenIdents[token]
		mu.Unlock()
		if !ok {
			// Fall back to fixed identity — older tests pass a static
			// access_token without going through /token.
			id = identity{Sub: fixedSubject, Email: fixedEmail, Name: name}
		}
		writeJSON(w, map[string]any{
			"sub":            id.Sub,
			"email":          id.Email,
			"email_verified": true,
			"name":           id.Name,
		})
	})

	log.Printf("oidc-mock: listening on :%s (issuer=%s, mode=%s, fixedSubject=%s)", port, issuer, mode, fixedSubject)
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
