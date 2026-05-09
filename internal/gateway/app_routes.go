// Package gateway: dynamic app routing.
//
// engine/app's AppLifecycle calls AppRouteSet.Register / Unregister on
// install / uninstall. The gateway's HTTP mux defers to the set on every
// request hitting /apps/<name>/* (path mode), Host==<sub>.<base> (subdomain
// mode), or :<host_port> (port mode).
//
// Implementations of the consumer-side RouteRegistry interface (engine/app
// declares it) live in engine/app — *engine/app.MemoryRouteRegistry is what
// production wires here, so the gateway and engine/app share a single
// registry instance.
package gateway

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/kuraos-org/kura/engine/app"
)

// AppRouteHandler is the HTTP entry point for /apps/<name>/* (path mode).
// Constructed via NewAppRouteHandler with the shared registry; the gateway
// mounts it at /apps/.
//
// AuthBundle, when non-nil, is consulted on each request to apply the
// AppRoute.AuthMode policy:
//
//   - AuthMode == "forward_auth": the route requires a live KuraOS session.
//     Unauthenticated requests are 302'd to /login; authenticated ones get
//     X-Forwarded-User: <username> (or the manifest-supplied header_user)
//     forwarded to the upstream.
//   - AuthMode == "oidc" / "none" / "" : pass-through; the upstream is
//     expected to do its own auth (oidc apps use the KuraOS OP at /oidc/*).
type AppRouteHandler struct {
	Registry *app.MemoryRouteRegistry
	// ProxyHost overrides the upstream host (default 127.0.0.1). Tests may
	// point this at httptest.Server.URL's host when exercising end-to-end.
	ProxyHost string
	// Auth is consulted for forward_auth routes. nil disables the gating
	// (useful for tests that don't need auth wired).
	Auth *authBundle
}

// NewAppRouteHandler returns a handler that resolves /apps/<name>/... to the
// app's host_port via the registry.
func NewAppRouteHandler(reg *app.MemoryRouteRegistry) *AppRouteHandler {
	return &AppRouteHandler{Registry: reg, ProxyHost: "127.0.0.1"}
}

// NewAppRouteHandlerWithAuth wires the auth bundle so forward_auth routes
// can resolve the operator session and inject X-Forwarded-User.
func NewAppRouteHandlerWithAuth(reg *app.MemoryRouteRegistry, sessions SessionResolver, users UserLookup) *AppRouteHandler {
	return &AppRouteHandler{
		Registry:  reg,
		ProxyHost: "127.0.0.1",
		Auth:      &authBundle{sessions: sessions, users: users},
	}
}

// ServeHTTP looks up the app by the first /apps/<name>/ path segment.
// strip_prefix=true rewrites the URL to /; strip_prefix=false (with
// base_path_env) leaves the prefix in place so the app reads its base from
// env.
func (h *AppRouteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/apps/")
	parts := strings.SplitN(rest, "/", 2)
	if parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	appName := parts[0]
	route, ok := h.Registry.LookupByName(appName)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if route.HostPort == 0 {
		http.Error(w, "app port not assigned", http.StatusBadGateway)
		return
	}

	// AuthMode=forward_auth gating: the operator must have a live KuraOS
	// session. We resolve the principal here (rather than deferring to a
	// generic middleware) because the header injection needs the username
	// and we have to decide before forwarding.
	var forwardedUser string
	if route.AuthMode == app.AuthModeForwardAuth && h.Auth != nil {
		u, ok, err := h.Auth.resolvePrincipal(r)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if !ok {
			loginURL := "/login?return_to=" + url.QueryEscape(r.URL.RequestURI())
			http.Redirect(w, r, loginURL, http.StatusFound)
			return
		}
		forwardedUser = u.Username
	}

	// Build proxy target URL.
	target := &url.URL{Scheme: "http", Host: h.ProxyHost + ":" + intToStr(route.HostPort)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	if route.AuthMode == app.AuthModeForwardAuth && forwardedUser != "" {
		headerName := route.HeaderUser
		if headerName == "" {
			headerName = "X-Forwarded-User"
		}
		captured := forwardedUser
		baseDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			baseDirector(req)
			// Strip any client-supplied value first to defeat header
			// smuggling — DESIGN_PRINCIPLES priority #5 (信頼性).
			req.Header.Del(headerName)
			req.Header.Set(headerName, captured)
		}
	}

	// Adjust request path according to strip_prefix.
	original := r.URL.Path
	if route.Mode == app.RoutingModePath {
		if route.StripPrefix {
			suffix := ""
			if len(parts) > 1 {
				suffix = "/" + parts[1]
			} else {
				suffix = "/"
			}
			r.URL.Path = suffix
		}
		// If !StripPrefix, the upstream app reads its base path from env;
		// pass the URL through unchanged.
	}
	defer func() { r.URL.Path = original }()

	proxy.ServeHTTP(w, r)
}

// AppRouteListHandler returns a JSON list of all registered routes. Used by
// the Apps UI to render the "currently routed" status without requiring
// a separate API call per app.
func AppRouteListHandler(reg *app.MemoryRouteRegistry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("["))
		for i, rt := range reg.ListAppRoutes() {
			if i > 0 {
				w.Write([]byte(","))
			}
			w.Write([]byte(routeToJSON(rt)))
		}
		w.Write([]byte("]"))
	})
}

// routeToJSON renders one route. Hand-rolled because importing encoding/json
// here would pull in struct tags we don't want to add upstream.
func routeToJSON(rt app.AppRoute) string {
	return `{"app_id":"` + jsonEscape(rt.AppID) +
		`","app_name":"` + jsonEscape(rt.AppName) +
		`","mode":"` + string(rt.Mode) +
		`","host_port":` + intToStr(rt.HostPort) +
		`,"strip_prefix":` + boolToStr(rt.StripPrefix) +
		`,"subdomain":"` + jsonEscape(rt.Subdomain) +
		`","auth_mode":"` + string(rt.AuthMode) +
		`","header_user":"` + jsonEscape(rt.HeaderUser) +
		`"}`
}

func jsonEscape(s string) string {
	return strings.NewReplacer(`"`, `\"`, "\\", `\\`).Replace(s)
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

func boolToStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
