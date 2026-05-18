package ui

import (
	"context"
	"net/http"
	"strings"

	tlspkg "github.com/kuraos-org/kura/engine/network/tls"
	"github.com/kuraos-org/kura/engine/network/acme"
	"github.com/kuraos-org/kura/i18n"
)

// TLSDeps wires the TLS engine into the Settings TLS tab.
type TLSDeps struct {
	// StorageDir is the directory for TLS key material (KURA_TLS_DIR).
	StorageDir string
	// CurrentMode is the current TLS mode from config.json.
	CurrentMode string
	// CurrentPort is the current TLS port from config.json.
	CurrentPort int
	// CurrentProvider is the current ACME provider.
	CurrentProvider string
	// CurrentDomains is the current domain list.
	CurrentDomains []string
	// Hosts is the list of hostnames for self-signed SANs.
	Hosts []string
	// ApplyFn is called when the user submits the TLS form. It should persist
	// the new TLS config to config.json and regenerate certs. nil = dry-run only.
	ApplyFn func(ctx context.Context, mode, provider string, domains []string, port int) error
}

// SetTLSHandler installs the TLS settings handler.
func (r *Renderer) SetTLSHandler(deps TLSDeps) {
	r.tlsHandler = &tlsSettingsHandler{r: r, deps: deps}
}

type tlsSettingsHandler struct {
	r    *Renderer
	deps TLSDeps
}

// tlsExtra is passed as PageData.Extra to settings.tmpl for the TLS tab.
type tlsExtra struct {
	ActiveTab        string
	Mode             string
	Port             int
	Provider         string
	Domains          string
	AvailableProviders []string
	CACertPath       string
	InstallHint      string
	SuccessMsg       string
	ErrorMsg         string
}

func (h *tlsSettingsHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path
	if path == "/ui/admin/settings/tls/apply" && req.Method == http.MethodPost {
		h.handleApply(w, req)
		return
	}
	if path == "/ui/admin/settings/tls/ca" && req.Method == http.MethodGet {
		h.handleCADownload(w, req)
		return
	}
	// GET /ui/admin/settings?tab=tls is handled by the main settings handler
	// by calling buildTLSExtra(). This handler is a sub-handler for POST operations.
	http.NotFound(w, req)
}

// buildTLSExtra builds the template data for the TLS tab.
// [AC-Sf92666-1-1]
func (h *tlsSettingsHandler) buildTLSExtra(req *http.Request) *tlsExtra {
	extra := &tlsExtra{
		ActiveTab:          "tls",
		Mode:               h.deps.CurrentMode,
		Port:               h.deps.CurrentPort,
		Provider:           h.deps.CurrentProvider,
		Domains:            strings.Join(h.deps.CurrentDomains, ", "),
		AvailableProviders: acme.List(),
	}
	if extra.Mode == "" {
		extra.Mode = "self_signed"
	}
	if extra.Port == 0 {
		extra.Port = 8443
	}

	// If self_signed and cert already exists, show the CA cert path.
	if extra.Mode == string(tlspkg.ModeSelfSigned) {
		dir := h.deps.StorageDir
		if dir == "" {
			dir = tlspkg.StorageDir()
		}
		caCert, _, _ := tlspkg.CertPaths(dir)
		extra.CACertPath = caCert
		extra.InstallHint = tlspkg.CAInstallHint(caCert)
	}
	return extra
}

// handleApply processes the TLS settings form POST.
// [AC-Sf92666-1-1]
func (h *tlsSettingsHandler) handleApply(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	mode := req.FormValue("mode")
	port := 8443
	if p := req.FormValue("port"); p != "" {
		_, _ = parseInt(p, &port)
	}
	provider := req.FormValue("provider")
	domainsRaw := req.FormValue("domains")
	var domains []string
	for _, d := range strings.Split(domainsRaw, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			domains = append(domains, d)
		}
	}

	extra := &tlsExtra{
		ActiveTab:          "tls",
		Mode:               mode,
		Port:               port,
		Provider:           provider,
		Domains:            domainsRaw,
		AvailableProviders: acme.List(),
	}

	if h.deps.ApplyFn != nil {
		if err := h.deps.ApplyFn(req.Context(), mode, provider, domains, port); err != nil {
			extra.ErrorMsg = h.r.tr.T(i18n.MsgSettingsTLSErrApply, err.Error())
		} else {
			extra.SuccessMsg = h.r.tr.T(i18n.MsgSettingsTLSApplySuccess)
			if mode == string(tlspkg.ModeSelfSigned) {
				dir := h.deps.StorageDir
				if dir == "" {
					dir = tlspkg.StorageDir()
				}
				caCert, _, _ := tlspkg.CertPaths(dir)
				extra.CACertPath = caCert
				extra.InstallHint = tlspkg.CAInstallHint(caCert)
			}
		}
	} else {
		extra.SuccessMsg = h.r.tr.T(i18n.MsgSettingsTLSApplySuccess)
	}

	data := h.r.buildPageData("settings", i18n.MsgNavSettings)
	data.Extra = extra
	h.r.render(w, "templates/pages/settings.tmpl", data)
}

// handleCADownload serves the CA certificate file for browser import.
func (h *tlsSettingsHandler) handleCADownload(w http.ResponseWriter, req *http.Request) {
	dir := h.deps.StorageDir
	if dir == "" {
		dir = tlspkg.StorageDir()
	}
	caCertPath, _, _ := tlspkg.CertPaths(dir)
	w.Header().Set("Content-Disposition", "attachment; filename=kuraos-ca.crt")
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	http.ServeFile(w, req, caCertPath)
}

func parseInt(s string, out *int) (int, bool) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return n, true
}
