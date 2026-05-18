// setup.go owns the full setup wizard handler (S99702c-2).
// The wizard spans 5 steps: admin (step 1) → welcome (step 2) →
// storage (step 3) → share (step 4) → done (step 5).
// The /setup and /setup/admin routes are gated by requireNoAdmin middleware.
// The /setup/welcome, /setup/storage-config, /setup/share-config, /setup/done
// routes require any authenticated session (admin cookie from step 1 suffices).
package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/share"
	"github.com/kuraos-org/kura/engine/storage"
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
	// Storage provides disk listing + pool creation for Step 3.
	// Nil when the storage engine is not wired.
	Storage storage.Engine
	// Shares provides share creation for Step 4.
	// Nil when the share engine is not wired.
	Shares SetupShareCreator
}

// SetupUserStore is the slice of *user.Store the wizard needs.
type SetupUserStore interface {
	CountByRole(ctx context.Context, role user.Role) (int, error)
	CreateLocalUser(ctx context.Context, username, displayName, password string, role user.Role) (user.User, error)
}

// SetupShareCreator is the slim surface the wizard's share step needs.
type SetupShareCreator interface {
	Create(ctx context.Context, in share.CreateInput) (share.Share, error)
}

// usernameRule constrains usernames to characters that are safe across
// downstream targets. Letters/digits/underscore/hyphen, 2-32 chars,
// must start with a letter.
var usernameRule = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,31}$`)

// minPasswordLen — wizard password rule per NIST SP 800-63B-4 §5.1.1.
const minPasswordLen = 8

// setupPageData is passed as PageData.Extra to setup.tmpl.
type setupPageData struct {
	// Step is the active wizard step (1-5).
	Step int
	// Step 1 fields.
	ErrorMessage string
	Username     string
	DisplayName  string
	// Step 2 fields.
	DiskCount int
	IPAddress string
	// Step 3 fields.
	Recommendation *storage.RecommendedPool
	StorageError   string
	// Step 4 fields.
	ShareError string
	ShareName  string
	// Step 5 fields.
	PoolCreated  string
	ShareCreated string
}

// SetupHandler returns the http.Handler that owns /setup (GET) and
// /setup/admin (POST). Mount under requireNoAdmin so it disappears once
// an admin exists.
func (r *Renderer) SetupHandler(d SetupDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/setup", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		r.renderSetupPage(w, req, setupPageData{Step: 1})
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

// SetupWizardRoutes mounts the authenticated wizard steps (2-5) on the
// provided mux. These routes must be protected by requireAnySession middleware
// so only the logged-in admin can access them. Called from the gateway after
// requireNoAdmin routes are already registered.
func (r *Renderer) SetupWizardRoutes(mux *http.ServeMux, d SetupDeps) {
	// Step 2: welcome overview
	mux.HandleFunc("/setup/welcome", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		extra := setupPageData{Step: 2, IPAddress: "—"}
		if d.Storage != nil {
			disks, err := d.Storage.ListDisks(req.Context())
			if err == nil {
				extra.DiskCount = len(disks)
			}
		}
		r.renderSetupPage(w, req, extra)
	})
	// /setup/next: generic POST-redirect-GET for inter-step navigation.
	mux.HandleFunc("/setup/next", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Redirect(w, req, "/setup/welcome", http.StatusFound)
			return
		}
		from := req.PostForm.Get("from_step")
		pool := req.PostForm.Get("pool")
		switch from {
		case "2":
			http.Redirect(w, req, "/setup/storage-config", http.StatusFound)
		case "3":
			target := "/setup/share-config"
			if pool != "" {
				target += "?pool=" + url.QueryEscape(pool)
			}
			http.Redirect(w, req, target, http.StatusFound)
		case "4":
			target := "/setup/done"
			if pool != "" {
				target += "?pool=" + url.QueryEscape(pool)
			}
			http.Redirect(w, req, target, http.StatusFound)
		default:
			http.Redirect(w, req, "/setup/welcome", http.StatusFound)
		}
	})
	// Step 3: storage recommendation
	mux.HandleFunc("/setup/storage-config", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			r.handleSetupStorage(w, req, d)
			return
		}
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		extra := setupPageData{Step: 3}
		if d.Storage != nil {
			disks, err := d.Storage.ListDisks(req.Context())
			if err == nil {
				extra.Recommendation = storage.RecommendTopology(disks)
			}
		}
		r.renderSetupPage(w, req, extra)
	})
	// Step 4: first share
	mux.HandleFunc("/setup/share-config", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			r.handleSetupShare(w, req, d)
			return
		}
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		pool := req.URL.Query().Get("pool")
		extra := setupPageData{Step: 4, ShareName: "documents"}
		_ = pool
		r.renderSetupPage(w, req, extra)
	})
	// Step 5: completion screen
	mux.HandleFunc("/setup/done", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		q := req.URL.Query()
		extra := setupPageData{
			Step:         5,
			PoolCreated:  q.Get("pool"),
			ShareCreated: q.Get("share"),
		}
		r.renderSetupPage(w, req, extra)
	})
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
		r.renderSetupPage(w, req, setupPageData{Step: 1, ErrorMessage: r.tr.T(i18n.MsgLoginGenericError)})
		return
	}
	username := strings.TrimSpace(req.PostForm.Get("username"))
	displayName := strings.TrimSpace(req.PostForm.Get("display_name"))
	password := req.PostForm.Get("password")
	confirm := req.PostForm.Get("password_confirm")

	// Re-check no-admin invariant inside POST to prevent race.
	n, err := d.Users.CountByRole(req.Context(), user.RoleAdmin)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		r.renderSetupPage(w, req, setupPageData{Step: 1, ErrorMessage: r.tr.T(i18n.MsgLoginGenericError)})
		return
	}
	if n > 0 {
		http.Redirect(w, req, "/login", http.StatusFound)
		return
	}

	switch {
	case !usernameRule.MatchString(username):
		w.WriteHeader(http.StatusBadRequest)
		r.renderSetupPage(w, req, setupPageData{
			Step: 1, ErrorMessage: r.tr.T(i18n.MsgSetupAdminUsernameRule),
			Username: username, DisplayName: displayName,
		})
		return
	case len(password) < minPasswordLen:
		w.WriteHeader(http.StatusBadRequest)
		r.renderSetupPage(w, req, setupPageData{
			Step: 1, ErrorMessage: r.tr.T(i18n.MsgSetupAdminPasswordShort),
			Username: username, DisplayName: displayName,
		})
		return
	case password != confirm:
		w.WriteHeader(http.StatusBadRequest)
		r.renderSetupPage(w, req, setupPageData{
			Step: 1, ErrorMessage: r.tr.T(i18n.MsgSetupAdminPasswordMatch),
			Username: username, DisplayName: displayName,
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
			Step: 1, ErrorMessage: msg,
			Username: username, DisplayName: displayName,
		})
		return
	}

	sess, err := d.Sessions.Issue(req.Context(), u.ID, req.UserAgent(), req.RemoteAddr)
	if err != nil {
		http.Redirect(w, req, "/login", http.StatusFound)
		return
	}
	setSessionCookie(w, sess.ID, ttlOr(d.Sessions.TTL(), 24*time.Hour), d.SecureCookies)
	// After admin creation → step 2 (welcome overview).
	http.Redirect(w, req, "/setup/welcome", http.StatusFound)
}

// handleSetupStorage applies the pool configuration from the form.
func (r *Renderer) handleSetupStorage(w http.ResponseWriter, req *http.Request, d SetupDeps) {
	if err := req.ParseForm(); err != nil {
		http.Redirect(w, req, "/setup/storage-config", http.StatusFound)
		return
	}
	poolName := strings.TrimSpace(req.PostForm.Get("pool_name"))
	topology := storage.VdevLayout(req.PostForm.Get("topology"))
	dataDisks := req.PostForm["data_disks"]
	specialTopology := storage.VdevLayout(req.PostForm.Get("special_topology"))
	specialDisks := req.PostForm["special_disks"]

	if d.Storage != nil && poolName != "" && topology != "" && len(dataDisks) > 0 {
		cfg := storage.PoolConfig{
			Name: poolName,
			Data: storage.VdevSpec{
				Layout: topology,
				Disks:  dataDisks,
			},
		}
		if specialTopology != "" && len(specialDisks) >= 2 {
			cfg.Special = &storage.VdevSpec{
				Layout: specialTopology,
				Disks:  specialDisks,
			}
		}
		if err := d.Storage.CreatePool(req.Context(), cfg); err != nil {
			extra := setupPageData{Step: 3, StorageError: err.Error()}
			if disks, derr := d.Storage.ListDisks(req.Context()); derr == nil {
				extra.Recommendation = storage.RecommendTopology(disks)
			}
			r.renderSetupPage(w, req, extra)
			return
		}
	}
	target := "/setup/share-config"
	if poolName != "" {
		target += "?pool=" + url.QueryEscape(poolName)
	}
	http.Redirect(w, req, target, http.StatusFound)
}

// handleSetupShare creates the initial share from the form.
func (r *Renderer) handleSetupShare(w http.ResponseWriter, req *http.Request, d SetupDeps) {
	if err := req.ParseForm(); err != nil {
		http.Redirect(w, req, "/setup/share-config", http.StatusFound)
		return
	}
	shareName := strings.TrimSpace(req.PostForm.Get("share_name"))
	presetStr := req.PostForm.Get("preset")
	pool := req.PostForm.Get("pool")
	if pool == "" {
		pool = "tank"
	}

	if d.Shares != nil && shareName != "" {
		preset := share.Preset(presetStr)
		if preset == "" {
			preset = share.PresetGeneral
		}
		path := "/mnt/" + pool + "/" + shareName
		in := share.CreateInput{
			Name:       shareName,
			Path:       path,
			Protocol:   share.ProtocolSMB,
			Preset:     preset,
			AccessMode: share.AccessReadWrite,
		}
		if _, err := d.Shares.Create(req.Context(), in); err != nil {
			extra := setupPageData{Step: 4, ShareError: err.Error(), ShareName: shareName}
			r.renderSetupPage(w, req, extra)
			return
		}
	}
	target := "/setup/done"
	q := url.Values{}
	if pool != "tank" {
		q.Set("pool", pool)
	}
	if shareName != "" {
		q.Set("share", shareName)
	}
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	http.Redirect(w, req, target, http.StatusFound)
}

// ttlOr returns d if positive, otherwise fallback.
func ttlOr(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

var _ = session.CookieName
