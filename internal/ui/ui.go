// Package ui renders the htmx + html/template admin shell.
//
// Templates and the Tailwind-compiled stylesheet are embedded so the kura
// binary stays self-contained. The visual SSOT is prototype/claude_design/;
// templates here mirror that prototype's HTML structure and CSS class
// vocabulary verbatim — design changes flow through the prototype, not
// through these files.
package ui

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/kuraos-org/kura/i18n"
)

//go:embed templates/layouts/*.tmpl templates/partials/*.tmpl templates/pages/*.tmpl
var templatesFS embed.FS

//go:embed dist/*
var distFS embed.FS

// Renderer owns the parsed template tree. Templates are parsed once at
// construction and reused per request — html/template is safe for concurrent
// Execute calls.
type Renderer struct {
	tr        *i18n.Translator
	version   string
	templates *template.Template
	// tplDispatch is a clone of `templates` used by the {{ tpl ... }} template
	// func to execute named body/foot templates from inside the modal partial.
	// Executing on the master would set its escape state and break subsequent
	// Clone() calls in renderToBuffer (Go html/template's documented contract:
	// you cannot Clone a template after it has been executed). Cloning once
	// at New() time and reusing the dispatch tree across requests is safe —
	// Execute is concurrency-safe and may be called repeatedly.
	tplDispatch *template.Template
	// storageHandler, when non-nil, replaces the storage placeholder route.
	// Set via Renderer.SetStorageHandler before Routes() is called. Keeping
	// this as an optional field rather than a constructor parameter lets the
	// auth-only acceptance tests (S1e7eeb) keep working unchanged.
	storageHandler http.Handler
	// storageWriteHandler is the optional POST surface (create pool /
	// snapshot / import). Wired by SetStorageWriteHandler. Routes mount it
	// under /ui/admin/storage/* without the read route ever being able to
	// shadow it (specific paths win in the storage write mux).
	storageWriteHandler http.Handler
	// sharesHandler / sharesDeleteHandler / sharesUpdateHandler — installed
	// via SetSharesHandlers. When unset, the route falls through to the
	// generic "shares" placeholder (lets older acceptance tests keep passing
	// while shares work lands).
	sharesHandler       http.Handler
	sharesDeleteHandler http.Handler
	sharesUpdateHandler http.Handler

	// Apps handlers (added in S65b510). Wired via SetAppsHandler.
	appsListHandler         http.Handler
	appsInstallFormHandler  http.Handler
	appsInstallStartHandler http.Handler
	appsStreamHandler       http.Handler
	appsUninstallHandler    http.Handler

	// Users handler (added in S822961). Wired via SetUsersHandler.
	usersHandler http.Handler
}

// New parses every embedded template into a single tree so {{ template ... }}
// references between files resolve. The translator is wired into the tree's
// FuncMap so templates can call {{ T "msg.id" }} directly.
func New(tr *i18n.Translator, version string) (*Renderer, error) {
	if tr == nil {
		return nil, fmt.Errorf("ui: translator is nil")
	}
	r := &Renderer{tr: tr, version: version}
	funcs := template.FuncMap{
		"T": func(id string, args ...any) string {
			return tr.T(i18n.MessageID(id), args...)
		},
		// dict packs k/v pairs into a map so partial templates ({{ template ... }})
		// can receive multiple named arguments (Go html/template only passes one
		// pipeline value to a partial).
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict requires an even number of arguments")
			}
			m := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key %d must be a string, got %T", i, values[i])
				}
				m[key] = values[i+1]
			}
			return m, nil
		},
		// tpl renders the named template with the given data and returns the
		// result as already-escaped HTML. Used by partials/modal.tmpl to
		// dispatch on caller-supplied body/foot template names — Go html/template
		// requires literal template names in {{ template }}, so we route
		// through ExecuteTemplate at call time. Crucially we execute on
		// tplDispatch (a clone of master), not on r.templates: executing
		// the master would mark it escaped and break the next request's
		// Clone() in renderToBuffer.
		"tpl": func(name string, data any) (template.HTML, error) {
			if r.tplDispatch == nil {
				return "", fmt.Errorf("tpl: dispatch tree not ready")
			}
			var buf bytes.Buffer
			if err := r.tplDispatch.ExecuteTemplate(&buf, name, data); err != nil {
				return "", fmt.Errorf("tpl %q: %w", name, err)
			}
			return template.HTML(buf.String()), nil
		},
		// safeJS marks a translated string as safe to interpolate inside a
		// <script>'s JSON-island string literal. The value is JSON-escaped
		// with Go's encoding/json so backslashes and quotes are handled
		// uniformly. Use sparingly — only inside known-safe contexts where
		// you control the surrounding quoting.
		"safeJS": func(s string) (template.JS, error) {
			b, err := json.Marshal(s)
			if err != nil {
				return "", err
			}
			return template.JS(b), nil
		},
	}
	t := template.New("kura").Funcs(funcs)

	files, err := collectTemplates()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		raw, err := fs.ReadFile(templatesFS, f)
		if err != nil {
			return nil, fmt.Errorf("ui: read template %q: %w", f, err)
		}
		// Use the file's relative path as the template name so duplicates are
		// surfaced as "redefinition" errors at parse time rather than silently
		// overwriting each other at execution time.
		if _, err := t.New(f).Parse(string(raw)); err != nil {
			return nil, fmt.Errorf("ui: parse %q: %w", f, err)
		}
	}
	r.templates = t
	// Cache a clone for the `tpl` func to execute against. See the field
	// comment on Renderer.tplDispatch for the rationale.
	dispatch, err := t.Clone()
	if err != nil {
		return nil, fmt.Errorf("ui: clone dispatch tree: %w", err)
	}
	r.tplDispatch = dispatch
	return r, nil
}

func collectTemplates() ([]string, error) {
	var out []string
	err := fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".tmpl") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ui: walk templates: %w", err)
	}
	return out, nil
}

// NavItem is one entry in the sidebar.
type NavItem struct {
	ID      string
	LabelID string
	Icon    string
	Href    string
	Badge   string
	Active  bool
}

// NavGroup groups related sidebar items under a section heading.
type NavGroup struct {
	LabelID string
	Items   []NavItem
}

type SidebarData struct {
	Groups []NavGroup
}

type UserData struct {
	Name     string
	Initials string
	RoleID   string
}

type Crumb struct {
	Label string
	Last  bool
}

// PageData is what every admin template receives. Page-specific extras hang
// off Extra so renderPage stays generic.
type PageData struct {
	Locale      string
	Version     string
	PageTitle   string
	PageTitleID string
	Sidebar     SidebarData
	User        UserData
	Breadcrumbs []Crumb
	Extra       any
}

// SetStorageHandler installs h as the /ui/admin/storage route. Must be called
// before Routes(); calling it after has no effect because the mux has
// already snapshotted the field. Wiring the handler this way (rather than
// passing it to Routes) keeps the signature of Routes stable across sprints.
func (r *Renderer) SetStorageHandler(h http.Handler) { r.storageHandler = h }

// SetStorageWriteHandler installs the POST surface (create pool / snapshot /
// import). Must be called before Routes(). Optional — when nil, mutation
// routes are simply not registered.
func (r *Renderer) SetStorageWriteHandler(h http.Handler) { r.storageWriteHandler = h }

// SetSharesHandlers installs the GET/POST + delete handlers for /ui/admin/shares.
// Must be called before Routes(). Optional — when nil the placeholder
// handler is mounted, matching the behaviour of older sprints.
func (r *Renderer) SetSharesHandlers(get, del http.Handler) {
	r.sharesHandler = get
	r.sharesDeleteHandler = del
}

// SetSharesUpdateHandler installs the POST handler for /ui/admin/shares/update.
// Optional — older sprints (before the share-edit feature) leave it nil.
func (r *Renderer) SetSharesUpdateHandler(h http.Handler) { r.sharesUpdateHandler = h }

// adminNavGroups returns the sidebar definition with the active item set.
// IDs match the prototype's NAV_GROUPS so test fixtures and screen
// references line up. The 7-item admin row (Dashboard / Storage / Shares /
// Users / Network / Apps / Settings) is fixed by AC-S464e47-1-1.
func adminNavGroups(activeID string) []NavGroup {
	items := []NavItem{
		{ID: "dashboard", LabelID: string(i18n.MsgNavDashboard), Icon: "dashboard", Href: "/ui/admin/dashboard"},
		{ID: "storage", LabelID: string(i18n.MsgNavStorage), Icon: "storage", Href: "/ui/admin/storage"},
		{ID: "shares", LabelID: string(i18n.MsgNavShares), Icon: "share", Href: "/ui/admin/shares"},
		{ID: "users", LabelID: string(i18n.MsgNavUsers), Icon: "users", Href: "/ui/admin/users"},
		{ID: "network", LabelID: string(i18n.MsgNavNetwork), Icon: "network", Href: "/ui/admin/network"},
		{ID: "apps", LabelID: string(i18n.MsgNavApps), Icon: "apps", Href: "/ui/admin/apps"},
		{ID: "settings", LabelID: string(i18n.MsgNavSettings), Icon: "settings", Href: "/ui/admin/settings"},
	}
	for i := range items {
		items[i].Active = items[i].ID == activeID
	}
	return []NavGroup{{LabelID: string(i18n.MsgNavSectionAdmin), Items: items}}
}

// Routes returns the http.Handler for everything under /ui/admin/* plus
// /ui/static/*. The caller mounts this on its top-level router.
func (r *Renderer) Routes() http.Handler {
	mux := http.NewServeMux()

	// Static assets (kura.css, htmx). Served from embedded dist/.
	staticFS, err := fs.Sub(distFS, "dist")
	if err == nil {
		mux.Handle("/ui/static/", http.StripPrefix("/ui/static/", http.FileServer(http.FS(staticFS))))
	}

	// Page handlers.
	mux.HandleFunc("/ui/admin", r.redirectToDashboard)
	mux.HandleFunc("/ui/admin/", r.redirectToDashboard)
	mux.HandleFunc("/ui/admin/dashboard", r.handleDashboard)
	if r.storageHandler != nil {
		mux.Handle("/ui/admin/storage", r.storageHandler)
	} else {
		mux.HandleFunc("/ui/admin/storage", r.handlePlaceholder("storage", i18n.MsgNavStorage))
	}
	if r.storageWriteHandler != nil {
		// Specific routes win over the read handler at /ui/admin/storage.
		mux.Handle("/ui/admin/storage/pools", r.storageWriteHandler)
		mux.Handle("/ui/admin/storage/import", r.storageWriteHandler)
		mux.Handle("/ui/admin/storage/volumes", r.storageWriteHandler)
		mux.Handle("/ui/admin/storage/quota", r.storageWriteHandler)
		mux.Handle("/ui/admin/storage/snapshots", r.storageWriteHandler)
		mux.Handle("/ui/admin/storage/snapshots/rollback", r.storageWriteHandler)
		mux.Handle("/ui/admin/storage/pools/destroy", r.storageWriteHandler)
		mux.Handle("/ui/admin/storage/volumes/destroy", r.storageWriteHandler)
		mux.Handle("/ui/admin/storage/snapshots/destroy", r.storageWriteHandler)
	}
	if r.sharesHandler != nil {
		mux.Handle("/ui/admin/shares", r.sharesHandler)
	} else {
		mux.HandleFunc("/ui/admin/shares", r.handlePlaceholder("shares", i18n.MsgNavShares))
	}
	if r.sharesDeleteHandler != nil {
		mux.Handle("/ui/admin/shares/delete", r.sharesDeleteHandler)
	}
	if r.sharesUpdateHandler != nil {
		mux.Handle("/ui/admin/shares/update", r.sharesUpdateHandler)
	}
	if r.usersHandler != nil {
		mux.Handle("/ui/admin/users", r.usersHandler)
	} else {
		mux.HandleFunc("/ui/admin/users", r.handlePlaceholder("users", i18n.MsgNavUsers))
	}
	mux.HandleFunc("/ui/admin/network", r.handlePlaceholder("network", i18n.MsgNavNetwork))
	if r.appsListHandler != nil {
		mux.Handle("/ui/admin/apps", r.appsListHandler)
	} else {
		mux.HandleFunc("/ui/admin/apps", r.handlePlaceholder("apps", i18n.MsgNavApps))
	}
	if r.appsInstallFormHandler != nil {
		mux.Handle("/ui/admin/apps/install/form", r.appsInstallFormHandler)
	}
	if r.appsInstallStartHandler != nil {
		mux.Handle("/ui/admin/apps/install/start", r.appsInstallStartHandler)
	}
	if r.appsStreamHandler != nil {
		mux.Handle("/ui/admin/apps/stream", r.appsStreamHandler)
	}
	if r.appsUninstallHandler != nil {
		mux.Handle("/ui/admin/apps/uninstall", r.appsUninstallHandler)
	}
	mux.HandleFunc("/ui/admin/settings", r.handlePlaceholder("settings", i18n.MsgNavSettings))

	// /ui — user-portal landing. The portal proper is built out in a later
	// sprint; for S1e7eeb this is a tiny stub so role-user accounts have a
	// 200 OK landing page that the auth middleware can permit.
	mux.HandleFunc("/ui", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/ui" {
			http.NotFound(w, req)
			return
		}
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!doctype html><html lang="ja"><head>` +
			`<meta charset="utf-8"><title>KuraOS</title>` +
			`<link rel="stylesheet" href="/ui/static/kura.css"></head>` +
			`<body><div class="center-shell"><div class="card"><div class="card-body">` +
			`<h1 class="page-title">` + r.tr.T(i18n.MsgBrandName) + `</h1>` +
			`<p class="page-sub">` + r.tr.T(i18n.MsgDashboardSubtitle) + `</p>` +
			`</div></div></div></body></html>`))
	})

	return mux
}

func (r *Renderer) redirectToDashboard(w http.ResponseWriter, req *http.Request) {
	http.Redirect(w, req, "/ui/admin/dashboard", http.StatusFound)
}

func (r *Renderer) handleDashboard(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	data := r.buildPageData("dashboard", i18n.MsgNavDashboard)
	r.render(w, "templates/pages/dashboard.tmpl", data)
}

func (r *Renderer) handlePlaceholder(activeID string, titleID i18n.MessageID) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		data := r.buildPageData(activeID, titleID)
		r.render(w, "templates/pages/placeholder.tmpl", data)
	}
}

func (r *Renderer) buildPageData(activeID string, titleID i18n.MessageID) PageData {
	title := r.tr.T(titleID)
	return PageData{
		Locale:      r.tr.Locale(),
		Version:     r.version,
		PageTitle:   title,
		PageTitleID: string(titleID),
		Sidebar:     SidebarData{Groups: adminNavGroups(activeID)},
		User:        UserData{Name: "admin", Initials: "AD", RoleID: string(i18n.MsgRoleAdmin)},
		Breadcrumbs: []Crumb{
			{Label: r.tr.T(i18n.MsgBrandName)},
			{Label: title, Last: true},
		},
	}
}

// render executes the admin layout against the supplied page template and
// writes 200 OK on success. The body is buffered first so a template error
// produces a clean 500 with no partial output.
func (r *Renderer) render(w http.ResponseWriter, pageTemplate string, data PageData) {
	body, err := r.renderToBuffer("templates/layouts/admin.tmpl", pageTemplate, data)
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// renderWithLayout executes layoutTemplate without writing the status code —
// the caller is expected to have called WriteHeader first when the response
// is non-200 (auth.go / setup.go re-render forms with 400/401/422). On
// template error this still writes a 500 via http.Error, which logs a
// "superfluous WriteHeader" warning if the caller had already written a
// status; that warning is acceptable in the failure path.
func (r *Renderer) renderWithLayout(w http.ResponseWriter, layoutTemplate, pageTemplate string, data PageData) {
	body, err := r.renderToBuffer(layoutTemplate, pageTemplate, data)
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

// renderToBuffer is the shared core: clone the master template tree, re-parse
// the page template into the clone (so concurrent requests can't race to
// redefine names on the shared tree), execute the layout, and return the
// rendered HTML. Errors are wrapped with the offending step for diagnosis.
func (r *Renderer) renderToBuffer(layoutTemplate, pageTemplate string, data PageData) ([]byte, error) {
	clone, err := r.templates.Clone()
	if err != nil {
		return nil, fmt.Errorf("clone master: %w", err)
	}
	raw, err := fs.ReadFile(templatesFS, pageTemplate)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", pageTemplate, err)
	}
	if _, err := clone.New(pageTemplate).Parse(string(raw)); err != nil {
		return nil, fmt.Errorf("parse %q: %w", pageTemplate, err)
	}
	var buf bytes.Buffer
	if err := clone.ExecuteTemplate(&buf, layoutTemplate, data); err != nil {
		return nil, fmt.Errorf("execute %q: %w", layoutTemplate, err)
	}
	return buf.Bytes(), nil
}
