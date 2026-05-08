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
type SharesDeps struct {
	Engine       ShareEngine
	VolumeLister VolumeLister
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
	Delete(ctx context.Context, id string) error
}

// SharesView is the template view-model. Pre-translated strings hang off the
// row structs; the template renders them verbatim.
type SharesView struct {
	Subtitle  string
	HasShares bool
	Shares    []ShareRow

	// Selected is the share currently shown in the detail card. nil when the
	// list is empty.
	Selected *ShareRow

	Presets   []SharePresetOption
	Protocols []ShareProtocolOption
	Access    []ShareAccessOption

	// AvailableDatasets is the dataset picker source for the New Share form.
	// Empty when no VolumeLister is configured; the form then degrades to
	// free-text path entry.
	AvailableDatasets []DatasetOption

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
	ModeLabel string
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
		ACL:         parseACLCSV(req.FormValue("acl")),
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

	view := SharesView{
		Subtitle:          r.tr.T(i18n.MsgSharesSubtitle, len(rows)),
		HasShares:         len(rows) > 0,
		Shares:            rows,
		Presets:           sharePresetOptions(r.tr),
		Protocols:         shareProtocolOptions(r.tr),
		Access:            shareAccessOptions(r.tr),
		AvailableDatasets: datasets,
		FormError:         formError,
		DefaultsHeading:   r.tr.T(i18n.MsgSharesDefaultsHeading),
		DefaultsLead:      r.tr.T(i18n.MsgSharesDefaultsLead),
		Defaults:          shareDefaults(),
	}
	if len(rows) > 0 {
		// First row is the default selection (mirrors the prototype state).
		s := rows[0]
		view.Selected = &s
	}
	return view
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
			ModeLabel: aclModeLabel(tr, a.Mode),
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
// one place.
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
