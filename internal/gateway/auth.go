package gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"
)

// SessionResolver is what the auth middleware needs from the session layer.
// Defining it as an interface in this package — rather than importing the
// concrete *session.Store directly into types — keeps gateway tests
// trivially mockable (DESIGN_PRINCIPLES priority #9).
type SessionResolver interface {
	Lookup(ctx context.Context, token string) (session.Session, error)
}

// UserLookup is what the auth middleware needs from the user layer: resolve
// a user by ID (so role / disabled flag are fresh on every request, not
// cached on session creation).
type UserLookup interface {
	GetByID(ctx context.Context, id string) (user.User, error)
	CountByRole(ctx context.Context, role user.Role) (int, error)
}

// principalKey is the unexported context key under which the resolved User
// is stored. Handlers downstream of authMiddleware retrieve it via
// PrincipalFromContext.
type principalKey struct{}

// PrincipalFromContext returns the authenticated user attached to ctx by the
// auth middleware, or (User{}, false) if no session was attached.
func PrincipalFromContext(ctx context.Context) (user.User, bool) {
	v, ok := ctx.Value(principalKey{}).(user.User)
	return v, ok
}

// authBundle wires the middleware's collaborators. Created once by New() and
// shared across handler closures.
type authBundle struct {
	sessions SessionResolver
	users    UserLookup
}

// resolvePrincipal reads the session cookie and returns the live user, if
// any. It returns (User{}, false, nil) when the request has no session or
// the session doesn't resolve. A real DB error propagates so the handler can
// return 500 instead of silently treating it as "unauthenticated".
func (a *authBundle) resolvePrincipal(r *http.Request) (user.User, bool, error) {
	c, err := r.Cookie(session.CookieName)
	if err != nil {
		return user.User{}, false, nil
	}
	sess, err := a.sessions.Lookup(r.Context(), c.Value)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return user.User{}, false, nil
		}
		return user.User{}, false, err
	}
	u, err := a.users.GetByID(r.Context(), sess.UserID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return user.User{}, false, nil
		}
		return user.User{}, false, err
	}
	if u.Disabled {
		return user.User{}, false, nil
	}
	return u, true, nil
}

// withPrincipal returns a copy of r whose context carries u as the principal.
func withPrincipal(r *http.Request, u user.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, u))
}

// requireAdminExists wraps a handler so that it 302s to /setup whenever zero
// admin users exist in the DB. Used by the front-door redirect from "/" so
// fresh installs land on the wizard.
func (a *authBundle) requireAdminExists(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := a.users.CountByRole(r.Context(), user.RoleAdmin)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if n == 0 {
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireRole returns a middleware that enforces role hierarchy: admin > user.
// Unauthenticated requests get a 302 to /login; authenticated-but-wrong-role
// requests get a 403 (DESIGN_PRINCIPLES priority #8 明示的 — never fail-open).
// Pending users (role=pending) are redirected to /ui/pending-approval for any
// path other than /ui/pending-approval itself — they must not reach any
// functional UI until an admin promotes them.
func (a *authBundle) requireRole(min user.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok, err := a.resolvePrincipal(r)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		// Pending users may only view the approval-pending page.
		if u.Role == user.RolePending && r.URL.Path != "/ui/pending-approval" {
			http.Redirect(w, r, "/ui/pending-approval", http.StatusFound)
			return
		}
		if !roleAllows(u.Role, min) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, withPrincipal(r, u))
	})
}

// requireNoAdmin allows the wrapped handler iff zero admin users exist. Used
// by /setup so the wizard becomes inaccessible the moment the first admin is
// created (AC-S1e7eeb-3-2).
func (a *authBundle) requireNoAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := a.users.CountByRole(r.Context(), user.RoleAdmin)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if n > 0 {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// roleAllows returns true iff have satisfies the minimum want. For v1's flat
// admin/user hierarchy this collapses to: admin satisfies anything; user
// satisfies user; anyone else satisfies nothing.
func roleAllows(have, want user.Role) bool {
	switch want {
	case user.RoleAdmin:
		return have == user.RoleAdmin
	case user.RoleUser:
		return have == user.RoleAdmin || have == user.RoleUser
	default:
		return false
	}
}
