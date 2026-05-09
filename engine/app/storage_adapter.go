package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// StorageAdapter is the production DatasetWriter. It wraps an engine/storage
// Engine plus a CmdExecutor for the snapshot operations not yet exposed
// via the storage Engine surface.
//
// We do not import engine/storage directly here to keep the dependency arrow
// pointing one way (storage knows nothing about apps). Callers in cmd/kura
// wire the concrete adapter against the live Engine.
type StorageAdapter struct {
	Engine StorageDatasetEngine
}

// StorageDatasetEngine is the slice of engine/storage.Engine the adapter
// needs. cmd/kura assigns engine/storage.CLI to this field — both interfaces
// match by method set so no glue is required.
type StorageDatasetEngine interface {
	CreateVolume(ctx context.Context, dataset string, opts StorageVolumeOpts) error
	SetQuota(ctx context.Context, dataset string, quotaBytes int64) error
	CreateSnapshot(ctx context.Context, dataset, name string) error
	Rollback(ctx context.Context, dataset, snapshot string) error
	DestroyVolume(ctx context.Context, dataset string, opts StorageDestroyOpts) error
}

// StorageVolumeOpts mirrors storage.VolumeOpts (subset).
type StorageVolumeOpts struct {
	QuotaBytes int64
	Mountpoint string
	Preset     string
}

// StorageDestroyOpts mirrors storage.DestroyOpts (subset).
type StorageDestroyOpts struct {
	Recursive bool
	Force     bool
}

// CreateAppDataset creates a child dataset under the chosen pool. The
// dataset path is what the planner produced (pool/apps/<app>/<dataset>).
func (a *StorageAdapter) CreateAppDataset(ctx context.Context, dataset string, opts AppDatasetOpts) error {
	if a == nil || a.Engine == nil {
		return nil
	}
	return a.Engine.CreateVolume(ctx, dataset, StorageVolumeOpts{
		QuotaBytes: opts.QuotaBytes,
		Mountpoint: opts.Mountpoint,
	})
}

// DestroyAppDataset is invoked on rollback or uninstall+deleteData.
func (a *StorageAdapter) DestroyAppDataset(ctx context.Context, dataset string) error {
	if a == nil || a.Engine == nil {
		return nil
	}
	return a.Engine.DestroyVolume(ctx, dataset, StorageDestroyOpts{Recursive: true, Force: true})
}

// SnapshotAppDataset is used by Update. snapshot name is the suffix after '@'.
func (a *StorageAdapter) SnapshotAppDataset(ctx context.Context, dataset, snapshot string) error {
	if a == nil || a.Engine == nil {
		return nil
	}
	return a.Engine.CreateSnapshot(ctx, dataset, snapshot)
}

// RollbackAppDataset is used by Update on healthcheck failure.
func (a *StorageAdapter) RollbackAppDataset(ctx context.Context, dataset, snapshot string) error {
	if a == nil || a.Engine == nil {
		return nil
	}
	return a.Engine.Rollback(ctx, dataset, snapshot)
}

// DatasetMountpoint computes the host path for dataset. ZFS default is
// /<dataset> so the planner's pool/apps/<app>/<dataset> path becomes
// /pool/apps/<app>/<dataset>. cmd/kura overrides this when ZFS reports a
// non-default mountpoint via `zfs get -H -o value mountpoint`.
func (a *StorageAdapter) DatasetMountpoint(_ context.Context, dataset string) (string, error) {
	if dataset == "" {
		return "", fmt.Errorf("dataset is empty")
	}
	if !strings.HasPrefix(dataset, "/") {
		return "/" + dataset, nil
	}
	return filepath.Clean(dataset), nil
}

// FakeStorageWriter is the test/dev DatasetWriter — it records calls and
// returns synthesised mountpoints under the configured Root (default /tmp).
type FakeStorageWriter struct {
	Root      string
	Created   []string
	Destroyed []string
	Snaps     map[string][]string
	// FailCreate, when set, makes CreateAppDataset return an error for the
	// listed dataset paths. Used to test install rollback.
	FailCreate map[string]bool
}

// NewFakeStorageWriter returns a writer rooted at root.
func NewFakeStorageWriter(root string) *FakeStorageWriter {
	if root == "" {
		root = "/tmp/kura-test-data"
	}
	return &FakeStorageWriter{
		Root:       root,
		Snaps:      map[string][]string{},
		FailCreate: map[string]bool{},
	}
}

// CreateAppDataset records the dataset (and creates the directory on disk so
// real bind mounts can be exercised).
func (f *FakeStorageWriter) CreateAppDataset(_ context.Context, dataset string, _ AppDatasetOpts) error {
	if f.FailCreate[dataset] {
		return fmt.Errorf("fake: create %s: simulated failure", dataset)
	}
	f.Created = append(f.Created, dataset)
	return nil
}

// DestroyAppDataset records the call.
func (f *FakeStorageWriter) DestroyAppDataset(_ context.Context, dataset string) error {
	f.Destroyed = append(f.Destroyed, dataset)
	return nil
}

// SnapshotAppDataset records the call.
func (f *FakeStorageWriter) SnapshotAppDataset(_ context.Context, dataset, snap string) error {
	f.Snaps[dataset] = append(f.Snaps[dataset], snap)
	return nil
}

// RollbackAppDataset records the call.
func (f *FakeStorageWriter) RollbackAppDataset(_ context.Context, dataset, snap string) error {
	f.Snaps[dataset] = append(f.Snaps[dataset], "rollback:"+snap)
	return nil
}

// DatasetMountpoint returns Root + "/" + dataset (with '/' replaced).
func (f *FakeStorageWriter) DatasetMountpoint(_ context.Context, dataset string) (string, error) {
	return filepath.Join(f.Root, strings.ReplaceAll(dataset, "/", "_")), nil
}

var _ DatasetWriter = (*FakeStorageWriter)(nil)
var _ DatasetWriter = (*StorageAdapter)(nil)
