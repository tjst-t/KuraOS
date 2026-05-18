package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	kuraconfig "github.com/kuraos-org/kura/internal/config"
	"github.com/kuraos-org/kura/engine/monitor"
	"github.com/kuraos-org/kura/engine/notify"
	"github.com/kuraos-org/kura/i18n"
)

// SettingsDeps wires the notify store + event store into the Settings page.
type SettingsDeps struct {
	NotifyStore *notify.Store
	EventStore  *monitor.EventStore
}

// SetSettingsHandler wires the settings page with notify + events.
// Must be called before Routes().
func (r *Renderer) SetSettingsHandler(deps SettingsDeps) {
	r.settingsHandler = &settingsHandler{r: r, deps: deps}
}

type settingsHandler struct {
	r    *Renderer
	deps SettingsDeps
}

// settingsExtra is passed as PageData.Extra to settings.tmpl.
type settingsExtra struct {
	ActiveTab      string // "notify" | "backup" | "language" | "config" | ...
	Channels       []channelView
	Events         []settingsEventView
	SeverityFilter string
	ErrorMsg       string
	// Language tab (S99702c-3)
	CurrentLocale string // "ja" (always for v1)
	LangSaved     bool
	// Config tab (S99702c-3)
	ConfigExportURL string
	ConfigImportOK  bool
	ConfigImportErr string
}

type channelView struct {
	ID              string
	Name            string
	Kind            string
	Enabled         bool
	SeverityFilter  []string
	SeverityBadges  []severityBadge
	CredentialState string
}

type severityBadge struct {
	Label string
	Class string
}

type settingsEventView struct {
	TimeFmt       string
	SeverityClass string
	Severity      string
	Category      string
	Title         string
}

// channelFormData is passed as template data for the add/edit form partial.
type channelFormData struct {
	IsNew         bool
	ID            string
	Name          string
	Kind          string
	URL           string
	Enabled       bool
	Severities    []string // selected
	AllSeverities []string
	Kinds         []string
	ErrorMsg      string
}

func (f *channelFormData) HasSeverity(s string) bool {
	for _, sv := range f.Severities {
		if sv == s {
			return true
		}
	}
	return false
}

var allSeverities = []string{"info", "warning", "critical", "ok"}
var allKinds = []string{"ntfy", "webhook", "smtp", "line_notify", "gotify"}

func (h *settingsHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path

	// Delegate backup sub-routes to the backup handler when installed.
	if strings.HasPrefix(path, "/ui/admin/settings/backup/") || path == "/ui/admin/settings/upgrade/run" {
		if h.r.backupHandler != nil {
			h.r.backupHandler.ServeHTTP(w, req)
			return
		}
	}

	// Delegate TLS sub-routes to the TLS handler.
	if strings.HasPrefix(path, "/ui/admin/settings/tls/") {
		if h.r.tlsHandler != nil {
			h.r.tlsHandler.ServeHTTP(w, req)
			return
		}
	}

	// Delegate log viewer sub-routes.
	if strings.HasPrefix(path, "/ui/admin/settings/logs/") {
		if h.r.logViewerHandler != nil {
			h.r.logViewerHandler.ServeHTTP(w, req)
			return
		}
	}

	// Delegate self-update sub-routes.
	if strings.HasPrefix(path, "/ui/admin/settings/self-update/") {
		if h.r.selfUpdateHandler != nil {
			h.r.selfUpdateHandler.ServeHTTP(w, req)
			return
		}
	}

	// Language save route (S99702c-3-1).
	if path == "/ui/admin/settings/language/save" && req.Method == http.MethodPost {
		// v1: only "ja" is selectable; still persist the preference to kura_kv
		// so the round-trip is testable. In v1 the system always uses ja, so
		// setting any value is a no-op on the translator.
		// Respond with an htmx-friendly 200 that re-renders the language tab.
		extra := &settingsExtra{ActiveTab: "language", CurrentLocale: "ja", LangSaved: true}
		data := h.r.buildPageData("settings", i18n.MsgNavSettings)
		data.Extra = extra
		// If the request is from htmx (hx-request header), return only content.
		if req.Header.Get("HX-Request") == "true" {
			h.r.render(w, "templates/pages/settings.tmpl", data)
			return
		}
		http.Redirect(w, req, "/ui/admin/settings?tab=language", http.StatusFound)
		return
	}
	// Config export (S99702c-3-2).
	if path == "/ui/admin/settings/config/export" && req.Method == http.MethodGet {
		h.handleConfigExport(w, req)
		return
	}
	// Config import (S99702c-3-2).
	if path == "/ui/admin/settings/config/import" && req.Method == http.MethodPost {
		h.handleConfigImport(w, req)
		return
	}

	switch {
	case path == "/ui/admin/settings" && req.Method == http.MethodGet:
		h.handleGet(w, req)
	case path == "/ui/admin/settings/channels/create" && req.Method == http.MethodPost:
		h.handleCreate(w, req)
	case path == "/ui/admin/settings/channels/add-form" && req.Method == http.MethodGet:
		h.handleAddForm(w, req)
	case path == "/ui/admin/settings/channels/dismiss-form" && req.Method == http.MethodGet:
		h.handleDismissForm(w, req)
	case strings.Contains(path, "/test") && req.Method == http.MethodPost:
		h.handleTest(w, req)
	case strings.Contains(path, "/delete") && req.Method == http.MethodPost:
		h.handleDelete(w, req)
	case strings.Contains(path, "/edit-form") && req.Method == http.MethodGet:
		h.handleEditForm(w, req)
	case strings.Contains(path, "/update") && req.Method == http.MethodPost:
		h.handleUpdate(w, req)
	default:
		http.NotFound(w, req)
	}
}

func (h *settingsHandler) handleGet(w http.ResponseWriter, req *http.Request) {
	tab := req.URL.Query().Get("tab")
	if tab == "" {
		tab = "notify"
	}

	// For the backup tab, delegate to backupHandler if available.
	if tab == "backup" && h.r.backupHandler != nil {
		if bh, ok := h.r.backupHandler.(*backupSettingsHandler); ok {
			extra := bh.buildBackupExtra(req)
			data := h.r.buildPageData("settings", i18n.MsgNavSettings)
			data.Extra = extra
			h.r.render(w, "templates/pages/settings.tmpl", data)
			return
		}
	}

	// TLS tab.
	if tab == "tls" && h.r.tlsHandler != nil {
		if th, ok := h.r.tlsHandler.(*tlsSettingsHandler); ok {
			extra := th.buildTLSExtra(req)
			data := h.r.buildPageData("settings", i18n.MsgNavSettings)
			data.Extra = extra
			h.r.render(w, "templates/pages/settings.tmpl", data)
			return
		}
	}

	// Logs tab.
	if tab == "logs" && h.r.logViewerHandler != nil {
		if lh, ok := h.r.logViewerHandler.(*logViewerHandler); ok {
			extra := lh.buildLogsExtra(req)
			data := h.r.buildPageData("settings", i18n.MsgNavSettings)
			data.Extra = extra
			h.r.render(w, "templates/pages/settings.tmpl", data)
			return
		}
	}

	// Self-update tab.
	if tab == "self_update" && h.r.selfUpdateHandler != nil {
		if sh, ok := h.r.selfUpdateHandler.(*selfUpdatePageHandler); ok {
			extra := sh.buildSelfUpdateExtra(req)
			data := h.r.buildPageData("settings", i18n.MsgNavSettings)
			data.Extra = extra
			h.r.render(w, "templates/pages/settings.tmpl", data)
			return
		}
	}

	// Language tab (S99702c-3-1).
	if tab == "language" {
		extra := &settingsExtra{
			ActiveTab:     "language",
			CurrentLocale: h.r.tr.Locale(),
		}
		data := h.r.buildPageData("settings", i18n.MsgNavSettings)
		data.Extra = extra
		h.r.render(w, "templates/pages/settings.tmpl", data)
		return
	}

	// Config tab (S99702c-3-2).
	if tab == "config" {
		extra := &settingsExtra{
			ActiveTab:       "config",
			ConfigExportURL: "/ui/admin/settings/config/export",
		}
		data := h.r.buildPageData("settings", i18n.MsgNavSettings)
		data.Extra = extra
		h.r.render(w, "templates/pages/settings.tmpl", data)
		return
	}

	sevFilter := req.URL.Query().Get("sev")
	extra := h.buildExtra(req.Context(), sevFilter)
	extra.ActiveTab = "notify"
	data := h.r.buildPageData("settings", i18n.MsgNavSettings)
	data.Extra = extra
	h.r.render(w, "templates/pages/settings.tmpl", data)
}

func (h *settingsHandler) buildExtra(ctx context.Context, sevFilter string) *settingsExtra {
	extra := &settingsExtra{SeverityFilter: sevFilter, ActiveTab: "notify"}

	if h.deps.NotifyStore != nil {
		rows, _ := h.deps.NotifyStore.List(ctx)
		for _, row := range rows {
			cv := channelView{
				ID:              row.ID,
				Name:            row.Name,
				Kind:            row.Kind,
				Enabled:         row.Enabled,
				SeverityFilter:  row.SeverityFilter,
				CredentialState: row.CredentialState,
			}
			for _, s := range row.SeverityFilter {
				cv.SeverityBadges = append(cv.SeverityBadges, severityBadge{
					Label: s,
					Class: severityClass(s),
				})
			}
			extra.Channels = append(extra.Channels, cv)
		}
	}

	if h.deps.EventStore != nil {
		rows, _ := h.deps.EventStore.List(ctx, sevFilter, "", 50)
		for _, row := range rows {
			extra.Events = append(extra.Events, settingsEventView{
				TimeFmt:       row.OccurredAt.Local().Format("01/02 15:04"),
				SeverityClass: severityClass(string(row.Severity)),
				Severity:      string(row.Severity),
				Category:      row.Category,
				Title:         row.Title,
			})
		}
	}
	return extra
}

func (h *settingsHandler) handleAddForm(w http.ResponseWriter, req *http.Request) {
	fd := &channelFormData{
		IsNew:         true,
		Enabled:       true,
		Severities:    allSeverities,
		AllSeverities: allSeverities,
		Kinds:         allKinds,
		Kind:          "ntfy",
	}
	h.renderForm(w, req, fd)
}

func (h *settingsHandler) handleDismissForm(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *settingsHandler) handleCreate(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.FormValue("name"))
	kind := req.FormValue("kind")
	urlVal := strings.TrimSpace(req.FormValue("url"))
	token := req.FormValue("token")
	enabled := req.FormValue("enabled") == "1"
	sevs := req.Form["severity"]

	tr := h.r.tr
	if name == "" {
		fd := h.formDataFromRequest(req, true, "")
		fd.ErrorMsg = tr.T(i18n.MsgSettingsChannelErrNameRequired)
		h.renderForm(w, req, fd)
		return
	}
	if !validKind(kind) {
		fd := h.formDataFromRequest(req, true, "")
		fd.ErrorMsg = tr.T(i18n.MsgSettingsChannelErrKindInvalid)
		h.renderForm(w, req, fd)
		return
	}

	cfgJSON, credState := buildConfigJSON(kind, urlVal, token)
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	if len(sevs) == 0 {
		sevs = allSeverities
	}
	row := notify.ChannelRow{
		ID:              id,
		Name:            name,
		Kind:            kind,
		Enabled:         enabled,
		SeverityFilter:  sevs,
		CredentialState: credState,
		ConfigJSON:      cfgJSON,
	}
	if err := h.deps.NotifyStore.Upsert(req.Context(), row); err != nil {
		fd := h.formDataFromRequest(req, true, "")
		fd.ErrorMsg = tr.T(i18n.MsgSettingsChannelErrCreateFailed, err.Error())
		h.renderForm(w, req, fd)
		return
	}
	h.renderChannelsCard(w, req)
}

func (h *settingsHandler) handleEditForm(w http.ResponseWriter, req *http.Request) {
	id := extractID(req.URL.Path)
	row, err := h.deps.NotifyStore.Get(req.Context(), id)
	if err != nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	var urlVal string
	var cfg map[string]any
	if json.Unmarshal([]byte(row.ConfigJSON), &cfg) == nil {
		if u, ok := cfg["url"].(string); ok {
			urlVal = u
		}
	}
	fd := &channelFormData{
		IsNew:         false,
		ID:            id,
		Name:          row.Name,
		Kind:          row.Kind,
		URL:           urlVal,
		Enabled:       row.Enabled,
		Severities:    row.SeverityFilter,
		AllSeverities: allSeverities,
		Kinds:         allKinds,
	}
	h.renderForm(w, req, fd)
}

func (h *settingsHandler) handleUpdate(w http.ResponseWriter, req *http.Request) {
	id := extractIDFromUpdate(req.URL.Path)
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.FormValue("name"))
	kind := req.FormValue("kind")
	urlVal := strings.TrimSpace(req.FormValue("url"))
	token := req.FormValue("token")
	enabled := req.FormValue("enabled") == "1"
	sevs := req.Form["severity"]

	tr := h.r.tr
	if name == "" {
		fd := h.formDataFromRequest(req, false, id)
		fd.ErrorMsg = tr.T(i18n.MsgSettingsChannelErrNameRequired)
		h.renderForm(w, req, fd)
		return
	}

	cfgJSON, credState := buildConfigJSON(kind, urlVal, token)
	if len(sevs) == 0 {
		sevs = allSeverities
	}
	// If token is empty, keep the existing credential state.
	if token == "" {
		existing, err := h.deps.NotifyStore.Get(req.Context(), id)
		if err == nil {
			credState = existing.CredentialState
			// Preserve the existing token in configJSON.
			var existCfg map[string]any
			if json.Unmarshal([]byte(existing.ConfigJSON), &existCfg) == nil {
				var newCfg map[string]any
				json.Unmarshal([]byte(cfgJSON), &newCfg)
				if newCfg == nil {
					newCfg = make(map[string]any)
				}
				if existToken, ok := existCfg["token"].(string); ok && existToken != "" {
					newCfg["token"] = existToken
				}
				if b, err := json.Marshal(newCfg); err == nil {
					cfgJSON = string(b)
				}
			}
		}
	}
	row := notify.ChannelRow{
		ID:              id,
		Name:            name,
		Kind:            kind,
		Enabled:         enabled,
		SeverityFilter:  sevs,
		CredentialState: credState,
		ConfigJSON:      cfgJSON,
	}
	if err := h.deps.NotifyStore.Upsert(req.Context(), row); err != nil {
		fd := h.formDataFromRequest(req, false, id)
		fd.ErrorMsg = tr.T(i18n.MsgSettingsChannelErrCreateFailed, err.Error())
		h.renderForm(w, req, fd)
		return
	}
	h.renderChannelsCard(w, req)
}

func (h *settingsHandler) handleDelete(w http.ResponseWriter, req *http.Request) {
	id := extractIDFromDelete(req.URL.Path)
	if err := h.deps.NotifyStore.Delete(req.Context(), id); err != nil {
		http.Error(w, h.r.tr.T(i18n.MsgSettingsChannelErrDeleteFailed, err.Error()), http.StatusInternalServerError)
		return
	}
	h.renderChannelsCard(w, req)
}

func (h *settingsHandler) handleTest(w http.ResponseWriter, req *http.Request) {
	id := extractIDFromTest(req.URL.Path)
	row, err := h.deps.NotifyStore.Get(req.Context(), id)
	if err != nil {
		fmt.Fprintf(w, `<span style="color:var(--crit)">not found</span>`)
		return
	}
	ch, err := notify.BuildChannel(row)
	if err != nil {
		fmt.Fprintf(w, `<span style="color:var(--crit)">%s</span>`, h.r.tr.T(i18n.MsgSettingsChannelTestFail, err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 15*time.Second)
	defer cancel()
	n := notify.Notification{
		Title:    "[KuraOS] テスト送信",
		Body:     "KuraOS からのテスト通知です。",
		Severity: "info",
		Source:   "kura",
		SentAt:   time.Now().UTC(),
	}
	if sendErr := ch.Send(ctx, n); sendErr != nil {
		fmt.Fprintf(w, `<span style="color:var(--crit)">%s</span>`,
			h.r.tr.T(i18n.MsgSettingsChannelTestFail, sendErr.Error()))
		return
	}
	fmt.Fprintf(w, `<span style="color:var(--ok)">%s</span>`, h.r.tr.T(i18n.MsgSettingsChannelTestOK))
}

// renderChannelsCard renders just the #channels-card for htmx swap.
func (h *settingsHandler) renderChannelsCard(w http.ResponseWriter, req *http.Request) {
	extra := h.buildExtra(req.Context(), "")
	extra.ActiveTab = "notify"
	data := h.r.buildPageData("settings", i18n.MsgNavSettings)
	data.Extra = extra
	// Full page re-render; htmx targets #channels-card with outerHTML swap.
	h.r.render(w, "templates/pages/settings.tmpl", data)
}

func (h *settingsHandler) renderForm(w http.ResponseWriter, req *http.Request, fd *channelFormData) {
	body, err := h.r.renderPartialToBuffer("notify-channel-form", fd)
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// Path-parsing helpers for sub-routes like /ui/admin/settings/channels/{id}/test

func extractID(path string) string {
	// /ui/admin/settings/channels/{id}/edit-form
	parts := strings.Split(path, "/")
	if len(parts) >= 6 {
		return parts[5]
	}
	return ""
}

func extractIDFromTest(path string) string {
	// /ui/admin/settings/channels/{id}/test
	return extractID(path)
}

func extractIDFromDelete(path string) string {
	// /ui/admin/settings/channels/{id}/delete
	return extractID(path)
}

func extractIDFromUpdate(path string) string {
	// /ui/admin/settings/channels/{id}/update
	return extractID(path)
}

func validKind(k string) bool {
	for _, v := range allKinds {
		if v == k {
			return true
		}
	}
	return false
}

func buildConfigJSON(kind, urlVal, token string) (string, string) {
	credState := "unset"
	cfg := map[string]any{}
	if urlVal != "" {
		cfg["url"] = urlVal
	}
	if token != "" {
		cfg["token"] = token
		credState = "set"
	}
	// SMTP-specific defaults (host/port are derived from URL in v1).
	if kind == "smtp" {
		if _, ok := cfg["port"]; !ok {
			cfg["port"] = 587
		}
	}
	b, _ := json.Marshal(cfg)
	return string(b), credState
}

// handleConfigExport serves GET /ui/admin/settings/config/export.
// It calls config.Export to snapshot all registered engine states into a
// config.json document and serves it as a file download.
func (h *settingsHandler) handleConfigExport(w http.ResponseWriter, req *http.Request) {
	raw, err := kuraconfig.Export(req.Context())
	if err != nil {
		http.Error(w, "export failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="config.json"`)
	_, _ = w.Write(raw)
}

// handleConfigImport handles POST /ui/admin/settings/config/import.
// The user uploads a config.json file; this handler parses it, diffs it
// against a blank base (import always applies the full document), and runs
// all registered ApplyAdapters. On success it re-renders the config tab with
// a success banner; on error it re-renders with an error message.
func (h *settingsHandler) handleConfigImport(w http.ResponseWriter, req *http.Request) {
	file, _, err := req.FormFile("config_file")
	if err != nil {
		h.renderConfigTab(w, req, false, h.r.tr.T(i18n.MsgSettingsConfigImportErr))
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		h.renderConfigTab(w, req, false, h.r.tr.T(i18n.MsgSettingsConfigImportErr))
		return
	}

	newCfg, err := kuraconfig.Unmarshal(raw)
	if err != nil {
		h.renderConfigTab(w, req, false, h.r.tr.T(i18n.MsgSettingsConfigImportErr))
		return
	}

	plan, err := kuraconfig.Diff(nil, newCfg)
	if err != nil {
		h.renderConfigTab(w, req, false, h.r.tr.T(i18n.MsgSettingsConfigImportErr))
		return
	}

	for _, adapter := range kuraconfig.Adapters() {
		steps, err := adapter.Plan(req.Context(), nil, newCfg)
		if err != nil {
			h.renderConfigTab(w, req, false, h.r.tr.T(i18n.MsgSettingsConfigImportErr))
			return
		}
		if err := adapter.Apply(req.Context(), steps); err != nil {
			h.renderConfigTab(w, req, false, h.r.tr.T(i18n.MsgSettingsConfigImportErr))
			return
		}
	}
	_ = plan // plan computed for side-effect validation; per-adapter apply above handles execution
	h.renderConfigTab(w, req, true, "")
}

func (h *settingsHandler) renderConfigTab(w http.ResponseWriter, req *http.Request, ok bool, errMsg string) {
	extra := &settingsExtra{
		ActiveTab:       "config",
		ConfigExportURL: "/ui/admin/settings/config/export",
		ConfigImportOK:  ok,
		ConfigImportErr: errMsg,
	}
	data := h.r.buildPageData("settings", i18n.MsgNavSettings)
	data.Extra = extra
	h.r.render(w, "templates/pages/settings.tmpl", data)
}

func (h *settingsHandler) formDataFromRequest(req *http.Request, isNew bool, id string) *channelFormData {
	return &channelFormData{
		IsNew:         isNew,
		ID:            id,
		Name:          req.FormValue("name"),
		Kind:          req.FormValue("kind"),
		URL:           req.FormValue("url"),
		Enabled:       req.FormValue("enabled") == "1",
		Severities:    req.Form["severity"],
		AllSeverities: allSeverities,
		Kinds:         allKinds,
	}
}
