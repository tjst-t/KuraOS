package ui

import (
	"net/http"
	"os"
	"strings"

	"github.com/kuraos-org/kura/engine/network"
	"github.com/kuraos-org/kura/i18n"
)

// NetworkDeps wires the network engine into the Network page.
type NetworkDeps struct {
	Manager *network.Manager
}

// SetNetworkHandler installs the network page handler.
// Must be called before Routes().
func (r *Renderer) SetNetworkHandler(deps NetworkDeps) {
	r.networkHandler = &networkPageHandler{r: r, deps: deps}
}

type networkPageHandler struct {
	r    *Renderer
	deps NetworkDeps
}

// networkExtra is passed as PageData.Extra to network.tmpl.
type networkExtra struct {
	Info       *network.NetworkInfo
	ErrorMsg   string
	SuccessMsg string
}

func (h *networkPageHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/ui/admin/network":
		h.handleGet(w, req)
	case req.Method == http.MethodPost && req.URL.Path == "/ui/admin/network/apply":
		h.handleApply(w, req)
	default:
		http.NotFound(w, req)
	}
}

func (h *networkPageHandler) handleGet(w http.ResponseWriter, req *http.Request) {
	extra := &networkExtra{}
	if h.deps.Manager != nil {
		info, err := h.deps.Manager.GetInfo(req.Context())
		if err == nil {
			extra.Info = info
		}
	}
	data := h.r.buildPageData("network", i18n.MsgNetworkTitle)
	data.Extra = extra
	h.r.render(w, "templates/pages/network.tmpl", data)
}

// handleApply processes the POST form and applies network configuration.
// [AC-Sf92666-2-1]
func (h *networkPageHandler) handleApply(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	hostname := strings.TrimSpace(req.FormValue("hostname"))
	dnsRaw := strings.TrimSpace(req.FormValue("dns_servers"))
	var dnsServers []string
	for _, s := range strings.Split(dnsRaw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			dnsServers = append(dnsServers, s)
		}
	}

	// Build interface config from form fields.
	// Form field format: iface_<name>_address, iface_<name>_gateway, iface_<name>_dhcp
	ifaceMap := map[string]network.InterfaceConfig{}
	for key := range req.Form {
		if !strings.HasPrefix(key, "iface_") {
			continue
		}
		rest := key[len("iface_"):]
		// rest = <name>_<field>; field may contain underscores (e.g. "gateway")
		// We split on the last underscore to separate name from field.
		lastUS := strings.LastIndex(rest, "_")
		if lastUS < 0 {
			continue
		}
		ifName := rest[:lastUS]
		field := rest[lastUS+1:]
		cfg := ifaceMap[ifName]
		switch field {
		case "address":
			addr := strings.TrimSpace(req.FormValue(key))
			if addr != "" {
				cfg.Addresses = []string{addr}
			}
		case "gateway":
			cfg.Gateway4 = strings.TrimSpace(req.FormValue(key))
		case "dhcp":
			cfg.DHCP4 = req.FormValue(key) == "1"
		}
		ifaceMap[ifName] = cfg
	}

	cfg := network.NetplanConfig{
		Hostname:   hostname,
		Interfaces: ifaceMap,
		DNSServers: dnsServers,
	}

	extra := &networkExtra{}
	if h.deps.Manager != nil {
		if err := h.deps.Manager.ApplyConfig(req.Context(), cfg); err != nil {
			extra.ErrorMsg = h.r.tr.T(i18n.MsgNetworkErrApply, err.Error())
		} else {
			if os.Getenv("KURA_NETWORK_APPLY") == "1" {
				extra.SuccessMsg = h.r.tr.T(i18n.MsgNetworkApplySuccess)
			} else {
				extra.SuccessMsg = h.r.tr.T(i18n.MsgNetworkApplyDryRun)
			}
		}
	}

	// Reload info for display.
	if h.deps.Manager != nil {
		info, err := h.deps.Manager.GetInfo(req.Context())
		if err == nil {
			extra.Info = info
		}
	}

	data := h.r.buildPageData("network", i18n.MsgNetworkTitle)
	data.Extra = extra
	h.r.render(w, "templates/pages/network.tmpl", data)
}
