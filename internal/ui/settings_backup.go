package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kuraos-org/kura/engine/backup"
	"github.com/kuraos-org/kura/i18n"
	"github.com/kuraos-org/kura/internal/cmdexec"
)

// BackupDeps wires the backup store + upgrade executor into the Settings page.
type BackupDeps struct {
	Store       *backup.Store
	Exec        cmdexec.Executor
	AppStopper  backup.AppStopper
	ZFSSnapper  backup.ZFSSnapshotter
	RootDataset string // e.g. "tank/rootfs"
}

// SetBackupHandler installs the backup settings handler. Must be called before Routes().
func (r *Renderer) SetBackupHandler(deps BackupDeps) {
	r.backupHandler = &backupSettingsHandler{r: r, deps: deps}
}

type backupSettingsHandler struct {
	r    *Renderer
	deps BackupDeps
}

// scheduleView is the template data for one backup schedule row.
type scheduleView struct {
	ID         string
	Name       string
	CronExpr   string
	Datasets   []string
	RetHourly  int
	RetDaily   int
	RetMonthly int
}

// backendView is the template data for one offsite backend row.
type backendView struct {
	ID              string
	Name            string
	Kind            string
	CredentialState string
}

// scheduleFormData is template data for the add/edit schedule form.
type scheduleFormData struct {
	IsNew        bool
	ID           string
	Name         string
	CronExpr     string
	DatasetsText string // newline-separated
	RetHourly    int
	RetDaily     int
	RetMonthly   int
	ErrorMsg     string
}

// backendFormData is template data for the add/edit backend form.
type backendFormData struct {
	IsNew    bool
	ID       string
	Name     string
	Kind     string
	Host     string
	Repo     string
	Remote   string
	ErrorMsg string
	Kinds    []string
}

var allBackendKinds = []string{"zfs_send", "restic", "rclone"}

// ServeHTTP dispatches backup-settings sub-routes.
func (h *backupSettingsHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path
	switch {
	// Schedule CRUD
	case path == "/ui/admin/settings/backup/schedules/add-form" && req.Method == http.MethodGet:
		h.handleScheduleAddForm(w, req)
	case path == "/ui/admin/settings/backup/schedules/dismiss-form" && req.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
	case path == "/ui/admin/settings/backup/schedules/create" && req.Method == http.MethodPost:
		h.handleScheduleCreate(w, req)
	case strings.Contains(path, "/backup/schedules/") && strings.HasSuffix(path, "/edit-form") && req.Method == http.MethodGet:
		h.handleScheduleEditForm(w, req)
	case strings.Contains(path, "/backup/schedules/") && strings.HasSuffix(path, "/update") && req.Method == http.MethodPost:
		h.handleScheduleUpdate(w, req)
	case strings.Contains(path, "/backup/schedules/") && strings.HasSuffix(path, "/delete") && req.Method == http.MethodPost:
		h.handleScheduleDelete(w, req)

	// Backend CRUD
	case path == "/ui/admin/settings/backup/backends/add-form" && req.Method == http.MethodGet:
		h.handleBackendAddForm(w, req)
	case path == "/ui/admin/settings/backup/backends/dismiss-form" && req.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
	case path == "/ui/admin/settings/backup/backends/create" && req.Method == http.MethodPost:
		h.handleBackendCreate(w, req)
	case strings.Contains(path, "/backup/backends/") && strings.HasSuffix(path, "/edit-form") && req.Method == http.MethodGet:
		h.handleBackendEditForm(w, req)
	case strings.Contains(path, "/backup/backends/") && strings.HasSuffix(path, "/update") && req.Method == http.MethodPost:
		h.handleBackendUpdate(w, req)
	case strings.Contains(path, "/backup/backends/") && strings.HasSuffix(path, "/delete") && req.Method == http.MethodPost:
		h.handleBackendDelete(w, req)

	// System upgrade
	case path == "/ui/admin/settings/upgrade/run" && req.Method == http.MethodPost:
		h.handleUpgradeRun(w, req)

	default:
		http.NotFound(w, req)
	}
}

// ── Schedule handlers ────────────────────────────────────────────────────────

func (h *backupSettingsHandler) handleScheduleAddForm(w http.ResponseWriter, req *http.Request) {
	fd := &scheduleFormData{IsNew: true}
	h.renderScheduleForm(w, req, fd)
}

func (h *backupSettingsHandler) handleScheduleEditForm(w http.ResponseWriter, req *http.Request) {
	id := extractBackupID(req.URL.Path)
	row, err := h.deps.Store.GetSchedule(req.Context(), id)
	if err != nil {
		http.Error(w, "schedule not found", http.StatusNotFound)
		return
	}
	fd := &scheduleFormData{
		IsNew:        false,
		ID:           id,
		Name:         row.Name,
		CronExpr:     row.CronExpr,
		DatasetsText: strings.Join(row.Datasets, "\n"),
		RetHourly:    row.RetHourly,
		RetDaily:     row.RetDaily,
		RetMonthly:   row.RetMonthly,
	}
	h.renderScheduleForm(w, req, fd)
}

func (h *backupSettingsHandler) handleScheduleCreate(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	row := scheduleRowFromForm(req)
	row.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	if err := h.deps.Store.UpsertSchedule(req.Context(), row); err != nil {
		fd := scheduleFormDataFromForm(req, true, "")
		fd.ErrorMsg = h.r.tr.T(i18n.MsgSettingsBackupErrSave, err.Error())
		h.renderScheduleForm(w, req, fd)
		return
	}
	h.renderSchedulesCard(w, req)
}

func (h *backupSettingsHandler) handleScheduleUpdate(w http.ResponseWriter, req *http.Request) {
	id := extractBackupIDFromSuffix(req.URL.Path, "/update")
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	row := scheduleRowFromForm(req)
	row.ID = id
	if err := h.deps.Store.UpsertSchedule(req.Context(), row); err != nil {
		fd := scheduleFormDataFromForm(req, false, id)
		fd.ErrorMsg = h.r.tr.T(i18n.MsgSettingsBackupErrSave, err.Error())
		h.renderScheduleForm(w, req, fd)
		return
	}
	h.renderSchedulesCard(w, req)
}

func (h *backupSettingsHandler) handleScheduleDelete(w http.ResponseWriter, req *http.Request) {
	id := extractBackupIDFromSuffix(req.URL.Path, "/delete")
	if err := h.deps.Store.DeleteSchedule(req.Context(), id); err != nil {
		http.Error(w, h.r.tr.T(i18n.MsgSettingsBackendErrDelete, err.Error()), http.StatusInternalServerError)
		return
	}
	h.renderSchedulesCard(w, req)
}

func (h *backupSettingsHandler) renderSchedulesCard(w http.ResponseWriter, req *http.Request) {
	rows, _ := h.deps.Store.ListSchedules(req.Context())
	var views []scheduleView
	for _, r := range rows {
		views = append(views, scheduleView{
			ID: r.ID, Name: r.Name, CronExpr: r.CronExpr, Datasets: r.Datasets,
			RetHourly: r.RetHourly, RetDaily: r.RetDaily, RetMonthly: r.RetMonthly,
		})
	}
	extra := h.buildBackupExtra(req)
	extra.Schedules = views
	data := h.r.buildPageData("settings", i18n.MsgNavSettings)
	data.Extra = extra
	h.r.render(w, "templates/pages/settings.tmpl", data)
}

func (h *backupSettingsHandler) renderScheduleForm(w http.ResponseWriter, req *http.Request, fd *scheduleFormData) {
	body, err := h.r.renderPartialToBuffer("backup-schedule-form", fd)
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ── Backend handlers ─────────────────────────────────────────────────────────

func (h *backupSettingsHandler) handleBackendAddForm(w http.ResponseWriter, req *http.Request) {
	fd := &backendFormData{IsNew: true, Kind: "zfs_send", Kinds: allBackendKinds}
	h.renderBackendForm(w, req, fd)
}

func (h *backupSettingsHandler) handleBackendEditForm(w http.ResponseWriter, req *http.Request) {
	id := extractBackupID(req.URL.Path)
	row, err := h.deps.Store.GetBackend(req.Context(), id)
	if err != nil {
		http.Error(w, "backend not found", http.StatusNotFound)
		return
	}
	cfg := row.ConfigAsMap()
	fd := &backendFormData{
		IsNew:  false,
		ID:     id,
		Name:   row.Name,
		Kind:   row.Kind,
		Kinds:  allBackendKinds,
		Host:   stringFromMap(cfg, "host"),
		Repo:   stringFromMap(cfg, "repo"),
		Remote: stringFromMap(cfg, "remote"),
	}
	h.renderBackendForm(w, req, fd)
}

func (h *backupSettingsHandler) handleBackendCreate(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	row := backendRowFromForm(req)
	row.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	if err := h.deps.Store.UpsertBackend(req.Context(), row); err != nil {
		fd := backendFormDataFromForm(req, true, "")
		fd.ErrorMsg = h.r.tr.T(i18n.MsgSettingsBackendErrSave, err.Error())
		h.renderBackendForm(w, req, fd)
		return
	}
	h.renderBackendsCard(w, req)
}

func (h *backupSettingsHandler) handleBackendUpdate(w http.ResponseWriter, req *http.Request) {
	id := extractBackupIDFromSuffix(req.URL.Path, "/update")
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	row := backendRowFromForm(req)
	row.ID = id
	// If no token was provided, preserve existing credential state.
	if req.FormValue("token") == "" {
		if existing, err := h.deps.Store.GetBackend(req.Context(), id); err == nil {
			row.CredentialState = existing.CredentialState
		}
	}
	if err := h.deps.Store.UpsertBackend(req.Context(), row); err != nil {
		fd := backendFormDataFromForm(req, false, id)
		fd.ErrorMsg = h.r.tr.T(i18n.MsgSettingsBackendErrSave, err.Error())
		h.renderBackendForm(w, req, fd)
		return
	}
	h.renderBackendsCard(w, req)
}

func (h *backupSettingsHandler) handleBackendDelete(w http.ResponseWriter, req *http.Request) {
	id := extractBackupIDFromSuffix(req.URL.Path, "/delete")
	if err := h.deps.Store.DeleteBackend(req.Context(), id); err != nil {
		http.Error(w, h.r.tr.T(i18n.MsgSettingsBackendErrDelete, err.Error()), http.StatusInternalServerError)
		return
	}
	h.renderBackendsCard(w, req)
}

func (h *backupSettingsHandler) renderBackendsCard(w http.ResponseWriter, req *http.Request) {
	rows, _ := h.deps.Store.ListBackends(req.Context())
	var views []backendView
	for _, r := range rows {
		views = append(views, backendView{
			ID: r.ID, Name: r.Name, Kind: r.Kind, CredentialState: r.CredentialState,
		})
	}
	extra := h.buildBackupExtra(req)
	extra.Backends = views
	data := h.r.buildPageData("settings", i18n.MsgNavSettings)
	data.Extra = extra
	h.r.render(w, "templates/pages/settings.tmpl", data)
}

func (h *backupSettingsHandler) renderBackendForm(w http.ResponseWriter, req *http.Request, fd *backendFormData) {
	body, err := h.r.renderPartialToBuffer("backup-backend-form", fd)
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ── Upgrade handler ──────────────────────────────────────────────────────────

func (h *backupSettingsHandler) handleUpgradeRun(w http.ResponseWriter, req *http.Request) {
	dryRun := os.Getenv("KURA_UPGRADE_DRY_RUN") == "1"
	rootDS := h.deps.RootDataset
	if rootDS == "" {
		rootDS = "tank/rootfs"
	}

	cfg := backup.UpgradeConfig{
		RootDataset: rootDS,
		DryRun:      dryRun,
	}

	// Derive app datasets: any datasets named with backup:true in the schedule store.
	// For v1 we skip auto-detection; operators configure explicit datasets in schedules.
	// (Explicit > implicit — DESIGN_PRINCIPLES priority #8.)

	res, err := backup.RunUpgrade(req.Context(), h.deps.Exec, h.deps.AppStopper, h.deps.ZFSSnapper, cfg)
	if err != nil {
		msg := h.r.tr.T(i18n.MsgSettingsUpgradeErrSnapshot, err.Error())
		fmt.Fprintf(w, `<span style="color:var(--crit)">%s</span>`, msg)
		return
	}
	snapList := strings.Join(res.Snapshots, ", ")
	fmt.Fprintf(w, `<span style="color:var(--ok)">%s</span> <span style="font-size:11px;color:var(--text-3)">%s</span>`,
		h.r.tr.T(i18n.MsgSettingsUpgradeDone), snapList)
}

// ── Shared extra builder ─────────────────────────────────────────────────────

// backupExtra is passed as PageData.Extra for the backup tab.
type backupExtra struct {
	ActiveTab  string
	Schedules  []scheduleView
	Backends   []backendView
	// Notify tab fields (when rendering the full settings page)
	Channels       []channelView
	Events         []settingsEventView
	SeverityFilter string
	ErrorMsg       string
}

func (h *backupSettingsHandler) buildBackupExtra(req *http.Request) *backupExtra {
	extra := &backupExtra{ActiveTab: "backup"}

	if h.deps.Store != nil {
		schedRows, _ := h.deps.Store.ListSchedules(req.Context())
		for _, r := range schedRows {
			extra.Schedules = append(extra.Schedules, scheduleView{
				ID: r.ID, Name: r.Name, CronExpr: r.CronExpr, Datasets: r.Datasets,
				RetHourly: r.RetHourly, RetDaily: r.RetDaily, RetMonthly: r.RetMonthly,
			})
		}
		beRows, _ := h.deps.Store.ListBackends(req.Context())
		for _, r := range beRows {
			extra.Backends = append(extra.Backends, backendView{
				ID: r.ID, Name: r.Name, Kind: r.Kind, CredentialState: r.CredentialState,
			})
		}
	}
	return extra
}

// ── Form parsers ─────────────────────────────────────────────────────────────

func scheduleRowFromForm(req *http.Request) backup.ScheduleRow {
	var datasets []string
	for _, ds := range strings.Split(req.FormValue("datasets"), "\n") {
		ds = strings.TrimSpace(ds)
		if ds != "" {
			datasets = append(datasets, ds)
		}
	}
	return backup.ScheduleRow{
		Name:       strings.TrimSpace(req.FormValue("name")),
		CronExpr:   strings.TrimSpace(req.FormValue("cron_expr")),
		Datasets:   datasets,
		RetHourly:  parseIntForm(req, "ret_hourly"),
		RetDaily:   parseIntForm(req, "ret_daily"),
		RetMonthly: parseIntForm(req, "ret_monthly"),
	}
}

func scheduleFormDataFromForm(req *http.Request, isNew bool, id string) *scheduleFormData {
	return &scheduleFormData{
		IsNew: isNew, ID: id,
		Name:         req.FormValue("name"),
		CronExpr:     req.FormValue("cron_expr"),
		DatasetsText: req.FormValue("datasets"),
		RetHourly:    parseIntForm(req, "ret_hourly"),
		RetDaily:     parseIntForm(req, "ret_daily"),
		RetMonthly:   parseIntForm(req, "ret_monthly"),
	}
}

func backendRowFromForm(req *http.Request) backup.BackendRow {
	kind := req.FormValue("kind")
	token := req.FormValue("token")
	credState := "unset"
	if token != "" {
		credState = "set"
	}
	cfg := buildBackendConfig(kind, req)
	cfgBytes, _ := jsonMarshalCompact(cfg)
	return backup.BackendRow{
		Name:            strings.TrimSpace(req.FormValue("name")),
		Kind:            kind,
		CredentialState: credState,
		ConfigJSON:      string(cfgBytes),
	}
}

func backendFormDataFromForm(req *http.Request, isNew bool, id string) *backendFormData {
	return &backendFormData{
		IsNew: isNew, ID: id,
		Name: req.FormValue("name"), Kind: req.FormValue("kind"),
		Host: req.FormValue("host"), Repo: req.FormValue("repo"),
		Remote: req.FormValue("remote"), Kinds: allBackendKinds,
	}
}

func buildBackendConfig(kind string, req *http.Request) map[string]any {
	cfg := map[string]any{}
	switch kind {
	case "zfs_send":
		if h := strings.TrimSpace(req.FormValue("host")); h != "" {
			cfg["host"] = h
		}
	case "restic":
		if r := strings.TrimSpace(req.FormValue("repo")); r != "" {
			cfg["repo"] = r
		}
	case "rclone":
		if r := strings.TrimSpace(req.FormValue("remote")); r != "" {
			cfg["remote"] = r
		}
	}
	return cfg
}

// ── Path helpers ─────────────────────────────────────────────────────────────

func extractBackupID(path string) string {
	// /ui/admin/settings/backup/{section}/{id}/...
	parts := strings.Split(path, "/")
	// parts: ["", "ui", "admin", "settings", "backup", section, id, action]
	if len(parts) >= 8 {
		return parts[7]
	}
	return ""
}

func extractBackupIDFromSuffix(path, suffix string) string {
	// Strip the suffix then extract the last component before it.
	trimmed := strings.TrimSuffix(path, suffix)
	parts := strings.Split(trimmed, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// ── Misc helpers ─────────────────────────────────────────────────────────────

func parseIntForm(req *http.Request, key string) int {
	v := req.FormValue(key)
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

func stringFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func jsonMarshalCompact(v any) ([]byte, error) {
	return json.Marshal(v)
}
