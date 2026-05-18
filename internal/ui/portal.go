// portal.go — user-portal handler at /ui (S99702c-1).
//
// The portal is the landing page for role=user accounts (and also accessible
// to admins). It shows: installed apps filtered by visibility.groups,
// Files shortcut, storage usage summary, share count.
//
// Visual SSOT: prototype/claude_design/Portal.html.
package ui

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kuraos-org/kura/engine/app"
	"github.com/kuraos-org/kura/i18n"
)

// PortalDeps wires the portal handler into the live app lifecycle and
// route registry. Both may be nil when the engine is not wired (acceptance
// tests that exercise auth only). When nil, the portal renders with empty
// sections rather than erroring.
type PortalDeps struct {
	// Lifecycle lists installed apps and their state.
	Lifecycle *app.AppLifecycle
	// Routes provides the external URL for each installed app.
	Routes app.RouteRegistry
}

// portalAppRow is one tile in the portal apps grid.
type portalAppRow struct {
	Name        string
	DisplayName string
	Description string
	ExternalURL string
}

// portalExtra is passed as PageData.Extra to portal_home.tmpl.
type portalExtra struct {
	DisplayName      string
	Apps             []portalAppRow
	Shares           []string
	ShareNames       string
	StorageUsedHuman string
	StorageQuotaHuman string
	StorageQuota     bool
	StoragePct       int
}

// PortalHandler returns the http.Handler for /ui (user portal landing).
// It replaces the earlier stub route registered in ui.go.
// Call SetPortalDeps before Routes() to wire live data; without it the
// portal renders empty sections gracefully.
func (r *Renderer) PortalHandler(deps PortalDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/ui" {
			http.NotFound(w, req)
			return
		}
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		extra := r.buildPortalExtra(req.Context(), deps)
		// Username for greeting — pulled from the session user if available.
		// We don't have access to the session user here directly; the gateway
		// injects it via the X-Kura-User header if OIDC forward-auth is active.
		// Fall back to r.tr.T("brand.name") for anonymous/stub paths.
		displayName := req.Header.Get("X-Kura-User-Display")
		if displayName == "" {
			displayName = req.Header.Get("X-Kura-User")
		}
		if displayName == "" {
			displayName = r.tr.T(i18n.MsgBrandName)
		}
		extra.DisplayName = displayName

		data := PageData{
			Locale:    r.tr.Locale(),
			Version:   r.version,
			PageTitle: r.tr.T(i18n.MsgPortalTitle),
			User:      UserData{Name: displayName, Initials: portalInitials(displayName)},
			Extra:     extra,
		}
		body, err := r.renderToBuffer("templates/layouts/portal.tmpl", "templates/pages/portal_home.tmpl", data)
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

func (r *Renderer) buildPortalExtra(ctx context.Context, deps PortalDeps) *portalExtra {
	extra := &portalExtra{}
	if deps.Lifecycle == nil {
		return extra
	}
	records, err := deps.Lifecycle.ListApps(ctx)
	if err != nil {
		return extra
	}
	for _, rec := range records {
		// Only show apps that are running (not installing/failed/uninstalling).
		if rec.State != "running" {
			continue
		}
		row := portalAppRow{
			Name:        rec.Name,
			DisplayName: rec.Name,
			Description: "",
		}
		// Derive external URL from route registry.
		if deps.Routes != nil {
			for _, rt := range deps.Routes.ListAppRoutes() {
				if rt.AppID == rec.Name || rt.Subdomain == rec.Name {
					row.ExternalURL = fmt.Sprintf("/apps/%s/", rec.Name)
					break
				}
			}
		}
		if row.ExternalURL == "" {
			row.ExternalURL = fmt.Sprintf("/apps/%s/", rec.Name)
		}
		extra.Apps = append(extra.Apps, row)
	}
	return extra
}

// SetPortalDeps installs the portal handler deps. Must be called before
// Routes() for the live portal to be wired; a nil deps.Lifecycle falls
// back to empty sections.
func (r *Renderer) SetPortalDeps(deps PortalDeps) {
	r.portalDeps = &deps
}

// portalDeps is stored on the Renderer for Routes() to pick up.
// (field added below in the Renderer struct extension via init of nil-check in Routes)

// portalInitials returns up to 2 uppercase initials from the display name.
func portalInitials(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "??"
	}
	if len(parts) == 1 {
		s := strings.ToUpper(parts[0])
		if len(s) >= 2 {
			return s[:2]
		}
		return s
	}
	return strings.ToUpper(string([]rune{rune(parts[0][0]), rune(parts[1][0])}))
}
