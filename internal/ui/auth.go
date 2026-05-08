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
			r.renderLoginPage(w, req, loginPageData{})
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
	if err := req.ParseForm(); err != nil {
		r.renderLoginPage(w, req, loginPageData{ErrorMessage: r.tr.T(i18n.MsgLoginGenericError)})
		return
	}
	username := req.PostForm.Get("username")
	password := req.PostForm.Get("password")
	if username == "" {
		w.WriteHeader(http.StatusBadRequest)
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginUsernameRequired),
			Username:     username,
		})
		return
	}
	if password == "" {
		w.WriteHeader(http.StatusBadRequest)
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginPasswordRequired),
			Username:     username,
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
		})
		return
	}
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginInvalidCreds),
			Username:     username,
		})
		return
	}
	sess, err := d.Sessions.Issue(req.Context(), u.ID, req.UserAgent(), req.RemoteAddr)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		r.renderLoginPage(w, req, loginPageData{
			ErrorMessage: r.tr.T(i18n.MsgLoginGenericError),
			Username:     username,
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
