// storage_write.go hosts the POST handlers that mutate storage state:
// /ui/admin/storage/pools (create), /ui/admin/storage/snapshots (create +
// rollback), /ui/admin/storage/import (import).
//
// All routes share the same shape: parse form -> validate via engine ->
// either redirect back to the storage page with a flash, or re-render with
// an inline error. Errors are mapped to translated MessageIDs via
// classifyStorageWriteErr so we never splat raw zfs/zpool stderr into the
// page (DESIGN_PRINCIPLES forbidden: "Docker / ZFS / SMB の CLI 出力を直接
// 画面に表示しない").
package ui

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kuraos-org/kura/engine/storage"
	"github.com/kuraos-org/kura/i18n"
)

// StorageWriteHandler returns the http.Handler that mounts the mutation
// routes under /ui/admin/storage/. Read handler stays separate (StorageHandler).
func (r *Renderer) StorageWriteHandler(d StorageDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ui/admin/storage/pools", r.handleCreatePool(d))
	mux.HandleFunc("/ui/admin/storage/import", r.handleImportPool(d))
	mux.HandleFunc("/ui/admin/storage/snapshots", r.handleCreateSnapshot(d))
	return mux
}

func (r *Renderer) handleCreatePool(d StorageDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if d.Writer == nil {
			http.Error(w, "storage writer not configured", http.StatusServiceUnavailable)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		cfg, err := parsePoolForm(req)
		if err != nil {
			r.writeStorageError(w, err)
			return
		}
		if err := d.Writer.CreatePool(req.Context(), cfg); err != nil {
			r.writeStorageError(w, err)
			return
		}
		// Successful create: redirect back to storage page. The HTMX-friendly
		// HX-Redirect header is set so a fetch request triggers a navigation
		// without us hand-rolling a JS reload.
		w.Header().Set("HX-Redirect", "/ui/admin/storage")
		http.Redirect(w, req, "/ui/admin/storage", http.StatusSeeOther)
	}
}

func (r *Renderer) handleImportPool(d StorageDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if d.Writer == nil {
			http.Error(w, "storage writer not configured", http.StatusServiceUnavailable)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(req.FormValue("name"))
		if name == "" {
			r.writeStorageError(w, fmt.Errorf("%w: empty", storage.ErrPoolNameInvalid))
			return
		}
		opts := storage.ImportOpts{
			Force:    req.FormValue("force") == "1" || req.FormValue("force") == "on",
			ReadOnly: req.FormValue("readonly") == "1" || req.FormValue("readonly") == "on",
			AltRoot:  strings.TrimSpace(req.FormValue("altroot")),
		}
		if err := d.Writer.ImportPool(req.Context(), name, opts); err != nil {
			r.writeStorageError(w, err)
			return
		}
		w.Header().Set("HX-Redirect", "/ui/admin/storage")
		http.Redirect(w, req, "/ui/admin/storage", http.StatusSeeOther)
	}
}

func (r *Renderer) handleCreateSnapshot(d StorageDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if d.Writer == nil {
			http.Error(w, "storage writer not configured", http.StatusServiceUnavailable)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		dataset := strings.TrimSpace(req.FormValue("dataset"))
		name := strings.TrimSpace(req.FormValue("name"))
		if err := d.Writer.CreateSnapshot(req.Context(), dataset, name); err != nil {
			r.writeStorageError(w, err)
			return
		}
		w.Header().Set("HX-Redirect", "/ui/admin/storage")
		http.Redirect(w, req, "/ui/admin/storage", http.StatusSeeOther)
	}
}

// parsePoolForm turns the New Pool form into a PoolConfig.
//
// The form uses multi-value checkbox inputs (one <input name="data_disks">
// per free disk), so disks arrive as req.PostForm["data_disks"] rather than
// a single textarea blob. ForceNoRedundancy is *deliberately* not parsed —
// the form has no field for it. CLI-only escape hatch
// (DESIGN_PRINCIPLES forbidden #14).
func parsePoolForm(req *http.Request) (storage.PoolConfig, error) {
	cfg := storage.PoolConfig{
		Name: strings.TrimSpace(req.FormValue("name")),
	}
	dataLayout := storage.VdevLayout(strings.TrimSpace(req.FormValue("data_layout")))
	cfg.Data = storage.VdevSpec{
		Layout: dataLayout,
		Disks:  trimAll(req.PostForm["data_disks"]),
	}

	specialLayout := strings.TrimSpace(req.FormValue("special_layout"))
	specialDisks := trimAll(req.PostForm["special_disks"])
	if specialLayout != "" && len(specialDisks) > 0 {
		cfg.Special = &storage.VdevSpec{
			Layout: storage.VdevLayout(specialLayout),
			Disks:  specialDisks,
		}
	}

	cfg.Spares = trimAll(req.PostForm["spares"])
	if v := strings.TrimSpace(req.FormValue("small_block_threshold")); v != "" {
		// Accept simple bytes; values like "32K" stay encoded into ZFS
		// directly via SmallBlockThreshold. For UI simplicity we only accept
		// integer bytes here — the CLI subcommand can take human-readable
		// suffixes if needed.
		if n, err := parseInt64(v); err == nil {
			cfg.SmallBlockThreshold = n
		}
	}
	return cfg, nil
}

// trimAll returns ss with leading/trailing whitespace stripped from each
// element and empty entries dropped. Used by the multi-value disk-picker
// fields where each checkbox produces one PostForm value.
func trimAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		t := strings.TrimSpace(s)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseInt64(s string) (int64, error) {
	var v int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		v = v*10 + int64(r-'0')
	}
	return v, nil
}

// writeStorageError maps an engine error to a translated message and writes
// a 400 with the message. The body is plain text (one sentence) so htmx
// callers can show it as an inline error and curl users get a readable
// response. Keeping the format simple sidesteps re-rendering the whole page
// with form state preserved — which is doable but not in scope for this
// sprint.
func (r *Renderer) writeStorageError(w http.ResponseWriter, err error) {
	id, args := classifyStorageWriteErr(err)
	msg := r.tr.T(id, args...)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(msg))
}

// classifyStorageWriteErr maps a wrapped storage engine error to a
// MessageID. Sentinel errors from validation.go are matched via errors.Is;
// catch-all branches map "create" / "import" to broad messages so the
// operator at least gets a translated category.
func classifyStorageWriteErr(err error) (i18n.MessageID, []any) {
	switch {
	case errors.Is(err, storage.ErrPoolNameInvalid):
		return i18n.MsgStorageErrPoolNameInvalid, []any{trimPrefixOrEmpty(err.Error(), "storage: pool name invalid: ")}
	case errors.Is(err, storage.ErrLayoutMinDisks):
		return i18n.MsgStorageErrLayoutMinDisks, nil
	case errors.Is(err, storage.ErrSpecialNotRedundant):
		return i18n.MsgStorageErrSpecialNotRedundant, nil
	case errors.Is(err, storage.ErrDuplicateDisk):
		return i18n.MsgStorageErrDuplicateDisk, nil
	case errors.Is(err, storage.ErrSmallBlocksWithoutSpecial):
		return i18n.MsgStorageErrSmallBlocksWithoutSpecial, nil
	case errors.Is(err, storage.ErrPresetUnknown):
		return i18n.MsgStorageErrPresetUnknown, nil
	}
	// Pool import failures (zpool import non-zero) bubble up here; we route
	// them to a different message than create failures so the operator knows
	// which step blew up. Heuristic on the wrapped string is good enough —
	// engine.go always wraps with "storage: zpool import ..." or
	// "storage: zpool create ..." so the prefix is stable.
	es := err.Error()
	switch {
	case strings.Contains(es, "zpool import"):
		return i18n.MsgStorageErrImportFailed, []any{shortError(es)}
	}
	return i18n.MsgStorageErrCreateFailed, []any{shortError(es)}
}

func trimPrefixOrEmpty(s, prefix string) string {
	if strings.HasPrefix(s, prefix) {
		return strings.TrimPrefix(s, prefix)
	}
	return ""
}

// shortError trims the developer-facing prefix and stops at the first colon
// of the underlying %w wrap so the message stays compact in the UI without
// leaking raw CLI tokens (the wrap chain is short by construction).
func shortError(s string) string {
	if i := strings.Index(s, ": "); i > 0 {
		return s[:i]
	}
	return s
}
