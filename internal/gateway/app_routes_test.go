package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/engine/app"
)

// TestAppRouteHandlerStripPrefix verifies AC-S65b510-2-1 path mode with
// strip_prefix=true: the upstream sees "/" rather than "/apps/<name>/".
func TestAppRouteHandlerStripPrefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("upstream:" + r.URL.Path))
	}))
	defer upstream.Close()

	// Extract host:port from upstream URL.
	host := strings.TrimPrefix(upstream.URL, "http://")
	colon := strings.Index(host, ":")
	if colon < 0 {
		t.Fatalf("upstream URL: %s", upstream.URL)
	}
	port := atoi(host[colon+1:])

	reg := app.NewMemoryRouteRegistry()
	_ = reg.RegisterAppRoute(app.AppRoute{
		AppID:       "demo.0001",
		AppName:     "demo",
		Mode:        app.RoutingModePath,
		HostPort:    port,
		StripPrefix: true,
	})
	h := NewAppRouteHandler(reg)
	h.ProxyHost = "127.0.0.1"

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/apps/demo/some/page")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "upstream:/some/page" {
		t.Fatalf("got %q; want upstream:/some/page", string(body))
	}
}

// TestAppRouteHandlerNoStrip verifies the base_path_env mode where the
// upstream sees the full /apps/<name>/... URL.
func TestAppRouteHandlerNoStrip(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("upstream:" + r.URL.Path))
	}))
	defer upstream.Close()
	port := atoi(strings.Split(strings.TrimPrefix(upstream.URL, "http://"), ":")[1])

	reg := app.NewMemoryRouteRegistry()
	_ = reg.RegisterAppRoute(app.AppRoute{
		AppID: "demo.0001", AppName: "demo",
		Mode: app.RoutingModePath, HostPort: port, StripPrefix: false,
		BasePathEnv: "DEMO_BASE",
	})
	h := NewAppRouteHandler(reg)
	h.ProxyHost = "127.0.0.1"
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/apps/demo/something")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "/apps/demo/something") {
		t.Fatalf("got %q; want path retained", string(body))
	}
}

// TestAppRouteListEndpoint covers the JSON listing the UI uses.
func TestAppRouteListEndpoint(t *testing.T) {
	reg := app.NewMemoryRouteRegistry()
	_ = reg.RegisterAppRoute(app.AppRoute{AppID: "a.1", AppName: "a", Mode: app.RoutingModePath, HostPort: 1, StripPrefix: true})
	_ = reg.RegisterAppRoute(app.AppRoute{AppID: "b.1", AppName: "b", Mode: app.RoutingModePort, HostPort: 2})

	srv := httptest.NewServer(AppRouteListHandler(reg))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, `"app_name":"a"`) || !strings.Contains(s, `"app_name":"b"`) {
		t.Fatalf("body = %s", s)
	}
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}
