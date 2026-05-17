// auth.go owns the login / logout HTTP handlers and renders the unauthenticated
// templates (login, setup) on top of the shared Renderer.

package ui

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
)

// AuthDeps is what the auth handler needs from the rest of the binary.
// SecureCookies is plumbed through Deps so the binary can flip it on once
// TLS is wired (S822961). Phase-1 NAS often runs HTTP on the LAN, so the
// default is false but can be overridden in production by env var.
type AuthDeps struct {
	Users         AuthUserStore
	Sessions      AuthSessionStore
	SecureCookies bool
	// Providers lets the login page render "Google でログイン" / etc.
	// buttons next to the password form (Sfix002-2). When nil or
	// empty, no federation buttons render — that matches a
	// federation-disabled deployment (the dominant Phase-1 home
	// install).
	Providers ProviderLister
}

// AuthUserStore is the slice of engine/user the auth handler needs. Defined
// as an interface here so tests can swap in a fake without standing up a
// SQLite store (DESIGN_PRINCIPLES priority #9).
type AuthUserStore interface {
	VerifyPassword(ctx context.Context, username, password string) (user.User, bool, error)
}

// AuthSessionStore is the session.Store contract the auth handler depends on.
type AuthSessionStore interface {
	Issue(ctx context.Context, userID, userAgent, remoteAddr string) (session.Session, error)
	Revoke(ctx context.Context, token string) error
	TTL() time.Duration
}

// AuthHandler returns an http.Handler that owns /login (GET + POST) and /logout.
// Mounted from the gateway so unauthenticated requests can reach it.
func (r *Renderer) AuthHandler(d AuthDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			r.renderLoginPage(w, req, loginPageData{Providers: listProvidersSafe(req.Context(), d.Providers)})
		case http.MethodPost:
			r.handleLoginPost(w, req, d)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost && req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		// Best-effort revoke. If the cookie is absent or already revoked we
		// still clear the cookie + redirect — never leak the failure to the
		// user.
		if c, err := req.Cookie(session.CookieName); err == nil {
			_ = d.Sessions.Revoke(req.Context(), c.Value)
		}
		clearSessionCookie(w, d.SecureCookies)
		// Use a query flag to surface "logged out" via a banner on /login.
		http.Redirect(w, req, "/login?logged_out=1", http.StatusFound)
	})
	return mux
}

type loginPageData struct {
	ErrorMessage string
	InfoMessage  string
	Username     string
	Providers    []UsersProvider
	ReturnTo     string
}

// listProvidersSafe collects enabled federation providers for the login
// page. Failures and nil listers are silently treated as "no providers"
// because federation buttons are a nice-to-have on the login screen —
// surfacing a DB error here would lock out password login too.
func listProvidersSafe(ctx context.Context, l ProviderLister) []UsersProvider {
	if l == nil {
		return nil
	}
	out, err := l.ListProviders(ctx)
	if err != nil {
		return nil
	}
	enabled := make([]UsersProvider, 0, len(out))
	for _, p := range out {
		if p.Enabled {
			enabled = append(enabled, p)
		}
	}
	return enabled
}

func (r *Renderer) renderLoginPage(w http.ResponseWriter, req *http.Request, extra loginPageData) {
	if req.URL.Query().Get("logged_out") == "1" && extra.InfoMessage == "" {
		extra.InfoMessage = r.tr.T(i18n.MsgLoginLogoutSuccess)
	}
	data := PageData{
		Locale:      r.tr.Locale(),
		Version:     r.version,
		PageTitle:   r.tr.T(i18n.MsgLoginTitle),
		PageTitleID: string(i18n.MsgLoginTitle),
		Extra:       extra,
	}
	r.renderWithLayout(w, "templates/layouts/auth.tmpl", "templates/pages/login.tmpl", data)
}

func (r *Renderer) handleLoginPost(w http.ResponseWriter, req *http.Request, d AuthDeps) {
	providers := listProvidersSafe(req.Context(), d.Providers)
	if err := req.ParseForm(); err != nil {
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginGenericError),
			Providers:    providers,
		})
		return
	}
	username := req.PostForm.Get("username")
	password := req.PostForm.Get("password")
	if username == "" {
		w.WriteHeader(http.StatusBadRequest)
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginUsernameRequired),
			Username:     username,
			Providers:    providers,
		})
		return
	}
	if password == "" {
		w.WriteHeader(http.StatusBadRequest)
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginPasswordRequired),
			Username:     username,
			Providers:    providers,
		})
		return
	}
	u, ok, err := d.Users.VerifyPassword(req.Context(), username, password)
	if err != nil {
		// Real DB error — log developer message in English (CLAUDE.md), show
		// translated generic message to the user.
		w.WriteHeader(http.StatusInternalServerError)
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginGenericError),
			Username:     username,
			Providers:    providers,
		})
		return
	}
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginInvalidCreds),
			Username:     username,
			Providers:    providers,
		})
		return
	}
	sess, err := d.Sessions.Issue(req.Context(), u.ID, req.UserAgent(), req.RemoteAddr)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginGenericError),
			Username:     username,
			Providers:    providers,
		})
		return
	}
	setSessionCookie(w, sess.ID, d.Sessions.TTL(), d.SecureCookies)
	dest := "/ui/admin/dashboard"
	if u.Role == user.RoleUser {
		dest = "/ui"
	}
	http.Redirect(w, req, dest, http.StatusFound)
}

// setSessionCookie writes the kura_session cookie with the strictest sane
// defaults: HttpOnly + SameSite=Lax. Path "/" so /ui, /api, /logout all see
// it. Secure is wired from the caller (false on LAN HTTP, true on TLS).
func setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// errLogin is a sentinel kept so other packages don't need to import this
// file just to reference loginPageData. Currently unused but reserved so
// the public API stays tidy when /api/login lands.
var errLogin = errors.New("ui: login error")

var _ = errLogin // keep for future use without "declared and not used"

// PendingApprovalHandler returns the /ui/pending-approval page. The route is
// intentionally mounted outside the admin/user requireRole gates so that a
// pending-role user (who has a valid session but no page access) can reach it.
// Non-pending authenticated users who manually type the URL are bounced to /.
func (r *Renderer) PendingApprovalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		data := PageData{
			Locale:      r.tr.Locale(),
			Version:     r.version,
			PageTitle:   r.tr.T(i18n.MsgPendingApprovalTitle),
			PageTitleID: string(i18n.MsgPendingApprovalTitle),
		}
		r.renderWithLayout(w, "templates/layouts/auth.tmpl", "templates/pages/pending_approval.tmpl", data)
	})
}

// federationErrorData is the view model for templates/pages/federation_error.tmpl.
// MessageID is an i18n key looked up via the {{ T }} template func so the
// caller never has to pre-translate.
type federationErrorData struct {
	MessageID string
}

// RenderFederationError satisfies engine/auth/federation.ErrorRenderer:
// renders a Fog-palette error page with an i18n message and a "ログインに戻る"
// button. Used when an unbound Google user hits the callback with
// auto_provision disabled or when the provisioner fails (Sfix002-3).
func (r *Renderer) RenderFederationError(w http.ResponseWriter, _ *http.Request, msgID string, status int) {
	data := PageData{
		Locale:      r.tr.Locale(),
		Version:     r.version,
		PageTitle:   r.tr.T(i18n.MsgFederationErrorTitle),
		PageTitleID: string(i18n.MsgFederationErrorTitle),
		Extra:       federationErrorData{MessageID: msgID},
	}
	w.WriteHeader(status)
	r.renderWithLayout(w, "templates/layouts/auth.tmpl", "templates/pages/federation_error.tmpl", data)
}
