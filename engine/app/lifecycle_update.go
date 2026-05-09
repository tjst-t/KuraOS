package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UpdateRequest changes an existing install to a new manifest version.
type UpdateRequest struct {
	AppID      string
	Source     RegistrySource
	NewVersion string
}

// Update flow (design.md §7.5):
//  1. snapshot every backup:true dataset (snap name = pre-update-<ts>)
//  2. fetch + verify new manifest
//  3. pull new images
//  4. stop old containers
//  5. recreate with new images (preserve container names)
//  6. healthcheck
//  7. on healthcheck failure: rollback (stop new, restart old image, restore
//     dataset snapshot for backup:true datasets)
//  8. update app_installs row + persist new compose transcript
func (l *AppLifecycle) Update(ctx context.Context, req UpdateRequest) error {
	rec, err := l.LookupApp(ctx, req.AppID)
	if err != nil {
		return err
	}
	if rec.AppID == "" {
		return fmt.Errorf("%w: %s", ErrAppNotInstalled, req.AppID)
	}
	l.emit(ProgressEvent{AppID: req.AppID, Stage: StageStart, OK: true,
		Detail: rec.Name + " " + rec.Version + " -> " + req.NewVersion})

	if err := l.Docker.Ping(ctx); err != nil {
		l.emit(ProgressEvent{AppID: req.AppID, Stage: StageError, OK: false, Final: true,
			Detail: "docker daemon unreachable: " + err.Error()})
		return fmt.Errorf("%w: %v", ErrDockerUnavailable, err)
	}

	newManifest, _, err := l.Registry.FetchManifest(ctx, req.Source, rec.Name, req.NewVersion)
	if err != nil {
		l.emit(ProgressEvent{AppID: req.AppID, Stage: StageError, OK: false, Final: true, Detail: err.Error()})
		return err
	}

	// 1. Snapshot backup:true datasets — failing here aborts before any
	// destructive change (DESIGN_PRINCIPLES priority #5: pre-upgrade snapshot
	// failure cancels the upgrade).
	snapTag := "pre-update-" + l.now().UTC().Format("20060102T150405Z")
	plans, err := l.Planner.Plan(ctx, req.AppID, newManifest)
	if err != nil {
		return l.failUpdate(req.AppID, "plan datasets", err)
	}
	snapshots := []DatasetPlan{}
	for _, plan := range plans {
		if !plan.Backup {
			continue
		}
		l.emit(ProgressEvent{AppID: req.AppID, Stage: StageSnapshot, OK: true,
			Detail: "snapshot " + plan.DatasetPath + "@" + snapTag})
		if l.Storage != nil {
			if err := l.Storage.SnapshotAppDataset(ctx, plan.DatasetPath, snapTag); err != nil {
				return l.failUpdate(req.AppID, "snapshot "+plan.DatasetPath, err)
			}
		}
		snapshots = append(snapshots, plan)
	}

	// 2. Pull new images.
	l.emit(ProgressEvent{AppID: req.AppID, Stage: StagePullUpdate, OK: true, Detail: "pulling new images"})
	for _, cname := range containerNames(newManifest.Containers) {
		c := newManifest.Containers[cname]
		if err := l.Docker.PullImage(ctx, c.Image); err != nil {
			return l.failUpdate(req.AppID, "pull "+c.Image, err)
		}
	}

	// 3. Stop old containers but DO NOT remove yet — we need them for rollback.
	oldList, err := l.Docker.ListContainersByLabel(ctx, map[string]string{"kuraos.app_id": req.AppID})
	if err != nil {
		return l.failUpdate(req.AppID, "list old containers", err)
	}
	oldImages := map[string]string{}
	for _, oc := range oldList {
		oldImages[oc.Labels["kuraos.container"]] = oc.Image
		l.emit(ProgressEvent{AppID: req.AppID, Stage: StageStop, OK: true, Detail: "stopping " + oc.Name})
		if err := l.Docker.StopContainer(ctx, oc.Name, 10*time.Second); err != nil {
			return l.failUpdate(req.AppID, "stop "+oc.Name, err)
		}
	}

	// 4. Re-create containers with new images.
	in, _, err := l.recomposeInputs(ctx, req.AppID, newManifest, rec)
	if err != nil {
		return l.failUpdate(req.AppID, "recompose inputs", err)
	}
	proj, err := BuildCompose(newManifest, in)
	if err != nil {
		return l.failUpdate(req.AppID, "build compose", err)
	}
	transcriptPath, err := l.writeTranscript(req.AppID, mustMarshal(proj))
	if err != nil {
		return l.failUpdate(req.AppID, "write transcript", err)
	}
	_ = transcriptPath

	startedNew := []string{}
	createOrder, err := topoSort(newManifest.Containers)
	if err != nil {
		return l.failUpdate(req.AppID, "topo sort", err)
	}
	rollback := func(reason string) error {
		l.emit(ProgressEvent{AppID: req.AppID, Stage: StageRollback, OK: true, Detail: reason})
		// Stop / remove the new containers
		for _, name := range startedNew {
			_ = l.Docker.StopContainer(ctx, name, 5*time.Second)
			_ = l.Docker.RemoveContainer(ctx, name, true)
		}
		// Restore old images: remove the (already-stopped) old containers
		// and re-create them with their original images, then start them.
		for _, oc := range oldList {
			_ = l.Docker.RemoveContainer(ctx, oc.Name, true)
			cname := oc.Labels["kuraos.container"]
			if cname == "" {
				continue
			}
			spec := buildContainerSpec(newManifest, cname, in, proj.Services[cname])
			spec.Image = oldImages[cname]
			if _, err := l.Docker.CreateContainer(ctx, spec); err == nil {
				_ = l.Docker.StartContainer(ctx, spec.Name)
			}
		}
		// Restore snapshots for backup:true datasets.
		for _, plan := range snapshots {
			if l.Storage != nil {
				_ = l.Storage.RollbackAppDataset(ctx, plan.DatasetPath, snapTag)
			}
		}
		l.emit(ProgressEvent{AppID: req.AppID, Stage: StageDone, OK: false, Final: true, Detail: reason})
		return errors.New(reason)
	}

	for _, cname := range createOrder {
		oldDocker := oldList
		spec := buildContainerSpec(newManifest, cname, in, proj.Services[cname])
		// Remove the old (stopped) container so the name is free.
		for _, oc := range oldDocker {
			if oc.Labels["kuraos.container"] == cname {
				_ = l.Docker.RemoveContainer(ctx, oc.Name, true)
			}
		}
		if _, err := l.Docker.CreateContainer(ctx, spec); err != nil {
			return rollback("create " + cname + " failed: " + err.Error())
		}
		if err := l.Docker.StartContainer(ctx, spec.Name); err != nil {
			return rollback("start " + cname + " failed: " + err.Error())
		}
		startedNew = append(startedNew, spec.Name)
	}

	// 5. Healthcheck.
	target := healthCheckTarget(newManifest, createOrder)
	if target != "" {
		l.emit(ProgressEvent{AppID: req.AppID, Stage: StageHealthcheck, OK: true,
			Detail: "waiting for " + target})
		if err := l.waitHealthy(ctx, containerName(req.AppID, target)); err != nil {
			return rollback("healthcheck failed: " + err.Error())
		}
	}

	// 6. Persist new version.
	rec.Version = req.NewVersion
	rec.State = "running"
	if err := l.persistAppRecord(ctx, rec); err != nil {
		return l.failUpdate(req.AppID, "persist", err)
	}
	l.emit(ProgressEvent{AppID: req.AppID, Stage: StageDone, OK: true, Final: true,
		Detail: "updated to " + req.NewVersion})
	return nil
}

// failUpdate emits + returns. Used for early failures before rollback machinery
// is necessary (no new containers started yet).
func (l *AppLifecycle) failUpdate(appID, what string, cause error) error {
	wrapped := fmt.Errorf("%s: %w", what, cause)
	l.emit(ProgressEvent{AppID: appID, Stage: StageError, OK: false, Final: true, Detail: wrapped.Error()})
	return wrapped
}

// recomposeInputs rebuilds InstallInputs from the persisted record and the
// new manifest. Setup values come from the original install; secrets are
// re-fetched from the vault; ports are looked up (already reserved).
func (l *AppLifecycle) recomposeInputs(ctx context.Context, appID string, newManifest *Manifest, rec AppRecord) (InstallInputs, []DatasetPlan, error) {
	plans, err := l.Planner.Plan(ctx, appID, newManifest)
	if err != nil {
		return InstallInputs{}, nil, err
	}
	dpaths, err := l.resolveDatasetPaths(ctx, plans)
	if err != nil {
		return InstallInputs{}, nil, err
	}
	pmap := map[PortKey]int{}
	for _, cname := range containerNames(newManifest.Containers) {
		c := newManifest.Containers[cname]
		for _, raw := range c.Ports {
			cp, err := parseManifestPort(raw)
			if err != nil {
				return InstallInputs{}, nil, err
			}
			host, err := l.Ports.Reserve(ctx, PortReservation{
				AppID: appID, Container: cname, ManifestPort: cp,
			})
			if err != nil {
				return InstallInputs{}, nil, err
			}
			pmap[PortKey{Container: cname, ManifestPort: cp}] = host
		}
	}
	secrets := map[string]string{}
	for _, s := range newManifest.Settings {
		if s.Type == SettingSecret {
			val, err := l.Secrets.EnsureSecret(ctx, appID, s.Key)
			if err != nil {
				return InstallInputs{}, nil, err
			}
			secrets[s.Key] = val
			continue
		}
		if v, ok := rec.Settings[s.Key]; ok && v != "" {
			secrets[s.Key] = v
		} else if s.Default != "" {
			secrets[s.Key] = s.Default
		}
	}
	sharePaths := map[string]string{}
	for _, sf := range newManifest.Setup.Required {
		if sf.Type == SetupSharePicker {
			val := rec.Setup[sf.Key]
			if val == "" {
				continue
			}
			if len(newManifest.Shares) == 1 {
				sharePaths[newManifest.Shares[0].Name] = val
			} else {
				sharePaths[sf.Key] = val
			}
		}
	}
	in := InstallInputs{
		AppID:         appID,
		SharePaths:    sharePaths,
		DatasetPaths:  dpaths,
		PortBindings:  pmap,
		Secrets:       secrets,
		ConfigOutputs: map[string]string{},
		NetworkName:   "kura-" + newManifest.Name,
	}
	return in, plans, nil
}

// UninstallRequest controls how aggressively uninstall removes state.
type UninstallRequest struct {
	AppID      string
	DeleteData bool // when true, also destroys app-private datasets
}

// Uninstall stops + removes containers, unregisters the gateway route, and
// deletes the app_installs row. dataset removal is gated on DeleteData=true
// (default false — re-install restores).
func (l *AppLifecycle) Uninstall(ctx context.Context, req UninstallRequest) error {
	rec, err := l.LookupApp(ctx, req.AppID)
	if err != nil {
		return err
	}
	if rec.AppID == "" {
		return fmt.Errorf("%w: %s", ErrAppNotInstalled, req.AppID)
	}
	l.emit(ProgressEvent{AppID: req.AppID, Stage: StageStart, OK: true, Detail: "uninstalling " + rec.Name})

	if err := l.Docker.Ping(ctx); err != nil {
		// Tolerate docker absence on uninstall — operator may be removing a
		// stranded record. Log + continue to free SQLite + Gateway state.
		l.emit(ProgressEvent{AppID: req.AppID, Stage: StageError, OK: false,
			Detail: "docker unreachable; continuing with metadata cleanup: " + err.Error()})
	} else {
		list, err := l.Docker.ListContainersByLabel(ctx, map[string]string{"kuraos.app_id": req.AppID})
		if err == nil {
			for _, c := range list {
				_ = l.Docker.StopContainer(ctx, c.Name, 10*time.Second)
				if err := l.Docker.RemoveContainer(ctx, c.Name, true); err != nil {
					l.emit(ProgressEvent{AppID: req.AppID, Stage: StageRemove, OK: false,
						Detail: "remove " + c.Name + ": " + err.Error()})
				}
			}
		}
	}

	if l.Routes != nil {
		_ = l.Routes.UnregisterAppRoute(req.AppID)
	}

	// Free port reservations
	if _, err := l.DB.ExecContext(ctx,
		`DELETE FROM app_port_reservations WHERE app_id = ?`, req.AppID); err != nil {
		return fmt.Errorf("free ports: %w", err)
	}

	if req.DeleteData {
		// Destroy datasets — destructive op gated on caller's explicit consent.
		rows, err := l.DB.QueryContext(ctx,
			`SELECT dataset_path FROM app_dataset_plan WHERE app_id = ?`, req.AppID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var path string
				if err := rows.Scan(&path); err == nil {
					if l.Storage != nil {
						_ = l.Storage.DestroyAppDataset(ctx, path)
					}
				}
			}
		}
		_, _ = l.DB.ExecContext(ctx, `DELETE FROM app_dataset_plan WHERE app_id = ?`, req.AppID)
	}

	if _, err := l.DB.ExecContext(ctx, `DELETE FROM app_installs WHERE app_id = ?`, req.AppID); err != nil {
		return fmt.Errorf("delete record: %w", err)
	}

	l.emit(ProgressEvent{AppID: req.AppID, Stage: StageDone, OK: true, Final: true,
		Detail: rec.Name + " uninstalled"})
	return nil
}

// mustMarshal is a panic-safe wrapper for compose marshalling used by Update.
// On failure it returns a stub YAML so the install continues — the transcript
// is best-effort observability, not the source of truth.
func mustMarshal(p *ComposeProject) []byte {
	b, err := MarshalCompose(p)
	if err != nil {
		return []byte("# compose marshal failed: " + err.Error() + "\n")
	}
	return b
}

// strFold is a tiny helper used by the route comparison. Kept here so the
// generic strings package import isn't needed in lifecycle.go's tight loop.
func strFold(a, b string) bool { return strings.EqualFold(a, b) }
