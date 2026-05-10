// shares.go renders the /ui/admin/shares page from a share.Engine.
//
// Visual SSOT for the page is prototype/claude_design/Shares.html — list
// table + per-share detail card + best-defaults table. The prototype's
// React-driven detail panel is collapsed in v1 to a server-rendered card
// (htmx replacement comes in a polish sprint).
//
// This file deliberately exposes ZERO knobs for SMB performance / compat
// options (vfs objects, sendfile, fruit:*) — DESIGN_PRINCIPLES priority #2:
// 賢いデフォルト > 設定項目を増やす. The form fields are: name / path /
// protocol / preset / access mode / ACL csv / description.
package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kuraos-org/kura/engine/share"
	"github.com/kuraos-org/kura/engine/storage"
	"github.com/kuraos-org/kura/i18n"
)

// SharesDeps is what the shares handler needs from the binary. Engine is the
// share.Engine; the small interface lets tests inject a stub without booting
// SQLite or cmdexec. VolumeLister, when set, is consulted to populate the
// dataset picker in the New Share form so operators can pick from existing
// ZFS datasets instead of typing a path. Optional — when nil the form falls
// back to free-text entry.
//
// PrincipalSource (Sfix001-3) returns the user / group lists used by the
// ACL row picker. Optional — when nil the picker still renders but with
// empty <select> options, and the operator can fall back to the legacy
// `acl` text field (still parsed for backward compatibility).
type SharesDeps struct {
	Engine          ShareEngine
	VolumeLister    VolumeLister
	PrincipalSource PrincipalSource
}

// PrincipalSource returns the user / group identifiers the ACL picker
// presents to the operator. Username and group name are lowercased,
// matching the engine/user store normalisation, so the rendered <option>
// values round-trip cleanly through engine/share validation.
type PrincipalSource interface {
	ListUsernames(ctx context.Context) ([]string, error)
	ListGroupNames(ctx context.Context) ([]string, error)
}

// VolumeLister is the minimum interface the shares form needs to populate
// its dataset dropdown. The storage engine's CLI satisfies this naturally.
type VolumeLister interface {
	ListVolumes(ctx context.Context, pool string) ([]storage.VolumeInfo, error)
}

// ShareEngine is the subset of share.Engine the UI consumes. Defined locally
// so a stub implementation does not need to depend on engine/share.
type ShareEngine interface {
	List(ctx context.Context) ([]share.Share, error)
	Get(ctx context.Context, id string) (share.Share, error)
	Create(ctx context.Context, in share.CreateInput) (share.Share, error)
	Update(ctx context.Context, id string, in share.UpdateInput) (share.Share, error)
	Delete(ctx context.Context, id string) error
}

// SharesView is the template view-model. Pre-translated strings hang off the
// row structs; the template renders them verbatim.
type SharesView struct {
	Subtitle  string
	HasShares bool
	Shares    []ShareRow

	// Selected is the share currently shown in the detail card. nil when no
	// row has been clicked (the placeholder pane is shown instead).
	Selected *ShareRow
	// SelectedID is the row id from ?selected=<id>; the template uses it to
	// mark the active row in the list (aria-selected="true").
	SelectedID string

	Presets   []SharePresetOption
	Protocols []ShareProtocolOption
	Access    []ShareAccessOption

	// AvailableDatasets is the dataset picker source for the New Share form.
	// Empty when no VolumeLister is configured; the form then degrades to
	// free-text path entry.
	AvailableDatasets []DatasetOption

	// AvailableUsers / AvailableGroups feed the ACL row picker (Sfix001-3).
	// Empty slices produce empty <select> bodies — the picker still renders
	// the row template so the operator sees the entry-point button.
	AvailableUsers  []string
	AvailableGroups []string

	// ACLModeOptions is the dropdown contents for the per-row mode picker
	// (rw / r). Built by buildSharesView so the template stays free of
	// raw mode IDs.
	ACLModeOptions []ACLModeOption

	FormError string

	DefaultsHeading string
	DefaultsLead    string
	// Defaults lists the best-default knobs for the prototype's
	// "best defaults applied" card. Read-only.
	Defaults []ShareDefaultRow
}

// DatasetOption is one row in the New Share form's dataset dropdown.
// MountPoint, when set, is the recommended Path the operator probably wants
// (sharing the dataset's mountpoint rather than a sub-directory).
type DatasetOption struct {
	Name       string
	MountPoint string
}

// ShareRow is one entry in the shares table.
type ShareRow struct {
	ID            string
	Name          string
	ProtocolID    string
	ProtocolLabel string
	Path          string
	PresetID      string
	PresetLabel   string
	AccessID      string
	AccessLabel   string
	StatusID      string
	StatusLabel   string
	StatusClass   string
	Description   string
	ACL           []ShareACLRow
}

// ShareACLRow is one ACL entry rendered in the detail card.
type ShareACLRow struct {
	Token     string // user:alice / group:family
	Kind      string // "user" or "group" — pulled from share.PrincipalKind
	Name      string // principal name without the kind prefix
	ModeLabel string
	// ModeID is the raw mode code (rw / r / w) used by the edit form to
	// rebuild the parseACLCSV-compatible string.
	ModeID string
}

// SharePresetOption is one entry in the preset picker. Hint is shown beside
// the radio so the operator picks the right preset without learning ZFS
// recordsize / SMB fruit options.
type SharePresetOption struct {
	ID    string
	Label string
	Hint  string
}

// ShareProtocolOption is one entry in the protocol picker.
type ShareProtocolOption struct {
	ID    string
	Label string
}

// ShareAccessOption is one entry in the default-access picker.
type ShareAccessOption struct {
	ID    string
	Label string
}

// ShareDefaultRow is one row in the read-only "best defaults applied" card.
type ShareDefaultRow struct {
	Key   string
	Value string
	Note  string
}

// ACLModeOption is one entry in the per-row mode dropdown.
type ACLModeOption struct {
	ID    string
	Label string
}

// SharesHandler returns an http.Handler for /ui/admin/shares (GET = list +
// new-share form, POST = create). Mounted under the admin-role middleware
// by the gateway.
func (r *Renderer) SharesHandler(d SharesDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			r.handleSharesGet(w, req, d, "")
		case http.MethodPost:
			r.handleSharesPost(w, req, d)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	})
}

// SharesACLRowHandler returns a single blank ACL row (kind / name /
// mode / remove button) so the operator can append it to the picker
// via htmx. The handler reads the same view-model so user / group /
// mode options stay consistent with the form.
//
// hx-get target: /ui/admin/shares/acl-row, hx-swap=beforeend.
func (r *Renderer) SharesACLRowHandler(d SharesDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		view := r.buildSharesView(req.Context(), d, "")
		// `acl_kind` arrives via hx-include on the kind <select>'s
		// on-change swap (the form field shares the same name). When
		// the user toggles user↔group, the row is re-rendered with
		// only the matching name options so the middle dropdown never
		// offers a principal that doesn't belong to the selected kind.
		// Default to "user" so a fresh row (from the "+ ユーザー /
		// グループを追加" button, no query) starts on user.
		kind := req.URL.Query().Get("acl_kind")
		if kind != "user" && kind != "group" {
			kind = "user"
		}
		// Preserve the currently selected name (when compatible with
		// the new kind — empty/nonmatching ones fall through harmlessly
		// since the template only renders matching options) and mode
		// across the kind-toggle swap.
		frag := aclRowFragment{
			Users:        view.AvailableUsers,
			Groups:       view.AvailableGroups,
			Modes:        view.ACLModeOptions,
			SelectedKind: kind,
			SelectedName: req.URL.Query().Get("acl_name"),
			SelectedMode: req.URL.Query().Get("acl_mode"),
		}
		clone, err := r.templates.Clone()
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var buf bytes.Buffer
		if err := clone.ExecuteTemplate(&buf, "share-acl-row", frag); err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	})
}

// aclRowFragment is the view-model the share-acl-row partial uses for
// both the per-row append (htmx fragment) and the inline rendering of
// existing rows from the create / edit modal bodies.
type aclRowFragment struct {
	Users  []string
	Groups []string
	Modes  []ACLModeOption
	// For pre-filled rows from the edit modal:
	SelectedKind string
	SelectedName string
	SelectedMode string
}

// SharesUpdateHandler handles POST /ui/admin/shares/update. Form fields:
// id, protocol, preset, access_mode, description, disabled, acl. Name and
// path are intentionally NOT in the form — the engine's UpdateInput omits
// them so renames go through delete + recreate. On success redirects back
// to the detail pane (?selected=<id>) so the operator sees the result.
func (r *Renderer) SharesUpdateHandler(d SharesDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if err := req.ParseForm(); err != nil {
			r.handleSharesGet(w, req, d, r.tr.T(i18n.MsgSharesErrGeneric, err.Error()))
			return
		}
		id := strings.TrimSpace(req.FormValue("id"))
		if id == "" {
			r.handleSharesGet(w, req, d, r.tr.T(i18n.MsgSharesErrGeneric, "missing id"))
			return
		}
		in := share.UpdateInput{
			Protocol:    share.Protocol(req.FormValue("protocol")),
			Preset:      share.Preset(req.FormValue("preset")),
			AccessMode:  share.AccessMode(req.FormValue("access_mode")),
			Description: strings.TrimSpace(req.FormValue("description")),
			Disabled:    req.FormValue("disabled") == "1" || req.FormValue("disabled") == "on",
			ACL:         parseACLForm(req.Form),
		}
		if _, err := d.Engine.Update(req.Context(), id, in); err != nil {
			r.handleSharesGet(w, req, d, r.tr.T(i18n.MsgSharesErrGeneric, err.Error()))
			return
		}
		http.Redirect(w, req, "/ui/admin/shares?selected="+id, http.StatusSeeOther)
	})
}

// SharesDeleteHandler handles POST /ui/admin/shares/delete. Form field `id`
// is the share id. Always 303-redirects to the list on completion so the
// browser back button doesn't double-submit.
func (r *Renderer) SharesDeleteHandler(d SharesDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		_ = req.ParseForm()
		id := strings.TrimSpace(req.FormValue("id"))
		if id != "" {
			_ = d.Engine.Delete(req.Context(), id)
		}
		http.Redirect(w, req, "/ui/admin/shares", http.StatusSeeOther)
	})
}

func (r *Renderer) handleSharesGet(w http.ResponseWriter, req *http.Request, d SharesDeps, formError string) {
	view := r.buildSharesView(req.Context(), d, formError)
	// ?selected=<id> picks which row's detail card is shown. Empty / absent
	// means "show the placeholder asking the operator to pick a row" — the
	// previous auto-select-first behaviour was confusing because the right
	// pane didn't follow the row clicks (ROADMAP hotfix).
	if id := strings.TrimSpace(req.URL.Query().Get("selected")); id != "" {
		view.SelectedID = id
		view.Selected = nil
		for i := range view.Shares {
			if view.Shares[i].ID == id {
				s := view.Shares[i]
				view.Selected = &s
				break
			}
		}
	} else {
		view.SelectedID = ""
		view.Selected = nil
	}
	data := r.buildPageData("shares", i18n.MsgSharesTitle)
	data.Extra = view
	r.render(w, "templates/pages/shares.tmpl", data)
}

func (r *Renderer) handleSharesPost(w http.ResponseWriter, req *http.Request, d SharesDeps) {
	if err := req.ParseForm(); err != nil {
		r.handleSharesGet(w, req, d, r.tr.T(i18n.MsgSharesErrGeneric, err.Error()))
		return
	}
	// The form has a radio toggle: path_mode=dataset (default when datasets
	// are available) reads path_dataset (the selector's value); path_mode=
	// freetext reads path_freetext. Older callers / tests still send `path`
	// directly, so accept that as the fall-through.
	pathMode := strings.TrimSpace(req.FormValue("path_mode"))
	var path string
	switch pathMode {
	case "dataset":
		path = strings.TrimSpace(req.FormValue("path_dataset"))
	case "freetext":
		path = strings.TrimSpace(req.FormValue("path_freetext"))
	default:
		path = strings.TrimSpace(req.FormValue("path"))
		if path == "" {
			if v := strings.TrimSpace(req.FormValue("path_dataset")); v != "" {
				path = v
			} else {
				path = strings.TrimSpace(req.FormValue("path_freetext"))
			}
		}
	}
	in := share.CreateInput{
		Name:        strings.TrimSpace(req.FormValue("name")),
		Path:        path,
		Protocol:    share.Protocol(req.FormValue("protocol")),
		Preset:      share.Preset(req.FormValue("preset")),
		AccessMode:  share.AccessMode(req.FormValue("access_mode")),
		Description: strings.TrimSpace(req.FormValue("description")),
		ACL:         parseACLForm(req.Form),
	}
	if _, err := d.Engine.Create(req.Context(), in); err != nil {
		r.handleSharesGet(w, req, d, classifyShareErr(r.tr, err))
		return
	}
	http.Redirect(w, req, "/ui/admin/shares", http.StatusSeeOther)
}

// buildSharesView is split out so unit tests can exercise formatter logic
// without spinning up an http.Server.
func (r *Renderer) buildSharesView(ctx context.Context, d SharesDeps, formError string) SharesView {
	rows := []ShareRow{}
	if d.Engine != nil {
		shares, _ := d.Engine.List(ctx)
		for _, s := range shares {
			rows = append(rows, shareToRow(r.tr, s))
		}
	}
	// Pull dataset options for the New Share form. Pool roots (e.g. "tank")
	// are excluded so the operator picks a child dataset like tank/photos
	// — sharing the pool root itself is rare and surfaces the wrong
	// mountpoint by default.
	var datasets []DatasetOption
	if d.VolumeLister != nil {
		if vols, err := d.VolumeLister.ListVolumes(ctx, ""); err == nil {
			for _, v := range vols {
				if !strings.Contains(v.Name, "/") {
					continue // pool root
				}
				datasets = append(datasets, DatasetOption{
					Name:       v.Name,
					MountPoint: v.MountPoint,
				})
			}
		}
	}

	var users, groups []string
	if d.PrincipalSource != nil {
		users, _ = d.PrincipalSource.ListUsernames(ctx)
		groups, _ = d.PrincipalSource.ListGroupNames(ctx)
	}

	view := SharesView{
		Subtitle:          r.tr.T(i18n.MsgSharesSubtitle, len(rows)),
		HasShares:         len(rows) > 0,
		Shares:            rows,
		Presets:           sharePresetOptions(r.tr),
		Protocols:         shareProtocolOptions(r.tr),
		Access:            shareAccessOptions(r.tr),
		AvailableDatasets: datasets,
		AvailableUsers:    users,
		AvailableGroups:   groups,
		ACLModeOptions:    aclModeOptions(r.tr),
		FormError:         formError,
		DefaultsHeading:   r.tr.T(i18n.MsgSharesDefaultsHeading),
		DefaultsLead:      r.tr.T(i18n.MsgSharesDefaultsLead),
		Defaults:          shareDefaults(),
	}
	// Selection is decided by the handler's ?selected= parsing, not here —
	// the placeholder pane on the right is the no-selection default.
	return view
}

func aclModeOptions(tr translator) []ACLModeOption {
	return []ACLModeOption{
		{ID: "rw", Label: tr.T(i18n.MsgSharesACLModeRW)},
		{ID: "r", Label: tr.T(i18n.MsgSharesACLModeR)},
	}
}

// shareToRow flattens a share.Share into the renderable view-row.
func shareToRow(tr translator, s share.Share) ShareRow {
	row := ShareRow{
		ID:            s.ID,
		Name:          s.Name,
		ProtocolID:    string(s.Protocol),
		ProtocolLabel: shareProtocolLabel(tr, s.Protocol),
		Path:          s.Path,
		PresetID:      string(s.Preset),
		PresetLabel:   sharePresetLabel(tr, s.Preset),
		AccessID:      string(s.AccessMode),
		AccessLabel:   shareAccessLabel(tr, s.AccessMode),
		Description:   s.Description,
	}
	row.StatusID, row.StatusLabel, row.StatusClass = shareStatus(tr, s)
	for _, a := range s.ACL {
		token := string(a.Kind) + ":" + a.Name
		row.ACL = append(row.ACL, ShareACLRow{
			Token:     token,
			Kind:      string(a.Kind),
			Name:      a.Name,
			ModeLabel: aclModeLabel(tr, a.Mode),
			ModeID:    string(a.Mode),
		})
	}
	return row
}

func shareProtocolLabel(tr translator, p share.Protocol) string {
	switch p {
	case share.ProtocolSMB:
		return tr.T(i18n.MsgSharesProtocolSMB)
	case share.ProtocolNFS:
		return tr.T(i18n.MsgSharesProtocolNFS)
	case share.ProtocolBoth:
		return tr.T(i18n.MsgSharesProtocolBoth)
	}
	return string(p)
}

func sharePresetLabel(tr translator, p share.Preset) string {
	switch p {
	case share.PresetGeneral:
		return tr.T(i18n.MsgSharesPresetGeneral)
	case share.PresetMedia:
		return tr.T(i18n.MsgSharesPresetMedia)
	case share.PresetTimeMachine:
		return tr.T(i18n.MsgSharesPresetTimeMachine)
	case share.PresetDatabase:
		return tr.T(i18n.MsgSharesPresetDatabase)
	}
	return string(p)
}

func shareAccessLabel(tr translator, m share.AccessMode) string {
	switch m {
	case share.AccessReadOnly:
		return tr.T(i18n.MsgSharesAccessReadOnly)
	case share.AccessReadWrite:
		return tr.T(i18n.MsgSharesAccessReadWrite)
	}
	return string(m)
}

func aclModeLabel(tr translator, m share.ACLMode) string {
	switch m {
	case share.ACLModeReadWrite:
		return tr.T(i18n.MsgSharesAccessReadWrite)
	case share.ACLModeRead:
		return tr.T(i18n.MsgSharesAccessReadOnly)
	case share.ACLModeNone:
		return "—"
	}
	return string(m)
}

func shareStatus(tr translator, s share.Share) (id, label, class string) {
	if s.Disabled {
		return "disabled", tr.T(i18n.MsgSharesStatusDisabled), ""
	}
	return "active", tr.T(i18n.MsgSharesStatusActive), "ok"
}

func sharePresetOptions(tr translator) []SharePresetOption {
	return []SharePresetOption{
		{ID: string(share.PresetGeneral), Label: tr.T(i18n.MsgSharesPresetGeneral), Hint: tr.T(i18n.MsgSharesPresetGeneralHint)},
		{ID: string(share.PresetMedia), Label: tr.T(i18n.MsgSharesPresetMedia), Hint: tr.T(i18n.MsgSharesPresetMediaHint)},
		{ID: string(share.PresetTimeMachine), Label: tr.T(i18n.MsgSharesPresetTimeMachine), Hint: tr.T(i18n.MsgSharesPresetTimeMachineHint)},
		{ID: string(share.PresetDatabase), Label: tr.T(i18n.MsgSharesPresetDatabase), Hint: tr.T(i18n.MsgSharesPresetDatabaseHint)},
	}
}

func shareProtocolOptions(tr translator) []ShareProtocolOption {
	return []ShareProtocolOption{
		{ID: string(share.ProtocolSMB), Label: tr.T(i18n.MsgSharesProtocolSMB)},
		{ID: string(share.ProtocolNFS), Label: tr.T(i18n.MsgSharesProtocolNFS)},
		{ID: string(share.ProtocolBoth), Label: tr.T(i18n.MsgSharesProtocolBoth)},
	}
}

func shareAccessOptions(tr translator) []ShareAccessOption {
	return []ShareAccessOption{
		{ID: string(share.AccessReadWrite), Label: tr.T(i18n.MsgSharesAccessReadWrite)},
		{ID: string(share.AccessReadOnly), Label: tr.T(i18n.MsgSharesAccessReadOnly)},
	}
}

// shareDefaults returns the curated list of internally-fixed SMB options
// shown in the prototype's "best defaults applied" card. The list is
// hard-coded here because the values themselves are hard-coded in the smb
// template — surfacing them is purely informational.
func shareDefaults() []ShareDefaultRow {
	return []ShareDefaultRow{
		{Key: "server multi channel support", Value: "yes", Note: "複数 NIC で並列転送"},
		{Key: "use sendfile", Value: "yes", Note: "ゼロコピー転送"},
		{Key: "aio read/write size", Value: "1", Note: "非同期 I/O"},
		{Key: "vfs objects", Value: "catia fruit streams_xattr", Note: "macOS 互換"},
		{Key: "fruit:metadata", Value: "stream", Note: "macOS 互換"},
		{Key: "smb encrypt", Value: "desired", Note: "暗号化を優先"},
	}
}

// parseACLCSV parses comma-separated ACL entries of the form "user:NAME:rw"
// or "group:NAME:r". Empty input returns nil. Invalid tokens are silently
// dropped so the engine layer's validator surfaces the user-facing error in
// one place. Retained for backward compatibility — callers that POST the
// new row-form fields use parseACLRows directly.
func parseACLCSV(s string) []share.ACLEntry {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []share.ACLEntry
	for _, raw := range strings.Split(s, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parts := strings.SplitN(raw, ":", 3)
		if len(parts) != 3 {
			continue
		}
		out = append(out, share.ACLEntry{
			Kind: share.PrincipalKind(parts[0]),
			Name: parts[1],
			Mode: share.ACLMode(parts[2]),
		})
	}
	return out
}

// parseACLForm consumes the row-form fields (acl_kind[], acl_name[],
// acl_mode[]) emitted by the picker template. Falls back to parseACLCSV
// when the legacy `acl` text field is the only one present so older
// tests / direct API callers stay green.
//
// Empty rows (operator clicked "+ 追加" but never picked a name) are
// silently dropped so the engine's "principal not found" error is only
// produced when the operator actively selected a non-existent name.
func parseACLForm(form map[string][]string) []share.ACLEntry {
	kinds := form["acl_kind"]
	names := form["acl_name"]
	modes := form["acl_mode"]
	if len(kinds) == 0 && len(names) == 0 && len(modes) == 0 {
		// No row-form fields at all — fall back to the legacy CSV input.
		if vals, ok := form["acl"]; ok && len(vals) > 0 {
			return parseACLCSV(vals[0])
		}
		return nil
	}
	n := len(kinds)
	if len(names) < n {
		n = len(names)
	}
	if len(modes) < n {
		n = len(modes)
	}
	var out []share.ACLEntry
	for i := 0; i < n; i++ {
		kind := strings.TrimSpace(kinds[i])
		name := strings.TrimSpace(names[i])
		mode := strings.TrimSpace(modes[i])
		if name == "" {
			continue
		}
		out = append(out, share.ACLEntry{
			Kind: share.PrincipalKind(kind),
			Name: name,
			Mode: share.ACLMode(mode),
		})
	}
	return out
}

// classifyShareErr maps engine sentinel errors to translated UI messages.
// Unknown errors fall through to a generic "create failed" string so the
// developer-side wrapping stays out of the page.
func classifyShareErr(tr *i18n.Translator, err error) string {
	switch {
	case errors.Is(err, share.ErrInvalidName):
		return tr.T(i18n.MsgSharesErrInvalidName)
	case errors.Is(err, share.ErrInvalidPath):
		return tr.T(i18n.MsgSharesErrInvalidPath)
	case errors.Is(err, share.ErrInvalidProtocol):
		return tr.T(i18n.MsgSharesErrInvalidProtocol)
	case errors.Is(err, share.ErrInvalidPreset):
		return tr.T(i18n.MsgSharesErrInvalidPreset)
	case errors.Is(err, share.ErrInvalidAccessMode):
		return tr.T(i18n.MsgSharesErrInvalidAccess)
	case errors.Is(err, share.ErrInvalidACL):
		return tr.T(i18n.MsgSharesErrInvalidACL)
	case errors.Is(err, share.ErrNameTaken):
		return tr.T(i18n.MsgSharesErrNameTaken)
	case errors.Is(err, share.ErrPathConflict):
		return tr.T(i18n.MsgSharesErrPathConflict)
	case errors.Is(err, share.ErrPathOutsideVolume):
		return tr.T(i18n.MsgSharesErrPathOutsideVolume)
	case errors.Is(err, share.ErrTestparmFailed):
		return tr.T(i18n.MsgSharesErrTestparmFailed)
	case errors.Is(err, share.ErrReloadFailed):
		return tr.T(i18n.MsgSharesErrReloadFailed)
	case errors.Is(err, share.ErrUnknownPrincipal):
		return tr.T(i18n.MsgSharesErrUnknownPrincipal)
	}
	return tr.T(i18n.MsgSharesErrCreateFailed, fmt.Sprintf("%v", err))
}
