package gateway

import (
	"encoding/json"
	"net/http"
	"time"

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
