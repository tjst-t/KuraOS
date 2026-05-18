package gateway

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/kuraos-org/kura/engine/app"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
)

// Deps is what the Gateway needs from the rest of the binary. Keeping the
// surface tiny makes it easy to wire fakes in tests.
type Deps struct {
	Translator *i18n.Translator
	Version    string
	StartedAt  time.Time
	// UIHandler serves /ui/admin/* (admin shell, static assets) and is
	// guarded by the admin-role middleware.
	UIHandler http.Handler
	// UIUserHandler serves /ui (the user-facing portal). Optional — when
	// nil, /ui mirrors UIHandler. Subsequent sprints split admin vs user
	// shells; for now both are admin-only and /ui aliases to /ui/admin.
	UIUserHandler http.Handler
	// AuthHandler serves /login, /logout, and POST /login.
	AuthHandler http.Handler
	// SetupHandler serves /setup (first-admin wizard). Mounted under the
	// requireNoAdmin middleware so it disappears once an admin exists.
	SetupHandler http.Handler

	// Sessions / Users wire the auth middleware. When nil (e.g. in the
	// /healthz-only test), authentication is bypassed and /ui/admin is open
	// — same behaviour as before this sprint added auth.
	Sessions SessionResolver
	Users    UserLookup

	// AppRoutes is the dynamic registry engine/app updates on
	// install/uninstall. When non-nil, /apps/<name>/* and /api/app-routes
	// are mounted. Tests / minimal binaries leave it nil to skip those
	// surfaces.
	AppRoutes *app.MemoryRouteRegistry

	// OIDCHandler serves the in-house OpenID Provider at /oidc/* (added
	// in S822961). Wired when an OIDC OP is constructed at startup;
	// minimal/test setups can leave it nil.
	OIDCHandler http.Handler

	// FederationHandler serves external IdP RP callbacks (e.g. Google) at
	// /federation/<provider>/(start|callback). Wired alongside OIDC.
	FederationHandler http.Handler

	// PendingApprovalHandler serves /ui/pending-approval. It is mounted
	// with a custom middleware that allows any authenticated session
	// (including role=pending) — the handler itself bounces non-pending
	// users back to /. Wired from cmd/kura when auth is enabled.
	PendingApprovalHandler http.Handler

	// MetricsHandler serves GET /metrics in OpenMetrics format (S8a756d-1).
	// When nil the route is not mounted. Admin-only in production.
	MetricsHandler http.Handler

	// FileAPIHandler serves /api/files/* for native apps (S0eedaa-1).
	// Authenticated by X-Kura-Token HMAC header (no session cookie required).
	// When nil the File API is not exposed.
	FileAPIHandler http.Handler
}

// New returns the http.Handler that fronts every HTTP route the kura binary
// exposes. /healthz lives here. /ui/* is delegated to the UI subtree, gated
// by role-based auth middleware. /login and /setup are owned by their own
// handlers but mounted here so the gateway is the single ingress.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz(d))

	if d.AuthHandler != nil {
		mux.Handle("/login", d.AuthHandler)
		mux.Handle("/logout", d.AuthHandler)
	}

	auth := &authBundle{sessions: d.Sessions, users: d.Users}
	hasAuth := d.Sessions != nil && d.Users != nil

	if d.SetupHandler != nil {
		var setup http.Handler = d.SetupHandler
		if hasAuth {
			setup = auth.requireNoAdmin(d.SetupHandler)
		}
		mux.Handle("/setup", setup)
		mux.Handle("/setup/", setup)
	}

	if d.UIHandler != nil {
		var admin http.Handler = d.UIHandler
		if hasAuth {
			admin = auth.requireRoleHandler(roleAdmin, d.UIHandler)
		}
		mux.Handle("/ui/admin/", admin)
		mux.Handle("/ui/admin", admin)

		// /ui/static/* is unauthenticated — CSS/JS shipped to the browser
		// pre-login (login page itself loads /ui/static/kura.css).
		mux.Handle("/ui/static/", d.UIHandler)

		// /ui (without /admin) — user-role shell. For S1e7eeb the user shell
		// shares the admin renderer; later sprints split it. Either way it
		// must be authenticated, but only at user level.
		userHandler := d.UIUserHandler
		if userHandler == nil {
			userHandler = d.UIHandler
		}
		var ui http.Handler = userHandler
		if hasAuth {
			ui = auth.requireRoleHandler(roleUser, userHandler)
		}
		mux.Handle("/ui", ui)

		// /ui/files — built-in filebrowser (S0eedaa-2). User-role session auth.
		// Served by the same UIHandler (which registers /ui/files and /ui/files/*
		// via Renderer.Routes()). A separate mux entry is required because the
		// /ui exact-match above does NOT cover sub-paths in Go 1.22.
		var filesUIHandler http.Handler = d.UIHandler
		if hasAuth {
			filesUIHandler = auth.requireRoleHandler(roleUser, d.UIHandler)
		}
		mux.Handle("/ui/files", filesUIHandler)
		mux.Handle("/ui/files/", filesUIHandler)

		// /ui/pending-approval — only accessible to authenticated users
		// (any role). The gateway intercepts it before the /ui/admin/*
		// and /ui handlers so pending users are not swallowed by
		// requireRole(user) which would 403 them. The handler itself
		// redirects non-pending visitors to /.
		if d.PendingApprovalHandler != nil {
			pendingHandler := d.PendingApprovalHandler
			if hasAuth {
				pendingHandler = auth.requireAnySession(d.PendingApprovalHandler)
			}
			mux.Handle("/ui/pending-approval", pendingHandler)
		}

		// "/" — front door. If no admin yet, send to /setup; else send to
		// /ui/admin/dashboard (which itself enforces the admin role).
		front := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "/ui/admin/dashboard", http.StatusFound)
		})
		var root http.Handler = front
		if hasAuth {
			root = auth.requireAdminExists(front)
		}
		mux.Handle("/", root)
	}

	if d.AppRoutes != nil {
		var appHandler *AppRouteHandler
		if hasAuth {
			appHandler = NewAppRouteHandlerWithAuth(d.AppRoutes, d.Sessions, d.Users)
		} else {
			appHandler = NewAppRouteHandler(d.AppRoutes)
		}
		mux.Handle("/apps/", appHandler)
		mux.Handle("/api/app-routes", AppRouteListHandler(d.AppRoutes))
	}

	// /oidc/* — KuraOS OpenID Provider (S822961). Mounted only when wired.
	if d.OIDCHandler != nil {
		mux.Handle("/oidc/", d.OIDCHandler)
	}
	// /federation/* — external IdP (Google) callback URLs.
	if d.FederationHandler != nil {
		mux.Handle("/federation/", d.FederationHandler)
	}
	// /metrics — OpenMetrics endpoint (S8a756d-1). Admin-only when auth is wired.
	if d.MetricsHandler != nil {
		var mh http.Handler = d.MetricsHandler
		if hasAuth {
			mh = auth.requireRoleHandler(roleAdmin, d.MetricsHandler)
		}
		mux.Handle("/metrics", mh)
	}

	// /api/files/* — File API for native apps (S0eedaa-1).
	// X-Kura-Token validates inside the handler; no gateway-level session check.
	if d.FileAPIHandler != nil {
		mux.Handle("/api/files/", d.FileAPIHandler)
		mux.Handle("/api/files", d.FileAPIHandler)
	}
	return mux
}

// roleAdmin / roleUser shadow the user.Role constants so calls in this file
// stay short. Imported as user.Role* so the underlying type still flows into
// requireRole's signature.
var (
	roleAdmin = user.RoleAdmin
	roleUser  = user.RoleUser
)

// requireRoleHandler is a thin sugar over requireRole so call sites in this
// file don't need to import engine/user just to spell the role.
func (a *authBundle) requireRoleHandler(min user.Role, next http.Handler) http.Handler {
	return a.requireRole(min, next)
}

type healthzResponse struct {
	Status    string    `json:"status"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
}

func healthz(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		body := healthzResponse{
			Status:    d.Translator.T(i18n.MsgSystemHealthy),
			Version:   d.Version,
			StartedAt: d.StartedAt,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}
}
