package share

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuraos-org/kura/engine/share/nfs"
	"github.com/kuraos-org/kura/engine/share/smb"
	"github.com/kuraos-org/kura/internal/cmdexec"
)

// Manager is the production Engine. It bundles the SQLite store, a
// CmdExecutor for systemctl / testparm / exportfs, and the file paths where
// the rendered conf files land. Every IO seam is injectable so tests can
// drive the manager without a real Samba install.
//
// Defaults align with Debian / Ubuntu samba & nfs-kernel-server packages:
//
//	/etc/samba/conf.d/kura.conf (Samba's `include` glob picks this up)
//	/etc/exports.d/kura.exports (nfs-utils' default include path)
type Manager struct {
	store *Store
	exec  cmdexec.Executor

	smbConfPath  string
	exportsPath  string
	systemctlBin string
	testparmBin  string
	exportfsBin  string

	// VolumeChecker, when non-nil, is consulted by Create to confirm the
	// requested path lives under a known ZFS volume. Engines without a
	// storage hook can leave it nil; Create then accepts any well-formed path.
	checker VolumeChecker
}

// VolumeChecker is the seam against engine/storage. The interface is local
// to engine/share so we don't import storage and create a cycle. Production
// wiring passes a tiny adapter that calls storage.Engine.ListPools and
// derives accepted prefixes.
type VolumeChecker interface {
	// PathBelongsToVolume reports whether `p` is rooted in a known ZFS
	// dataset (e.g. /tank/photos under pool "tank"). Returning false from a
	// configured checker triggers ErrPathOutsideVolume.
	PathBelongsToVolume(ctx context.Context, p string) (bool, error)
}

// Options configures NewManager. Empty fields fall back to production
// defaults; tests routinely override SMBConfPath / ExportsPath to a temp dir.
type Options struct {
	SMBConfPath  string
	ExportsPath  string
	SystemctlBin string
	TestparmBin  string
	ExportfsBin  string
	Checker      VolumeChecker
}

// NewManager constructs a Manager. The Executor must be non-nil; pass
// cmdexec.NewReal() in production and cmdexec.NewFake() in tests.
func NewManager(store *Store, exec cmdexec.Executor, opts Options) *Manager {
	if opts.SMBConfPath == "" {
		opts.SMBConfPath = "/etc/samba/conf.d/kura.conf"
	}
	if opts.ExportsPath == "" {
		opts.ExportsPath = "/etc/exports.d/kura.exports"
	}
	if opts.SystemctlBin == "" {
		opts.SystemctlBin = "systemctl"
	}
	if opts.TestparmBin == "" {
		opts.TestparmBin = "testparm"
	}
	if opts.ExportfsBin == "" {
		opts.ExportfsBin = "exportfs"
	}
	return &Manager{
		store:        store,
		exec:         exec,
		smbConfPath:  opts.SMBConfPath,
		exportsPath:  opts.ExportsPath,
		systemctlBin: opts.SystemctlBin,
		testparmBin:  opts.TestparmBin,
		exportfsBin:  opts.ExportfsBin,
		checker:      opts.Checker,
	}
}

// List returns every share, sorted by name.
func (m *Manager) List(ctx context.Context) ([]Share, error) {
	return m.store.List(ctx)
}

// Get returns the share with id, or ErrShareNotFound.
func (m *Manager) Get(ctx context.Context, id string) (Share, error) {
	return m.store.Get(ctx, id)
}

// Create validates input, checks for duplicates / nesting against existing
// shares, persists the row, then triggers a full template regeneration +
// reload. Any failure during regeneration aborts before the row sticks.
//
// Idempotency: re-Create with the same name returns ErrNameTaken — callers
// who want upsert semantics use Apply (driven from config.json).
func (m *Manager) Create(ctx context.Context, in CreateInput) (Share, error) {
	if err := in.Validate(); err != nil {
		return Share{}, err
	}
	if m.checker != nil {
		ok, err := m.checker.PathBelongsToVolume(ctx, in.Path)
		if err != nil {
			return Share{}, fmt.Errorf("share: volume check: %w", err)
		}
		if !ok {
			return Share{}, fmt.Errorf("%w: %q", ErrPathOutsideVolume, in.Path)
		}
	}
	existing, err := m.store.List(ctx)
	if err != nil {
		return Share{}, err
	}
	if err := checkPathConflict(in.Path, existing); err != nil {
		return Share{}, err
	}

	sh := Share{
		Name:        in.Name,
		Path:        in.Path,
		Protocol:    in.Protocol,
		Preset:      in.Preset,
		AccessMode:  in.AccessMode,
		Description: in.Description,
		ACL:         append([]ACLEntry(nil), in.ACL...),
	}
	created, err := m.store.Create(ctx, sh)
	if err != nil {
		return Share{}, err
	}
	if err := m.Apply(ctx); err != nil {
		// Roll back the row if regeneration fails — leaving an orphan share
		// the smbd doesn't see is worse than a clean failure.
		_ = m.store.Delete(ctx, created.ID)
		return Share{}, err
	}
	return created, nil
}

// Delete removes the share and regenerates the conf files. The store +
// regenerate pair lives behind one method so the conf is never out of sync
// with the table.
func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	return m.Apply(ctx)
}

// Apply is the idempotent regeneration entry point. It (re)writes the
// generated conf files from the current SQLite state, validates the smb.conf
// via testparm, then reloads smbd / nfs-server through systemctl. The whole
// pipeline tolerates being called when nothing has changed — that's the
// point of declarative config.
//
// DESIGN_PRINCIPLES priority #1: SSOT. SQLite is read; the conf files are
// fully recomputed. Any manual edits the operator made are clobbered.
func (m *Manager) Apply(ctx context.Context) error {
	shares, err := m.store.List(ctx)
	if err != nil {
		return err
	}

	smbBody, nfsBody, err := m.render(shares)
	if err != nil {
		return err
	}

	if err := m.writeFile(m.smbConfPath, smbBody); err != nil {
		return fmt.Errorf("share: write smb conf: %w", err)
	}
	if err := m.writeFile(m.exportsPath, nfsBody); err != nil {
		return fmt.Errorf("share: write exports: %w", err)
	}

	// testparm -s validates the generated smb.conf against samba's parser.
	// We feed the path so a syntax error surfaces *before* we reload smbd
	// and risk a half-loaded daemon. testparm exits non-zero on syntactic
	// problems; the engine surfaces ErrTestparmFailed for the UI.
	if _, _, err := m.exec.Run(ctx, m.testparmBin, "-s", "--suppress-prompt", m.smbConfPath); err != nil {
		return fmt.Errorf("%w: %v", ErrTestparmFailed, err)
	}

	hasSMB := false
	hasNFS := false
	for _, s := range shares {
		if !s.Disabled && s.Protocol.HasSMB() {
			hasSMB = true
		}
		if !s.Disabled && s.Protocol.HasNFS() {
			hasNFS = true
		}
	}

	// We always reload — empty conf files are valid, and reload is cheap.
	// `try-reload-or-restart` is the systemd verb that reloads when possible
	// and falls back to restart, which matches Samba's recommended flow.
	if _, _, err := m.exec.Run(ctx, m.systemctlBin, "reload", "smbd"); err != nil {
		// Reload failures bubble up; the UI maps to a translated message.
		// Note: we deliberately do not retry — repeated failures from the
		// same root cause just spam logs.
		_ = hasSMB // hasSMB observed for future "skip reload when no SMB shares" optimization (v1.x)
		return fmt.Errorf("%w: smbd: %v", ErrReloadFailed, err)
	}
	if _, _, err := m.exec.Run(ctx, m.exportfsBin, "-ra"); err != nil {
		_ = hasNFS
		return fmt.Errorf("%w: exportfs -ra: %v", ErrReloadFailed, err)
	}
	if _, _, err := m.exec.Run(ctx, m.systemctlBin, "reload", "nfs-server"); err != nil {
		return fmt.Errorf("%w: nfs-server: %v", ErrReloadFailed, err)
	}

	return nil
}

// Render is exposed for callers that want to preview the would-be config
// files (golden tests, dry-run apply). It does not touch the filesystem or
// the daemons.
func (m *Manager) Render(ctx context.Context) (smbConf, exports []byte, err error) {
	shares, err := m.store.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	return m.render(shares)
}

func (m *Manager) render(shares []Share) (smbConf, exports []byte, err error) {
	smbInput := smb.Input{}
	nfsInput := nfs.Input{}
	for _, s := range shares {
		if s.Disabled {
			continue
		}
		if s.Protocol.HasSMB() {
			v, err := buildSMBView(s)
			if err != nil {
				return nil, nil, err
			}
			smbInput.Shares = append(smbInput.Shares, v)
		}
		if s.Protocol.HasNFS() {
			nfsInput.Shares = append(nfsInput.Shares, buildNFSView(s))
		}
	}
	smbBody, err := smb.RenderConfig(smbInput)
	if err != nil {
		return nil, nil, err
	}
	nfsBody, err := nfs.RenderExports(nfsInput)
	if err != nil {
		return nil, nil, err
	}
	return smbBody, nfsBody, nil
}

// writeFile creates the parent dir and writes b atomically (write to
// `<path>.tmp`, then rename). The atomic step matters for testparm — a
// half-written smb.conf would fail validation for the wrong reason.
func (m *Manager) writeFile(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", tmp, path, err)
	}
	return nil
}

// checkPathConflict rejects paths that exactly equal or strictly nest under
// an existing share path. Strict nesting catches the operator who exports
// /tank/docs and then tries to also export /tank/docs/reports — this v1
// behaviour is conservative; a future sprint may relax to "warn instead of
// reject" once the UI can disambiguate ACLs.
func checkPathConflict(p string, existing []Share) error {
	for _, s := range existing {
		if s.Path == p {
			return fmt.Errorf("%w: %q (existing share %q)", ErrPathConflict, p, s.Name)
		}
		if strings.HasPrefix(p, s.Path+"/") {
			return fmt.Errorf("%w: %q nested under %q", ErrPathConflict, p, s.Path)
		}
		if strings.HasPrefix(s.Path, p+"/") {
			return fmt.Errorf("%w: %q would contain existing %q", ErrPathConflict, p, s.Path)
		}
	}
	return nil
}
