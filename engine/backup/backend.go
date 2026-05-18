// Package backup implements the BackupBackend interface (Se1e7a6-2) and the
// cron-based snapshot scheduler with retention (Se1e7a6-1).
//
// BackupBackend interface lets tests inject a fakeBackend without any real CLI
// calls — DESIGN_PRINCIPLES priority #9. Production backends shell out through
// cmdexec.Executor so the same seam serves both unit tests and real VMs.
//
// Hierarchy:
//
//	BackupBackend       — Send / Restore / List interface (consumer-side, defined here)
//	├─ ZFSSendBackend   — zfs send | ssh + zfs receive
//	├─ ResticBackend    — restic CLI wrapper
//	└─ RcloneBackend    — rclone CLI wrapper
package backup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kuraos-org/kura/internal/cmdexec"
)

// BackupEntry is one remote snapshot/backup as reported by List.
type BackupEntry struct {
	// ID is the backend-specific identifier (snapshot name, restic snapshot ID, etc.)
	ID string `json:"id"`
	// Dataset is the ZFS dataset this backup covers ("" for restic/rclone archives).
	Dataset string `json:"dataset,omitempty"`
	// CreatedAt is when the backup was taken.
	CreatedAt time.Time `json:"created_at"`
	// SizeBytes is the transferred/stored size (0 if unknown).
	SizeBytes int64 `json:"size_bytes,omitempty"`
}

// RestoreOpts carries per-restore configuration.
type RestoreOpts struct {
	// SnapshotID selects which backup to restore. For ZFS backends this is the
	// remote snapshot name; for restic it is the snapshot ID.
	SnapshotID string
	// TargetDataset is the local ZFS dataset to receive into (ZFS backends).
	TargetDataset string
}

// BackupBackend is the interface every offsite backend must satisfy.
// Declared in the consumer package so engine/backup controls the seam
// (DESIGN_PRINCIPLES priority #9 / Go duck-typing convention).
type BackupBackend interface {
	// Send transmits a ZFS dataset snapshot to the backend destination.
	// dataset is the local ZFS dataset path (e.g. "tank/photos"),
	// snapshot is the snapshot name (e.g. "auto-2026-05-18T00:00").
	Send(ctx context.Context, dataset, snapshot string) error

	// Restore pulls a previously sent backup and writes it to the local pool.
	Restore(ctx context.Context, opts RestoreOpts) error

	// List enumerates backups available in the backend destination.
	List(ctx context.Context) ([]BackupEntry, error)
}

// --------------------------------------------------------------------------
// ZFSSendBackend
// --------------------------------------------------------------------------

// ZFSSendConfig is the declarative config for a zfs-send backend.
// SSH private key path is stored in vault (credential_state in config.json).
type ZFSSendConfig struct {
	// Host is the SSH target host (user@host).
	Host string `json:"host"`
	// RemoteDataset is the ZFS dataset on the remote host to receive into.
	RemoteDataset string `json:"remote_dataset"`
	// SSHKeyPath is the path to the private key on the local system (from vault).
	SSHKeyPath string `json:"ssh_key_path,omitempty"`
	// Port overrides the default SSH port (22).
	Port int `json:"port,omitempty"`
}

// ZFSSendBackend implements BackupBackend using `zfs send | ssh zfs receive`.
// Requires the remote host to have ZFS installed and `zfs receive` accessible
// to the configured SSH user.
type ZFSSendBackend struct {
	exec cmdexec.Executor
	cfg  ZFSSendConfig
}

// NewZFSSendBackend constructs the production ZFS-send backend.
func NewZFSSendBackend(exec cmdexec.Executor, cfg ZFSSendConfig) *ZFSSendBackend {
	return &ZFSSendBackend{exec: exec, cfg: cfg}
}

// sshArgs builds the base ssh argument list, including optional key and port.
func (b *ZFSSendBackend) sshArgs() []string {
	args := []string{}
	if b.cfg.SSHKeyPath != "" {
		args = append(args, "-i", b.cfg.SSHKeyPath)
	}
	port := b.cfg.Port
	if port == 0 {
		port = 22
	}
	args = append(args, "-p", fmt.Sprintf("%d", port), "-o", "StrictHostKeyChecking=accept-new")
	return args
}

// Send shells out: zfs send tank/photos@snap | ssh host zfs receive remote/tank/photos
// We use the cmdexec.Executor for both zfs send (piped to ssh); since
// Executor.Run doesn't support pipe natively, we wrap it in sh -c to get the
// shell pipeline.
func (b *ZFSSendBackend) Send(ctx context.Context, dataset, snapshot string) error {
	snapRef := dataset + "@" + snapshot
	remote := b.cfg.RemoteDataset + "/" + lastComponent(dataset)

	sshParts := append([]string{"ssh"}, b.sshArgs()...)
	sshParts = append(sshParts, b.cfg.Host, "zfs", "receive", "-F", remote)
	pipeline := fmt.Sprintf("zfs send -R %s | %s", snapRef, strings.Join(sshParts, " "))

	if _, _, err := b.exec.Run(ctx, "sh", "-c", pipeline); err != nil {
		return fmt.Errorf("backup: zfs_send %s: %w", snapRef, err)
	}
	return nil
}

// Restore pulls a specific remote snapshot back to the local pool.
func (b *ZFSSendBackend) Restore(ctx context.Context, opts RestoreOpts) error {
	remoteDataset := b.cfg.RemoteDataset + "/" + lastComponent(opts.TargetDataset)
	remoteSnap := remoteDataset + "@" + opts.SnapshotID

	sshParts := append([]string{"ssh"}, b.sshArgs()...)
	sshParts = append(sshParts, b.cfg.Host, "zfs", "send", "-R", remoteSnap)
	pipeline := fmt.Sprintf("%s | zfs receive -F %s", strings.Join(sshParts, " "), opts.TargetDataset)

	if _, _, err := b.exec.Run(ctx, "sh", "-c", pipeline); err != nil {
		return fmt.Errorf("backup: zfs_send restore %s: %w", remoteSnap, err)
	}
	return nil
}

// List enumerates snapshots present in the remote dataset via ssh zfs list.
func (b *ZFSSendBackend) List(ctx context.Context) ([]BackupEntry, error) {
	remoteDataset := b.cfg.RemoteDataset

	sshParts := append([]string{"ssh"}, b.sshArgs()...)
	sshParts = append(sshParts, b.cfg.Host, "zfs", "list", "-H", "-t", "snapshot", "-o", "name,creation", "-p", remoteDataset)
	cmd := strings.Join(sshParts, " ")

	out, _, err := b.exec.Run(ctx, "sh", "-c", cmd)
	if err != nil {
		return nil, fmt.Errorf("backup: zfs_send list: %w", err)
	}
	return parseZFSSnapList(string(out))
}

// --------------------------------------------------------------------------
// ResticBackend
// --------------------------------------------------------------------------

// ResticConfig is the declarative config for a restic backend.
type ResticConfig struct {
	// Repo is the restic repository URL (e.g. "s3:s3.amazonaws.com/my-bucket/kura").
	Repo string `json:"repo"`
	// PasswordCredentialState is "set"|"unset" (password lives in vault).
	PasswordCredentialState string `json:"password_credential_state"`
	// Password is only used at runtime — never stored in config.json export.
	Password string `json:"-"`
}

// ResticBackend implements BackupBackend using the restic CLI.
type ResticBackend struct {
	exec cmdexec.Executor
	cfg  ResticConfig
}

// NewResticBackend constructs the production restic backend.
func NewResticBackend(exec cmdexec.Executor, cfg ResticConfig) *ResticBackend {
	return &ResticBackend{exec: exec, cfg: cfg}
}

// Send backs up the local path of the given dataset to the restic repository.
// dataset is a ZFS dataset path (e.g. "tank/photos"); the local mountpoint
// is derived by convention (/tank/photos) for the real implementation.
// The snapshot parameter is used as a restic tag for identification.
func (b *ResticBackend) Send(ctx context.Context, dataset, snapshot string) error {
	mountpoint := "/" + dataset
	args := []string{
		"-r", b.cfg.Repo,
		"backup",
		"--tag", snapshot,
		"--tag", dataset,
		mountpoint,
	}
	_, _, err := b.exec.Run(ctx, "restic", args...)
	if err != nil {
		return fmt.Errorf("backup: restic send %s@%s: %w", dataset, snapshot, err)
	}
	return nil
}

// Restore extracts a restic snapshot to the target dataset's mountpoint.
func (b *ResticBackend) Restore(ctx context.Context, opts RestoreOpts) error {
	mountpoint := "/" + opts.TargetDataset
	args := []string{
		"-r", b.cfg.Repo,
		"restore", opts.SnapshotID,
		"--target", mountpoint,
	}
	_, _, err := b.exec.Run(ctx, "restic", args...)
	if err != nil {
		return fmt.Errorf("backup: restic restore %s: %w", opts.SnapshotID, err)
	}
	return nil
}

// List returns all restic snapshots in the repository.
func (b *ResticBackend) List(ctx context.Context) ([]BackupEntry, error) {
	args := []string{"-r", b.cfg.Repo, "snapshots", "--json"}
	out, _, err := b.exec.Run(ctx, "restic", args...)
	if err != nil {
		return nil, fmt.Errorf("backup: restic list: %w", err)
	}
	return parseResticSnapshots(out)
}

// --------------------------------------------------------------------------
// RcloneBackend
// --------------------------------------------------------------------------

// RcloneConfig is the declarative config for an rclone backend.
type RcloneConfig struct {
	// Remote is the rclone remote name + path (e.g. "myremote:backups/kura").
	Remote string `json:"remote"`
	// CredentialState is "set"|"unset" (token lives in vault).
	CredentialState string `json:"credential_state"`
}

// RcloneBackend implements BackupBackend using the rclone CLI.
type RcloneBackend struct {
	exec cmdexec.Executor
	cfg  RcloneConfig
}

// NewRcloneBackend constructs the production rclone backend.
func NewRcloneBackend(exec cmdexec.Executor, cfg RcloneConfig) *RcloneBackend {
	return &RcloneBackend{exec: exec, cfg: cfg}
}

// Send syncs the local dataset mountpoint to the rclone remote.
func (b *RcloneBackend) Send(ctx context.Context, dataset, snapshot string) error {
	mountpoint := "/" + dataset
	dest := b.cfg.Remote + "/" + strings.ReplaceAll(dataset, "/", "_") + "/" + snapshot
	args := []string{"sync", mountpoint, dest, "--checksum"}
	_, _, err := b.exec.Run(ctx, "rclone", args...)
	if err != nil {
		return fmt.Errorf("backup: rclone send %s@%s: %w", dataset, snapshot, err)
	}
	return nil
}

// Restore syncs the remote snapshot directory back to the local mountpoint.
func (b *RcloneBackend) Restore(ctx context.Context, opts RestoreOpts) error {
	mountpoint := "/" + opts.TargetDataset
	src := b.cfg.Remote + "/" + strings.ReplaceAll(opts.TargetDataset, "/", "_") + "/" + opts.SnapshotID
	args := []string{"sync", src, mountpoint, "--checksum"}
	_, _, err := b.exec.Run(ctx, "rclone", args...)
	if err != nil {
		return fmt.Errorf("backup: rclone restore %s: %w", opts.SnapshotID, err)
	}
	return nil
}

// List enumerates directories (one per snapshot) under the remote path.
func (b *RcloneBackend) List(ctx context.Context) ([]BackupEntry, error) {
	args := []string{"lsd", b.cfg.Remote, "--format=n"}
	out, _, err := b.exec.Run(ctx, "rclone", args...)
	if err != nil {
		return nil, fmt.Errorf("backup: rclone list: %w", err)
	}
	return parseRcloneDirs(string(out))
}

// --------------------------------------------------------------------------
// Helper parsers
// --------------------------------------------------------------------------

func lastComponent(path string) string {
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	return parts[len(parts)-1]
}
