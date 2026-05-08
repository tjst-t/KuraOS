// setup.go owns the first-admin wizard handler. The /setup route itself is
// gated by the gateway's requireNoAdmin middleware so this file can assume
// it only runs while the User table has zero admins.

package ui

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
)

// SetupDeps mirrors AuthDeps but exposes the create-user surface. Kept
// separate so the auth handler doesn't accidentally gain user-creation
// capability outside the wizard.
type SetupDeps struct {
	Users         SetupUserStore
	Sessions      AuthSessionStore
	SecureCookies bool
}

// SetupUserStore is the slice of *user.Store the wizard needs. The interface
// shape lets tests drop in a fake without standing up SQLite.
type SetupUserStore interface {
	CountByRole(ctx context.Context, role user.Role) (int, error)
	CreateLocalUser(ctx context.Context, username, displayName, password string, role user.Role) (user.User, error)
}

// usernameRule constrains usernames to characters that are safe across the
// downstream targets (POSIX user accounts when smbd binds, ZFS dataset
// owners, OIDC subject prefixes). Letters/digits/underscore/hyphen, 2-32
// chars, must start with a letter.
var usernameRule = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,31}$`)

// minPasswordLen — the wizard's only password rule. We deliberately do not
// enforce class-mix rules: NIST SP 800-63B-4 deprecates them in favor of
// length + breach-corpus checks, and a NAS owner is creating a long key for
// themselves, not picking from a corporate policy.
const minPasswordLen = 8

// SetupHandler returns the http.Handler that owns /setup (GET) and
// /setup/admin (POST). Mount it under requireNoAdmin so it disappears as
// soon as a user with role=admin exists.
func (r *Renderer) SetupHandler(d SetupDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/setup", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		r.renderSetupPage(w, req, setupPageData{})
	})
	mux.HandleFunc("/setup/admin", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		r.handleSetupAdminPost(w, req, d)
	})
	return mux
}

type setupPageData struct {
	ErrorMessage string
	Username     string
	DisplayName  string
}

func (r *Renderer) renderSetupPage(w http.ResponseWriter, req *http.Request, extra setupPageData) {
	data := PageData{
		Locale:      r.tr.Locale(),
		Version:     r.version,
		PageTitle:   r.tr.T(i18n.MsgSetupTitle),
		PageTitleID: string(i18n.MsgSetupTitle),
		Extra:       extra,
	}
	r.renderWithLayout(w, "templates/layouts/auth.tmpl", "templates/pages/setup.tmpl", data)
}

func (r *Renderer) handleSetupAdminPost(w http.ResponseWriter, req *http.Request, d SetupDeps) {
	if err := req.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		r.renderSetupPage(w, req, setupPageData{ErrorMessage: r.tr.T(i18n.MsgLoginGenericError)})
		return
	}
	username := strings.TrimSpace(req.PostForm.Get("username"))
	displayName := strings.TrimSpace(req.PostForm.Get("display_name"))
	password := req.PostForm.Get("password")
	confirm := req.PostForm.Get("password_confirm")

	// Re-check the no-admin invariant inside the POST handler so a race
	// between two browser tabs can't end with two admins. The middleware
	// already filters GETs; here we close the tiny gap before the INSERT.
	n, err := d.Users.CountByRole(req.Context(), user.RoleAdmin)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		r.renderSetupPage(w, req, setupPageData{ErrorMessage: r.tr.T(i18n.MsgLoginGenericError)})
		return
	}
	if n > 0 {
		// An admin already exists — abort the wizard, send to login.
		http.Redirect(w, req, "/login", http.StatusFound)
		return
	}

	switch {
	case !usernameRule.MatchString(username):
		w.WriteHeader(http.StatusBadRequest)
		r.renderSetupPage(w, req, setupPageData{
			ErrorMessage: r.tr.T(i18n.MsgSetupAdminUsernameRule),
			Username:     username,
			DisplayName:  displayName,
		})
		return
	case len(password) < minPasswordLen:
		w.WriteHeader(http.StatusBadRequest)
		r.renderSetupPage(w, req, setupPageData{
			ErrorMessage: r.tr.T(i18n.MsgSetupAdminPasswordShort),
			Username:     username,
			DisplayName:  displayName,
		})
		return
	case password != confirm:
		w.WriteHeader(http.StatusBadRequest)
		r.renderSetupPage(w, req, setupPageData{
			ErrorMessage: r.tr.T(i18n.MsgSetupAdminPasswordMatch),
			Username:     username,
			DisplayName:  displayName,
		})
		return
	}

	u, err := d.Users.CreateLocalUser(req.Context(), username, displayName, password, user.RoleAdmin)
	if err != nil {
		var msg string
		if errors.Is(err, user.ErrUsernameTaken) {
			msg = r.tr.T(i18n.MsgSetupAdminUsernameTaken)
		} else {
			msg = r.tr.T(i18n.MsgSetupAdminError, err.Error())
		}
		w.WriteHeader(http.StatusBadRequest)
		r.renderSetupPage(w, req, setupPageData{
			ErrorMessage: msg,
			Username:     username,
			DisplayName:  displayName,
		})
		return
	}

	// Issue a session so the new admin lands in /ui/admin/dashboard already
	// signed in — Step 1 ends with a working management console, not a
	// detour through the login screen.
	sess, err := d.Sessions.Issue(req.Context(), u.ID, req.UserAgent(), req.RemoteAddr)
	if err != nil {
		// User is created; surface the failure but redirect to /login so they
		// can sign in manually.
		http.Redirect(w, req, "/login", http.StatusFound)
		return
	}
	setSessionCookie(w, sess.ID, ttlOr(d.Sessions.TTL(), 24*time.Hour), d.SecureCookies)
	http.Redirect(w, req, "/ui/admin/dashboard", http.StatusFound)
}

// ttlOr returns d if positive, otherwise fallback. Avoids handing a 0 Max-Age
// to setSessionCookie when a test passes a zero-valued Sessions stub.
func ttlOr(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

// _ = session is here so go vet doesn't complain about an unused import in
// future refactors that move the cookie helpers into this file.
var _ = session.CookieName
