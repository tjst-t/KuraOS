// apps.go renders /ui/admin/apps and the install / progress / uninstall
// htmx fragments. Visual SSOT is prototype/claude_design/Apps.html — Installed
// / Store toggle, category pill filter, card grid, install modal.
//
// Design choices:
//   - The install flow streams ProgressEvent over Server-Sent Events (SSE).
//     The browser's EventSource (or htmx's sse extension) listens on
//     /ui/admin/apps/install/stream?app_id=... and renders each stage as a
//     row in the progress card.
//   - Catalog cards (Store tab) are sourced from app.RegistryClient.
//     FetchRegistry() — when no registries are configured the tab shows the
//     empty-state banner.
//   - Install / uninstall buttons POST htmx fragments; install spawns a
//     goroutine on the server and returns immediately with the SSE-bound
//     progress card so the modal does not block the response.
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kuraos-org/kura/engine/app"
	"github.com/kuraos-org/kura/i18n"
)

// AppsDeps wires the Apps page handler.
type AppsDeps struct {
	Lifecycle *app.AppLifecycle
	Registry  app.RegistryClient
	// Sources is the trusted registry list (from config.json's
	// apps.registries). Empty means the Store tab is empty (no upstreams).
	Sources []app.RegistrySource
	// SharePathLister returns operator-visible share paths for the
	// share_picker dropdown. cmd/kura wires share.Manager-backed listing.
	SharePathLister AppsSharePathLister
	// Now overrides time.Now for tests.
	Now func() time.Time
}

// AppsSharePathLister is the slim surface the install form needs.
type AppsSharePathLister interface {
	ListSharePaths(ctx context.Context) ([]AppsShareOption, error)
}

// AppsShareOption is one entry in the share_picker dropdown.
type AppsShareOption struct {
	Name string
	Path string
}

// AppsView is the page-level template view.
type AppsView struct {
	Subtitle      string
	Tab           string // "installed" | "store"
	Categories    []string
	ActiveCategory string

	Installed []AppsCardRow
	Store     []AppsStoreCard

	EmptyInstalled string
	EmptyStore     string
}

// AppsCardRow is one entry of the Installed grid.
type AppsCardRow struct {
	AppID       string
	Name        string
	Version     string
	Category    string
	Description string
	StateID     string
	StateLabel  string
	StateClass  string
	OpenURL     string
	UninstallURL string
}

// AppsStoreCard is one entry of the Store grid.
type AppsStoreCard struct {
	Name        string
	Category    string
	Description string
	Registry    string
	Version     string
}

// AppsHandler returns the GET /ui/admin/apps handler.
func (r *Renderer) AppsHandler(deps AppsDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		view := r.buildAppsView(req.Context(), req, deps)
		data := r.buildPageData("apps", i18n.MsgAppsTitle)
		data.Extra = view
		r.render(w, "templates/pages/apps.tmpl", data)
	})
}

// buildAppsView assembles the page view-model.
func (r *Renderer) buildAppsView(ctx context.Context, req *http.Request, deps AppsDeps) AppsView {
	tab := req.URL.Query().Get("tab")
	if tab != "store" {
		tab = "installed"
	}
	cat := req.URL.Query().Get("category")
	if cat == "" {
		cat = "All"
	}
	view := AppsView{
		Tab:            tab,
		ActiveCategory: cat,
		EmptyInstalled: r.tr.T(i18n.MsgAppsEmptyInstalled),
		EmptyStore:     r.tr.T(i18n.MsgAppsEmptyStore),
		Categories:     []string{r.tr.T(i18n.MsgAppsCategoryAll)},
	}

	installedRows := []AppsCardRow{}
	if deps.Lifecycle != nil {
		recs, err := deps.Lifecycle.ListApps(ctx)
		if err == nil {
			for _, rec := range recs {
				row := AppsCardRow{
					AppID:        rec.AppID,
					Name:         rec.Name,
					Version:      rec.Version,
					Category:     rec.Settings["category"],
					Description:  rec.Settings["description"],
					StateID:      rec.State,
					StateLabel:   r.appStateLabel(rec.State),
					StateClass:   appStateClass(rec.State),
					OpenURL:      "/apps/" + rec.Name + "/",
					UninstallURL: "/ui/admin/apps/uninstall?app_id=" + rec.AppID,
				}
				installedRows = append(installedRows, row)
			}
		}
	}
	view.Installed = installedRows

	// Store: enumerate every (registry, app, version=latest) the trusted
	// registries advertise. Failures (signature, hash) bubble to the empty
	// state — DESIGN_PRINCIPLES forbidden: never silently approve.
	storeRows := []AppsStoreCard{}
	categorySet := map[string]bool{}
	if deps.Registry != nil {
		for _, src := range deps.Sources {
			reg, err := deps.Registry.FetchRegistry(ctx, src)
			if err != nil {
				continue
			}
			for name, ra := range reg.Apps {
				ver := ra.Latest
				if ver == "" && len(ra.Versions) > 0 {
					for v := range ra.Versions {
						ver = v
						break
					}
				}
				card := AppsStoreCard{
					Name:        name,
					Description: name,
					Registry:    src.Name,
					Version:     ver,
				}
				storeRows = append(storeRows, card)
				if card.Category != "" {
					categorySet[card.Category] = true
				}
			}
		}
		sort.Slice(storeRows, func(i, j int) bool { return storeRows[i].Name < storeRows[j].Name })
	}
	view.Store = storeRows

	for _, row := range installedRows {
		if row.Category != "" {
			categorySet[row.Category] = true
		}
	}
	cats := make([]string, 0, len(categorySet))
	for c := range categorySet {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	view.Categories = append(view.Categories, cats...)

	view.Subtitle = r.tr.T(i18n.MsgAppsSubtitle, len(installedRows), len(storeRows))
	return view
}

// AppsInstallFormHandler renders the htmx install modal form for a chosen
// store entry. The form fields come from manifest.setup.required.
func (r *Renderer) AppsInstallFormHandler(deps AppsDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		appName := req.URL.Query().Get("app")
		regName := req.URL.Query().Get("registry")
		if appName == "" || regName == "" {
			http.Error(w, "missing app or registry", http.StatusBadRequest)
			return
		}
		src, ok := findSource(deps.Sources, regName)
		if !ok {
			http.Error(w, "unknown registry "+regName, http.StatusBadRequest)
			return
		}
		reg, err := deps.Registry.FetchRegistry(req.Context(), src)
		if err != nil {
			http.Error(w, "fetch registry: "+err.Error(), http.StatusBadGateway)
			return
		}
		ra, ok := reg.Apps[appName]
		if !ok {
			http.NotFound(w, req)
			return
		}
		ver := ra.Latest
		manifest, _, err := deps.Registry.FetchManifest(req.Context(), src, appName, ver)
		if err != nil {
			http.Error(w, "fetch manifest: "+err.Error(), http.StatusBadGateway)
			return
		}
		shares := []AppsShareOption{}
		if deps.SharePathLister != nil {
			shares, _ = deps.SharePathLister.ListSharePaths(req.Context())
		}
		view := AppsInstallFormView{
			AppName:    appName,
			Registry:   regName,
			Version:    ver,
			Title:      r.tr.T(i18n.MsgAppsInstallTitle, appName),
			SetupHdr:   r.tr.T(i18n.MsgAppsInstallSetupHdr),
			SubmitText: r.tr.T(i18n.MsgAppsInstallSubmit),
			CancelText: r.tr.T(i18n.MsgAppsBtnCancel),
			Required:   buildSetupFields(manifest.Setup.Required, shares),
		}
		r.renderFragment(w, "templates/pages/apps_install_form.tmpl", view)
	})
}

// AppsInstallFormView is the install modal view.
type AppsInstallFormView struct {
	AppName    string
	Registry   string
	Version    string
	Title      string
	SetupHdr   string
	SubmitText string
	CancelText string
	Required   []AppsSetupField
}

// AppsSetupField is one field in the install form.
type AppsSetupField struct {
	Key     string
	Type    string // "share_picker" | "select" | "toggle" | "text"
	Label   string
	Options []AppsSetupOption
	Default string
}

// AppsSetupOption is one option of select / share_picker.
type AppsSetupOption struct {
	Value string
	Label string
}

// AppsInstallStartHandler kicks off install in a goroutine and returns the
// SSE-bound progress card. The browser's EventSource then streams stages.
func (r *Renderer) AppsInstallStartHandler(deps AppsDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		appName := req.FormValue("app")
		regName := req.FormValue("registry")
		version := req.FormValue("version")
		if appName == "" || regName == "" || version == "" {
			http.Error(w, "app, registry, version required", http.StatusBadRequest)
			return
		}
		src, ok := findSource(deps.Sources, regName)
		if !ok {
			http.Error(w, "unknown registry "+regName, http.StatusBadRequest)
			return
		}
		setup := map[string]string{}
		settings := map[string]string{}
		for k, vs := range req.PostForm {
			if len(vs) == 0 {
				continue
			}
			switch {
			case strings.HasPrefix(k, "setup."):
				setup[strings.TrimPrefix(k, "setup.")] = vs[0]
			case strings.HasPrefix(k, "setting."):
				settings[strings.TrimPrefix(k, "setting.")] = vs[0]
			}
		}
		appID := app.NewAppIDForName(appName)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			_, _ = deps.Lifecycle.Install(ctx, app.InstallRequest{
				Source:      src,
				AppName:     appName,
				Version:     version,
				SetupValues: setup,
				Settings:    settings,
				AppID:       appID,
			})
		}()
		view := AppsProgressView{
			AppID:    appID,
			AppName:  appName,
			Title:    r.tr.T(i18n.MsgAppsInstallProgress),
			StreamURL: "/ui/admin/apps/stream?app_id=" + appID,
		}
		r.renderFragment(w, "templates/pages/apps_progress.tmpl", view)
	})
}

// AppsProgressView is the SSE-bound progress card view.
type AppsProgressView struct {
	AppID     string
	AppName   string
	Title     string
	StreamURL string
}

// AppsStreamHandler is the SSE endpoint. Subscribes to lifecycle events for
// the given app_id and forwards them as SSE messages.
func (r *Renderer) AppsStreamHandler(deps AppsDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		appID := req.URL.Query().Get("app_id")
		if appID == "" {
			http.Error(w, "app_id required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		ch, cancel := deps.Lifecycle.Subscribe(appID)
		defer cancel()
		// Always send a synthetic "open" frame first so EventSource fires
		// onopen and tests can wait for the connection to be established.
		fmt.Fprintf(w, "event: open\ndata: {\"app_id\":%q}\n\n", appID)
		flusher.Flush()

		// Time out idle streams after 5 min so the goroutine doesn't leak.
		idle := time.NewTimer(5 * time.Minute)
		defer idle.Stop()
		for {
			select {
			case <-req.Context().Done():
				return
			case <-idle.C:
				fmt.Fprintf(w, "event: timeout\ndata: {}\n\n")
				flusher.Flush()
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				idle.Reset(5 * time.Minute)
				body, _ := json.Marshal(struct {
					AppID  string `json:"app_id"`
					Stage  string `json:"stage"`
					Detail string `json:"detail"`
					OK     bool   `json:"ok"`
					Final  bool   `json:"final"`
					Label  string `json:"label"`
				}{
					AppID:  ev.AppID,
					Stage:  ev.Stage,
					Detail: ev.Detail,
					OK:     ev.OK,
					Final:  ev.Final,
					Label:  r.appsStageLabel(ev.Stage),
				})
				fmt.Fprintf(w, "event: progress\ndata: %s\n\n", body)
				flusher.Flush()
				if ev.Final {
					return
				}
			}
		}
	})
}

// AppsUninstallHandler shows the uninstall confirm card for GET, performs
// the uninstall + redirects on POST.
func (r *Renderer) AppsUninstallHandler(deps AppsDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		appID := req.URL.Query().Get("app_id")
		if appID == "" {
			http.Error(w, "app_id required", http.StatusBadRequest)
			return
		}
		switch req.Method {
		case http.MethodGet:
			rec, err := deps.Lifecycle.LookupApp(req.Context(), appID)
			if err != nil || rec.AppID == "" {
				http.Error(w, "not installed", http.StatusNotFound)
				return
			}
			view := AppsUninstallView{
				AppID:        appID,
				AppName:      rec.Name,
				Title:        r.tr.T(i18n.MsgAppsUninstallTitle, rec.Name),
				Warn:         r.tr.T(i18n.MsgAppsUninstallWarn),
				Submit:       r.tr.T(i18n.MsgAppsUninstallSubmit),
				Cancel:       r.tr.T(i18n.MsgAppsBtnCancel),
				DeleteData:   r.tr.T(i18n.MsgAppsUninstallDeleteData),
				DeleteHint:   r.tr.T(i18n.MsgAppsUninstallDeleteHint),
			}
			r.renderFragment(w, "templates/pages/apps_uninstall_form.tmpl", view)
		case http.MethodPost:
			if err := req.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			deleteData := req.FormValue("delete_data") == "on"
			err := deps.Lifecycle.Uninstall(req.Context(), app.UninstallRequest{
				AppID: appID, DeleteData: deleteData,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// htmx redirect via HX-Redirect header.
			w.Header().Set("HX-Redirect", "/ui/admin/apps")
			w.WriteHeader(http.StatusOK)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	})
}

// AppsUninstallView is the confirm dialog view.
type AppsUninstallView struct {
	AppID      string
	AppName    string
	Title      string
	Warn       string
	Submit     string
	Cancel     string
	DeleteData string
	DeleteHint string
}

// renderFragment renders a partial template directly (no admin shell wrapping).
// Used for htmx swap targets.
func (r *Renderer) renderFragment(w http.ResponseWriter, tmplPath string, data any) {
	clone, err := r.templates.Clone()
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	raw, err := r.readTemplateFile(tmplPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := clone.New(tmplPath).Parse(string(raw)); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := clone.ExecuteTemplate(w, tmplPath, data); err != nil {
		// already wrote 200 — log via stderr; client gets a partial doc.
		fmt.Fprintln(w, "<div class=\"banner crit\">render error: "+err.Error()+"</div>")
	}
}

// readTemplateFile reads tmplPath from the embedded fs.
func (r *Renderer) readTemplateFile(tmplPath string) ([]byte, error) {
	raw, err := readEmbedFile(tmplPath)
	if err != nil {
		return nil, fmt.Errorf("read template %q: %w", tmplPath, err)
	}
	return raw, nil
}

// readEmbedFile fishes a file out of the package's embedded FS via the
// shared templatesFS handle.
func readEmbedFile(p string) ([]byte, error) {
	return readEmbed(p)
}

// readEmbed delegates to the package-level templatesFS. Lives in apps.go so
// fallback paths (templates/pages/*.tmpl) stay localised — adding more
// embedded subdirs only needs touching this helper.
func readEmbed(p string) ([]byte, error) {
	b, err := templatesFS.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("embed read %s: %w", p, err)
	}
	return b, nil
}

// findSource returns the RegistrySource matching name from a list. ok=false
// when name is unknown.
func findSource(sources []app.RegistrySource, name string) (app.RegistrySource, bool) {
	for _, s := range sources {
		if s.Name == name {
			return s, true
		}
	}
	return app.RegistrySource{}, false
}

// buildSetupFields converts manifest setup fields into the template view,
// resolving share_picker options against the live share list.
func buildSetupFields(in []app.SetupField, shares []AppsShareOption) []AppsSetupField {
	out := make([]AppsSetupField, 0, len(in))
	for _, f := range in {
		field := AppsSetupField{
			Key:     f.Key,
			Type:    string(f.Type),
			Label:   labelOrKey(f.Label, f.Key),
			Default: f.Default,
		}
		switch f.Type {
		case app.SetupSharePicker:
			for _, s := range shares {
				field.Options = append(field.Options, AppsSetupOption{
					Value: s.Path, Label: s.Name + " (" + s.Path + ")",
				})
			}
		case app.SetupSelect:
			for _, opt := range f.Options {
				field.Options = append(field.Options, AppsSetupOption{Value: opt, Label: opt})
			}
		}
		out = append(out, field)
	}
	return out
}

func labelOrKey(label map[string]string, key string) string {
	if v, ok := label["ja"]; ok && v != "" {
		return v
	}
	if v, ok := label["en"]; ok && v != "" {
		return v
	}
	return key
}

func appStateClass(state string) string {
	switch state {
	case "running":
		return "ok"
	case "failed":
		return "crit"
	case "installing":
		return "warn"
	}
	return ""
}

func (r *Renderer) appStateLabel(state string) string {
	switch state {
	case "running":
		return r.tr.T(i18n.MsgAppsStateRunning)
	case "stopped":
		return r.tr.T(i18n.MsgAppsStateStopped)
	case "installing":
		return r.tr.T(i18n.MsgAppsStateInstalling)
	case "failed":
		return r.tr.T(i18n.MsgAppsStateFailed)
	case "uninstalling":
		return r.tr.T(i18n.MsgAppsStateUninstalling)
	}
	return state
}

// appsStageLabel maps a lifecycle stage token to its translated label.
// Centralised here so the SSE handler and the progress card render
// consistent text.
func (r *Renderer) appsStageLabel(stage string) string {
	switch stage {
	case app.StageStart:
		return r.tr.T(i18n.MsgAppsStageStart)
	case app.StagePullImages:
		return r.tr.T(i18n.MsgAppsStagePullImages)
	case app.StageProvisionData:
		return r.tr.T(i18n.MsgAppsStageProvisionData)
	case app.StageReservePorts:
		return r.tr.T(i18n.MsgAppsStageReservePorts)
	case app.StageStoreSecrets:
		return r.tr.T(i18n.MsgAppsStageStoreSecrets)
	case app.StageRenderConfigs:
		return r.tr.T(i18n.MsgAppsStageRenderConfigs)
	case app.StageStartContainers:
		return r.tr.T(i18n.MsgAppsStageStartContainers)
	case app.StageHealthcheck:
		return r.tr.T(i18n.MsgAppsStageHealthcheck)
	case app.StageRegisterRoute:
		return r.tr.T(i18n.MsgAppsStageRegisterRoute)
	case app.StageDone:
		return r.tr.T(i18n.MsgAppsStageDone)
	case app.StageRollback:
		return r.tr.T(i18n.MsgAppsStageRollback)
	case app.StageError:
		return r.tr.T(i18n.MsgAppsStageError)
	case app.StageSnapshot:
		return r.tr.T(i18n.MsgAppsStageSnapshot)
	case app.StagePullUpdate:
		return r.tr.T(i18n.MsgAppsStagePullUpdate)
	case app.StageStop:
		return r.tr.T(i18n.MsgAppsStageStop)
	case app.StageRemove:
		return r.tr.T(i18n.MsgAppsStageRemove)
	}
	return stage
}

// SetAppsHandler installs the apps page handler set so Routes() picks them up.
// Must be called before Routes().
func (r *Renderer) SetAppsHandler(deps AppsDeps) {
	r.appsListHandler = r.AppsHandler(deps)
	r.appsInstallFormHandler = r.AppsInstallFormHandler(deps)
	r.appsInstallStartHandler = r.AppsInstallStartHandler(deps)
	r.appsStreamHandler = r.AppsStreamHandler(deps)
	r.appsUninstallHandler = r.AppsUninstallHandler(deps)
}

// Errors-as-string helper used by tests and the install start handler.
func errString(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, app.ErrSignatureVerification) {
		return "signature failed"
	}
	if errors.Is(err, app.ErrManifestHashMismatch) {
		return "hash mismatch"
	}
	return err.Error()
}
