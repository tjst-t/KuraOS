package ui

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/kuraos-org/kura/i18n"
)

// UsersDeps wires the data sources the Users page needs. All fields are
// optional — nil values render the corresponding section as an empty
// state, which matches what a fresh install looks like before any users,
// federations, or OIDC clients exist.
type UsersDeps struct {
	Users        UsersLister
	Groups       GroupsLister
	Federations  FederationLister
	OIDCClients  OIDCClientLister
	Providers    ProviderLister
	// CurrentUser resolves the authenticated session to (user_id,
	// username). Used to render the "Google を紐付け" button only on
	// the row of the logged-in user — every row had it before, and
	// /federation/google/link always binds to the session user, so
	// clicking a different row's button silently bound the wrong
	// account (2026-05-12 incident). Nil = hide all link buttons.
	CurrentUser func(*http.Request) (id, username string, ok bool)
}

// UsersLister returns the operator-managed user accounts. Implemented in
// cmd/kura by wrapping engine/user.Store.
type UsersLister interface {
	List(ctx context.Context) ([]UsersUserRow, error)
}

// UsersUserRow is the row data each Users 画面 list entry needs.
type UsersUserRow struct {
	UserID      string
	Username    string
	DisplayName string
	Role        string
	Methods     []string
	Groups      []string
	LastLogin   string
}

// FederationLister returns the (provider, user_id) -> link rows so the
// Users 画面 can render badges next to each user.
type FederationLister interface {
	ListFor(ctx context.Context, userID string) ([]UsersFederationRow, error)
}

// UsersFederationRow is one external IdP binding for a user.
type UsersFederationRow struct {
	Provider string
	Subject  string
	Email    string
	LinkedAt time.Time
}

// OIDCClientLister returns the auto-registered RP clients (one per
// installed app with auth.mode=oidc).
type OIDCClientLister interface {
	ListClients(ctx context.Context) ([]UsersOIDCClient, error)
}

// UsersOIDCClient is one row in the OIDC clients tab.
type UsersOIDCClient struct {
	ClientID     string
	Name         string
	RedirectURIs []string
	Issuer       string
	CreatedAt    time.Time
}

// ProviderLister returns the registered federation providers (Google,
// GitHub, Microsoft) so the Auth tab can render their toggles.
type ProviderLister interface {
	ListProviders(ctx context.Context) ([]UsersProvider, error)
}

// UsersProvider is one external IdP entry.
type UsersProvider struct {
	Name          string
	Enabled       bool
	ClientID      string
	AutoProvision bool
}

// UsersView is what the users.tmpl template renders. Built from the
// UsersDeps slices.
type UsersView struct {
	Tab          string
	UsersHeading string
	// PendingUsers are accounts with role=pending awaiting admin approval.
	// The template hides the pending section when this is empty.
	PendingUsers []UsersViewUser
	// Users are active (non-pending) accounts.
	Users        []UsersViewUser
	Groups       []UsersViewGroup
	Providers    []UsersProvider
	OIDCClients  []UsersOIDCClient
	Subtitle     string
	Issuer       string

	// FormError carries the result of a previous /ui/admin/users/* POST.
	// Populated from the ?err= query param so the page can display a
	// translated banner without keeping per-session form-error state.
	FormError string

	// AllUserRows is the universe of selectable users for the member
	// edit modal. Same data as Users, filtered/projected for the picker.
	AllUserRows []UsersViewUser

	// CurrentUserID is the authenticated session's user_id. Used by
	// the template to render the link button only on the matching
	// user's row (the user can only bind their own Google account).
	CurrentUserID string
}

// UsersViewUser is the per-user row the template iterates over.
type UsersViewUser struct {
	UserID      string
	Username    string
	DisplayName string
	Initials    string
	Role        string
	IsAdmin     bool
	Methods     []string
	Groups      []string
	LastLogin   string
	Federations []UsersFederationRow
}

// UsersViewGroup is the per-group row.
type UsersViewGroup struct {
	ID          string
	Name        string
	Members     int
	MemberIDs   []string // for the edit-members modal pre-check
	Description string
}

// SetUsersHandler installs the /ui/admin/users handler.
func (r *Renderer) SetUsersHandler(d UsersDeps) {
	r.usersHandler = r.usersPage(d)
}

// usersPage renders the Users page. Renders all four tabs in one
// response and lets the template hide-by-default; in v1 the tab state
// is a query param that the template inspects.
func (r *Renderer) usersPage(d UsersDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		ctx := req.Context()
		view := &UsersView{
			Tab:       activeTab(req.URL.Query().Get("tab"), "users"),
			Subtitle:  r.tr.T(i18n.MsgUsersSubtitle),
			Issuer:    "auto-generated",
			FormError: req.URL.Query().Get("err"),
		}
		if d.CurrentUser != nil {
			if id, _, ok := d.CurrentUser(req); ok {
				view.CurrentUserID = id
			}
		}
		if d.Users != nil {
			users, _ := d.Users.List(ctx)
			view.Users = make([]UsersViewUser, 0, len(users))
			view.PendingUsers = make([]UsersViewUser, 0)
			for _, u := range users {
				row := UsersViewUser{
					UserID:      u.UserID,
					Username:    u.Username,
					DisplayName: u.DisplayName,
					Initials:    initials(u.Username),
					Role:        u.Role,
					IsAdmin:     u.Role == "admin",
					Methods:     u.Methods,
					Groups:      u.Groups,
					LastLogin:   u.LastLogin,
				}
				if d.Federations != nil {
					links, _ := d.Federations.ListFor(ctx, u.UserID)
					row.Federations = links
					for _, l := range links {
						if !contains(row.Methods, l.Provider) {
							row.Methods = append(row.Methods, l.Provider)
						}
					}
				}
				if u.Role == "pending" {
					view.PendingUsers = append(view.PendingUsers, row)
				} else {
					view.Users = append(view.Users, row)
				}
			}
			sort.Slice(view.Users, func(i, j int) bool {
				return view.Users[i].Username < view.Users[j].Username
			})
			sort.Slice(view.PendingUsers, func(i, j int) bool {
				return view.PendingUsers[i].Username < view.PendingUsers[j].Username
			})
			// AllUserRows excludes pending users — pending users have no credentials
			// yet and cannot be assigned to groups.
			view.AllUserRows = view.Users
		}
		if d.Providers != nil {
			view.Providers, _ = d.Providers.ListProviders(ctx)
		}
		if d.OIDCClients != nil {
			view.OIDCClients, _ = d.OIDCClients.ListClients(ctx)
		}
		// Real groups data when the engine adapter is wired; otherwise
		// fall through to the synthetic admins row so older acceptance
		// tests that don't supply a GroupsLister still pass.
		if d.Groups != nil {
			groups, _ := d.Groups.ListGroups(ctx)
			view.Groups = make([]UsersViewGroup, 0, len(groups))
			usernameByID := map[string]string{}
			for _, u := range view.Users {
				usernameByID[u.UserID] = u.Username
			}
			for _, g := range groups {
				memberIDs := make([]string, 0, len(g.Members))
				memberNames := make([]string, 0, len(g.Members))
				for _, mid := range g.Members {
					memberIDs = append(memberIDs, mid)
					if name, ok := usernameByID[mid]; ok {
						memberNames = append(memberNames, name)
					}
				}
				_ = memberNames
				view.Groups = append(view.Groups, UsersViewGroup{
					ID:          g.ID,
					Name:        g.Name,
					Members:     len(g.Members),
					MemberIDs:   memberIDs,
					Description: g.Description,
				})
			}
			// Annotate users with the group names they belong to so the
			// users tab can render the "Groups" column without the
			// caller computing it. Cheap O(n*m) — n / m are tiny.
			for i := range view.Users {
				if len(view.Users[i].Groups) > 0 {
					continue
				}
				var names []string
				for _, g := range groups {
					for _, mid := range g.Members {
						if mid == view.Users[i].UserID {
							names = append(names, g.Name)
							break
						}
					}
				}
				view.Users[i].Groups = names
			}
		} else {
			view.Groups = []UsersViewGroup{
				{Name: "admins", Members: countAdmins(view.Users), Description: "システム管理者"},
			}
		}

		title := r.tr.T(i18n.MsgUsersTitle)
		data := PageData{
			Locale:      r.tr.Locale(),
			Version:     r.version,
			PageTitle:   title,
			PageTitleID: string(i18n.MsgUsersTitle),
			Sidebar:     SidebarData{Groups: adminNavGroups("users")},
			User:        UserData{Name: "admin", Initials: "AD", RoleID: string(i18n.MsgRoleAdmin)},
			Breadcrumbs: []Crumb{{Label: r.tr.T(i18n.MsgBrandName)}, {Label: title, Last: true}},
			Extra:       view,
		}
		r.render(w, "templates/pages/users.tmpl", data)
	})
}

func activeTab(raw, fallback string) string {
	switch raw {
	case "users", "groups", "auth", "oidc":
		return raw
	default:
		return fallback
	}
}

func initials(name string) string {
	if len(name) == 0 {
		return "?"
	}
	if len(name) == 1 {
		return string(byte(name[0]&^32)) // upper
	}
	return string(byte(name[0]&^32)) + string(byte(name[1]&^32))
}

func contains(set []string, want string) bool {
	for _, s := range set {
		if s == want {
			return true
		}
	}
	return false
}

func countAdmins(users []UsersViewUser) int {
	n := 0
	for _, u := range users {
		if u.IsAdmin {
			n++
		}
	}
	return n
}
