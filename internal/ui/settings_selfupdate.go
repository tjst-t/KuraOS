package ui

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kuraos-org/kura/engine/selfupdate"
	"github.com/kuraos-org/kura/i18n"
)

// SelfUpdateDeps wires the self-update engine into the Settings update tab.
type SelfUpdateDeps struct {
	Updater        *selfupdate.Updater
	CurrentVersion string
}

// SetSelfUpdateHandler installs the self-update handler.
func (r *Renderer) SetSelfUpdateHandler(deps SelfUpdateDeps) {
	r.selfUpdateHandler = &selfUpdatePageHandler{r: r, deps: deps}
}

type selfUpdatePageHandler struct {
	r    *Renderer
	deps SelfUpdateDeps
}

// selfUpdateExtra is passed as PageData.Extra for the self-update tab.
type selfUpdateExtra struct {
	ActiveTab      string
	CurrentVersion string
	LatestVersion  string
	Available      bool
	SuccessMsg     string
	ErrorMsg       string
	RolledBack     bool
}

func (h *selfUpdatePageHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path
	switch {
	case path == "/ui/admin/settings/self-update/check" && req.Method == http.MethodPost:
		h.handleCheck(w, req)
	case path == "/ui/admin/settings/self-update/apply" && req.Method == http.MethodPost:
		h.handleApply(w, req)
	default:
		http.NotFound(w, req)
	}
}

// buildSelfUpdateExtra returns the template data for the self-update tab.
func (h *selfUpdatePageHandler) buildSelfUpdateExtra(_ *http.Request) *selfUpdateExtra {
	return &selfUpdateExtra{
		ActiveTab:      "self_update",
		CurrentVersion: h.deps.CurrentVersion,
	}
}

// handleCheck checks for a new release.
// [AC-Sf92666-4-1]
func (h *selfUpdatePageHandler) handleCheck(w http.ResponseWriter, req *http.Request) {
	extra := &selfUpdateExtra{
		ActiveTab:      "self_update",
		CurrentVersion: h.deps.CurrentVersion,
	}
	if h.deps.Updater != nil {
		info, err := h.deps.Updater.CheckForUpdate(req.Context(), h.deps.CurrentVersion)
		if err != nil {
			extra.ErrorMsg = h.r.tr.T(i18n.MsgSettingsSelfUpdateErrCheck, err.Error())
		} else if info == nil {
			extra.LatestVersion = h.deps.CurrentVersion
			extra.Available = false
		} else {
			extra.LatestVersion = info.Version
			extra.Available = true
		}
	} else {
		extra.LatestVersion = h.deps.CurrentVersion
	}
	h.renderSelfUpdate(w, req, extra)
}

// handleApply downloads, verifies, and applies the new binary.
// [AC-Sf92666-4-1] [AC-Sf92666-4-2]
func (h *selfUpdatePageHandler) handleApply(w http.ResponseWriter, req *http.Request) {
	extra := &selfUpdateExtra{
		ActiveTab:      "self_update",
		CurrentVersion: h.deps.CurrentVersion,
	}

	if h.deps.Updater == nil {
		extra.ErrorMsg = h.r.tr.T(i18n.MsgSettingsSelfUpdateErrApply, "updater not configured")
		h.renderSelfUpdate(w, req, extra)
		return
	}

	info, err := h.deps.Updater.CheckForUpdate(req.Context(), h.deps.CurrentVersion)
	if err != nil {
		extra.ErrorMsg = h.r.tr.T(i18n.MsgSettingsSelfUpdateErrCheck, err.Error())
		h.renderSelfUpdate(w, req, extra)
		return
	}
	if info == nil {
		// Already up to date — nothing to apply.
		extra.LatestVersion = h.deps.CurrentVersion
		h.renderSelfUpdate(w, req, extra)
		return
	}
	extra.LatestVersion = info.Version

	// Apply runs in a goroutine to avoid blocking the HTTP response — the UI
	// shows "applying" and the user can observe the restart.
	// For the response, we report "applying" immediately.
	go func() {
		applyCtx := context.Background()
		if err := h.deps.Updater.Apply(applyCtx, info); err != nil {
			// Log the error; the process may have been killed + rolled back.
			fmt.Printf("selfupdate: apply failed: %v\n", err)
		}
	}()

	extra.SuccessMsg = h.r.tr.T(i18n.MsgSettingsSelfUpdateApplying)
	h.renderSelfUpdate(w, req, extra)
}

func (h *selfUpdatePageHandler) renderSelfUpdate(w http.ResponseWriter, req *http.Request, extra *selfUpdateExtra) {
	data := h.r.buildPageData("settings", i18n.MsgNavSettings)
	data.Extra = extra
	h.r.render(w, "templates/pages/settings.tmpl", data)
}
