package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	Channels       []channelView
	Events         []settingsEventView
	SeverityFilter string
	ErrorMsg       string
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
	sevFilter := req.URL.Query().Get("sev")
	extra := h.buildExtra(req.Context(), sevFilter)
	data := h.r.buildPageData("settings", i18n.MsgNavSettings)
	data.Extra = extra
	h.r.render(w, "templates/pages/settings.tmpl", data)
}

func (h *settingsHandler) buildExtra(ctx context.Context, sevFilter string) *settingsExtra {
	extra := &settingsExtra{SeverityFilter: sevFilter}

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
