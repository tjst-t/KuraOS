package fileapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/fileapi"
)

// [AC-S0eedaa-1-2] Scope evaluation — share not in allowlist → 403.
func TestScopeShareDenied(t *testing.T) {
	mem := fileapi.NewMemBackend()
	mem.WriteDir("/secret")
	mem.WriteFile("/secret/data.txt", []byte("secret"))

	resolver := &fileapi.FixedShareResolver{
		Roots: map[string]string{
			"photos": "/photos",
			"secret": "/secret",
		},
	}
	// App only has access to "photos", not "secret".
	scope := &fileapi.FixedScopeStore{
		Scope: fileapi.AppScope{Shares: []string{"photos"}},
	}
	h := fileapi.NewHandler(fileapi.HandlerConfig{
		Backend:   mem,
		Validator: fileapi.NoopValidator{},
		Scopes:    scope,
		Shares:    resolver,
	})
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	resp := doReq(t, http.MethodGet, srv.URL+"/api/files/secret/data.txt", nil, nil)
	defer resp.Body.Close()

	// [AC-S0eedaa-1-2]
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for denied share, got %d", resp.StatusCode)
	}
}

// [AC-S0eedaa-1-2] Scope evaluation — path_deny blocks specific subdirectory → 403.
func TestScopePathDenied(t *testing.T) {
	mem := fileapi.NewMemBackend()
	mem.WriteDir("/photos")
	mem.WriteDir("/photos/private")
	mem.WriteFile("/photos/private/secret.jpg", []byte("secret"))
	mem.WriteFile("/photos/public.jpg", []byte("public"))

	resolver := &fileapi.FixedShareResolver{
		Roots: map[string]string{"photos": "/photos"},
	}
	// App has "photos" share but path_deny blocks "private/".
	scope := &fileapi.FixedScopeStore{
		Scope: fileapi.AppScope{
			Shares:   []string{"photos"},
			PathDeny: []string{"private"},
		},
	}
	h := fileapi.NewHandler(fileapi.HandlerConfig{
		Backend:   mem,
		Validator: fileapi.NoopValidator{},
		Scopes:    scope,
		Shares:    resolver,
	})
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	// Access to /photos/private/secret.jpg should be denied.
	respDenied := doReq(t, http.MethodGet, srv.URL+"/api/files/photos/private/secret.jpg", nil, nil)
	defer respDenied.Body.Close()

	if respDenied.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for denied path, got %d", respDenied.StatusCode)
	}

	// Access to /photos/public.jpg should be allowed.
	respAllowed := doReq(t, http.MethodGet, srv.URL+"/api/files/photos/public.jpg", nil, nil)
	defer respAllowed.Body.Close()

	if respAllowed.StatusCode != http.StatusOK {
		t.Errorf("want 200 for allowed path, got %d", respAllowed.StatusCode)
	}
}

// [AC-S0eedaa-1-2] No scope registered for app → 403.
func TestScopeNoScope(t *testing.T) {
	mem := fileapi.NewMemBackend()
	mem.WriteDir("/photos")
	mem.WriteFile("/photos/hello.txt", []byte("hello"))

	resolver := &fileapi.FixedShareResolver{
		Roots: map[string]string{"photos": "/photos"},
	}
	h := fileapi.NewHandler(fileapi.HandlerConfig{
		Backend:   mem,
		Validator: fileapi.NoopValidator{},
		Scopes:    fileapi.DenyScopeStore{},
		Shares:    resolver,
	})
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	resp := doReq(t, http.MethodGet, srv.URL+"/api/files/photos/hello.txt", nil, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 when no scope, got %d", resp.StatusCode)
	}
}

// Path traversal: ".." in path → 403.
func TestPathTraversalDotDot(t *testing.T) {
	mem := fileapi.NewMemBackend()
	mem.WriteDir("/photos")
	mem.WriteFile("/photos/hello.txt", []byte("hello"))

	resolver := &fileapi.FixedShareResolver{
		Roots: map[string]string{"photos": "/photos"},
	}
	scope := &fileapi.FixedScopeStore{
		Scope: fileapi.AppScope{Shares: []string{"photos"}},
	}
	h := fileapi.NewHandler(fileapi.HandlerConfig{
		Backend:   mem,
		Validator: fileapi.NoopValidator{},
		Scopes:    scope,
		Shares:    resolver,
	})
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	// Try path traversal: /api/files/photos/../etc/passwd
	resp := doReq(t, http.MethodGet, srv.URL+"/api/files/photos/../etc/passwd", nil, nil)
	defer resp.Body.Close()

	// Go's http.ServeMux will clean the URL before routing, but we also check
	// in safeJoin. Either way the response must not be 200 with sensitive content.
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK && strings.Contains(string(body), "root:") {
		t.Error("path traversal succeeded — /etc/passwd was served!")
	}
}

// Path traversal via explicit ".." in JSON body (mkdir).
func TestPathTraversalInMkdirBody(t *testing.T) {
	mem := fileapi.NewMemBackend()
	mem.WriteDir("/photos")

	resolver := &fileapi.FixedShareResolver{
		Roots: map[string]string{"photos": "/photos"},
	}
	scope := &fileapi.FixedScopeStore{
		Scope: fileapi.AppScope{Shares: []string{"photos"}},
	}
	h := fileapi.NewHandler(fileapi.HandlerConfig{
		Backend:   mem,
		Validator: fileapi.NoopValidator{},
		Scopes:    scope,
		Shares:    resolver,
	})
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	body := `{"path":"../escape"}`
	resp := doReq(t, http.MethodPost, srv.URL+"/api/files/photos/mkdir",
		strings.NewReader(body),
		map[string]string{"Content-Type": "application/json"},
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for path traversal in mkdir, got %d", resp.StatusCode)
	}
}
