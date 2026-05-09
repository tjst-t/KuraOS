// users_crud.go: handlers backing Sfix001-1 (User CRUD) and Sfix001-2
// (Group CRUD). The HTML form posts arrive at /ui/admin/users/* —
// distinct paths per action so a non-JS browser still hits the right
// handler from the modal's <form action="...">.
//
// All projection (uid alloc, /etc/passwd rewrite, vault credential
// scrub) goes through the engine/system Engine — DESIGN_PRINCIPLES
// priority #10 forbids the UI from touching useradd/groupadd/smbpasswd
// directly. The Engine is supplied via UsersCRUDDeps and tested with a
// thin in-memory fake.
package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/kuraos-org/kura/i18n"
)

// SystemEngine is the slice of engine/system.Engine that the Users /
// Groups handlers consume. Defined here (consumer side) so the UI
// package never needs an engine/system import.
type SystemEngine interface {
	CreateUser(ctx context.Context, in SystemCreateUserInput) (string, error)
	UpdateUser(ctx context.Context, userID, displayName, role string) error
	DeleteUser(ctx context.Context, userID string) error
	CreateGroup(ctx context.Context, name, description string) (string, error)
	DeleteGroup(ctx context.Context, groupID string) error
	SetGroupMembers(ctx context.Context, groupID string, userIDs []string) error
}

// SystemCreateUserInput mirrors engine/system.CreateUserInput so the UI
// package doesn't import engine/system. Fields stay snake_case in the
// HTTP form parser, CamelCase here.
type SystemCreateUserInput struct {
	Username    string
	DisplayName string
	Password    string
	Role        string
}

// ShareACLLookup is what the Groups handler needs to enforce
// referential integrity on Group delete (Sfix001-2 AC-3). The UI passes
// in an adapter that consults engine/share.
type ShareACLLookup interface {
	// SharesUsingGroup returns the list of share names whose ACL
	// references groupName. Empty slice = safe to delete.
	SharesUsingGroup(ctx context.Context, groupName string) ([]string, error)
}

// UsersCRUDDeps wires the per-action handlers to engine/system + the
// share-acl lookup. CurrentUser is consulted to enforce the
// "admin can't delete themselves" rule (Sfix001-1 AC-2).
type UsersCRUDDeps struct {
	System         SystemEngine
	Lookup         ShareACLLookup
	UsersLister    UsersLister
	GroupsLister   GroupsLister
	CurrentUser    func(*http.Request) (id, username string, ok bool)
}

// GroupsLister is the data source the Groups tab uses. Implemented in
// cmd/kura via engine/user.Store.
type GroupsLister interface {
	ListGroups(ctx context.Context) ([]GroupRow, error)
}

// GroupRow is one entry on the Groups tab.
type GroupRow struct {
	ID          string
	Name        string
	Description string
	Members     []string // usernames
}

// SetUsersCRUDHandlers installs the Users / Groups handlers under
// /ui/admin/users/* on the renderer's mux. The handlers redirect back
// to /ui/admin/users with the operator-visible error in a query param
// (?err=...) so a fresh GET re-renders the page with the banner.
func (r *Renderer) SetUsersCRUDHandlers(d UsersCRUDDeps) {
	r.usersCreateHandler = r.usersCreate(d)
	r.usersUpdateHandler = r.usersUpdate(d)
	r.usersDeleteHandler = r.usersDelete(d)
	r.groupsCreateHandler = r.groupsCreate(d)
	r.groupsDeleteHandler = r.groupsDelete(d)
	r.groupsMembersHandler = r.groupsMembers(d)
}

func (r *Renderer) usersCreate(d UsersCRUDDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if err := req.ParseForm(); err != nil {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrCreateFailed, err.Error()))
			return
		}
		username := strings.TrimSpace(req.FormValue("username"))
		display := strings.TrimSpace(req.FormValue("display_name"))
		password := req.FormValue("password")
		role := strings.TrimSpace(req.FormValue("role"))
		if username == "" {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrUsernameRequired))
			return
		}
		if !validLocalUsername(username) {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrUsernameInvalid))
			return
		}
		if len(password) < 8 {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrPasswordShort))
			return
		}
		if role != "admin" && role != "user" {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrInvalidRole))
			return
		}
		_, err := d.System.CreateUser(req.Context(), SystemCreateUserInput{
			Username:    username,
			DisplayName: display,
			Password:    password,
			Role:        role,
		})
		if err != nil {
			msg := err.Error()
			// Map the user-facing taken-name error so the operator gets a
			// clean Japanese banner instead of the developer-side wrap.
			if strings.Contains(msg, "username already taken") {
				redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrUsernameTaken))
				return
			}
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrCreateFailed, msg))
			return
		}
		redirectUsers(w, req, "users", "")
	})
}

func (r *Renderer) usersUpdate(d UsersCRUDDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		_ = req.ParseForm()
		userID := strings.TrimSpace(req.FormValue("id"))
		display := strings.TrimSpace(req.FormValue("display_name"))
		role := strings.TrimSpace(req.FormValue("role"))
		if userID == "" {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrUpdateFailed, "missing id"))
			return
		}
		if role != "admin" && role != "user" {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrInvalidRole))
			return
		}
		if err := d.System.UpdateUser(req.Context(), userID, display, role); err != nil {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrUpdateFailed, err.Error()))
			return
		}
		redirectUsers(w, req, "users", "")
	})
}

func (r *Renderer) usersDelete(d UsersCRUDDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		_ = req.ParseForm()
		userID := strings.TrimSpace(req.FormValue("id"))
		if userID == "" {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrDeleteFailed, "missing id"))
			return
		}
		if d.CurrentUser != nil {
			if curID, _, ok := d.CurrentUser(req); ok && curID == userID {
				redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrSelfDeleteForbid))
				return
			}
		}
		// Last-admin protection: refuse the delete if the target is the
		// only admin left. We count via the lister rather than running a
		// dedicated query — the UI already has the data on the same page.
		if d.UsersLister != nil {
			if rows, err := d.UsersLister.List(req.Context()); err == nil {
				admins := 0
				targetIsAdmin := false
				for _, u := range rows {
					if u.Role == "admin" {
						admins++
						if u.UserID == userID {
							targetIsAdmin = true
						}
					}
				}
				if targetIsAdmin && admins <= 1 {
					redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrLastAdminProtect))
					return
				}
			}
		}
		if err := d.System.DeleteUser(req.Context(), userID); err != nil {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrDeleteFailed, err.Error()))
			return
		}
		redirectUsers(w, req, "users", "")
	})
}

func (r *Renderer) groupsCreate(d UsersCRUDDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		_ = req.ParseForm()
		name := strings.TrimSpace(req.FormValue("name"))
		desc := strings.TrimSpace(req.FormValue("description"))
		if !validGroupName(name) {
			redirectUsers(w, req, "groups", r.tr.T(i18n.MsgGroupsErrInvalidName))
			return
		}
		_, err := d.System.CreateGroup(req.Context(), name, desc)
		if err != nil {
			msg := err.Error()
			if strings.Contains(msg, "group name already taken") {
				redirectUsers(w, req, "groups", r.tr.T(i18n.MsgGroupsErrNameTaken))
				return
			}
			redirectUsers(w, req, "groups", r.tr.T(i18n.MsgGroupsErrCreateFailed, msg))
			return
		}
		redirectUsers(w, req, "groups", "")
	})
}

func (r *Renderer) groupsDelete(d UsersCRUDDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		_ = req.ParseForm()
		groupID := strings.TrimSpace(req.FormValue("id"))
		groupName := strings.TrimSpace(req.FormValue("name"))
		if groupID == "" {
			redirectUsers(w, req, "groups", r.tr.T(i18n.MsgGroupsErrDeleteFailed, "missing id"))
			return
		}
		// Referential check: refuse if any Share ACL still names the
		// group. (Sfix001-2 AC-3 / DESIGN_PRINCIPLES priority #5
		// 信頼性 > 機能.)
		if d.Lookup != nil && groupName != "" {
			users, err := d.Lookup.SharesUsingGroup(req.Context(), groupName)
			if err == nil && len(users) > 0 {
				redirectUsers(w, req, "groups", r.tr.T(i18n.MsgGroupsErrInUseShares, strings.Join(users, ", ")))
				return
			}
		}
		if err := d.System.DeleteGroup(req.Context(), groupID); err != nil {
			redirectUsers(w, req, "groups", r.tr.T(i18n.MsgGroupsErrDeleteFailed, err.Error()))
			return
		}
		redirectUsers(w, req, "groups", "")
	})
}

func (r *Renderer) groupsMembers(d UsersCRUDDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		_ = req.ParseForm()
		groupID := strings.TrimSpace(req.FormValue("id"))
		if groupID == "" {
			redirectUsers(w, req, "groups", r.tr.T(i18n.MsgGroupsErrSetMembersFailed, "missing id"))
			return
		}
		members := req.Form["member"]
		// member="" is sent as the placeholder when the operator
		// unchecked every box; filter it out so we don't try to insert
		// an empty user_id row.
		clean := make([]string, 0, len(members))
		for _, m := range members {
			if m = strings.TrimSpace(m); m != "" {
				clean = append(clean, m)
			}
		}
		if err := d.System.SetGroupMembers(req.Context(), groupID, clean); err != nil {
			redirectUsers(w, req, "groups", r.tr.T(i18n.MsgGroupsErrSetMembersFailed, err.Error()))
			return
		}
		redirectUsers(w, req, "groups", "")
	})
}

// redirectUsers always returns to /ui/admin/users with the requested
// tab and an optional error banner. POST-Redirect-GET keeps the
// browser back button safe and lets the page re-render with the
// freshly mutated state.
func redirectUsers(w http.ResponseWriter, req *http.Request, tab, formError string) {
	q := url.Values{}
	if tab != "" {
		q.Set("tab", tab)
	}
	if formError != "" {
		q.Set("err", formError)
	}
	target := "/ui/admin/users"
	if encoded := q.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, req, target, http.StatusSeeOther)
}

// validLocalUsername mirrors the constraints engine/user accepts:
// lowercase ASCII letters/digits/_/- only, 1-32 characters. Same as
// the storage layer but checked early so the UI gives a precise
// translated message rather than the developer-side error wrap.
func validLocalUsername(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// validGroupName matches engine/user.validGroupName. Re-implemented
// here so the UI can validate before hitting engine/system.
func validGroupName(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

