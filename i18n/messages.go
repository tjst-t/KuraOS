package i18n

type MessageID string

// System-level messages (added in S0ff37f).
const (
	MsgSystemStartup  MessageID = "system.startup"
	MsgSystemHealthy  MessageID = "system.healthy"
	MsgSystemShutdown MessageID = "system.shutdown"
)

// Brand and shell chrome (added in S464e47).
const (
	MsgBrandName    MessageID = "brand.name"
	MsgBrandTagline MessageID = "brand.tagline"
)

// Sidebar navigation labels.
const (
	MsgNavSectionAdmin    MessageID = "nav.section.admin"
	MsgNavSectionOverview MessageID = "nav.section.overview"

	MsgNavDashboard MessageID = "nav.dashboard"
	MsgNavStorage   MessageID = "nav.storage"
	MsgNavShares    MessageID = "nav.shares"
	MsgNavUsers     MessageID = "nav.users"
	MsgNavNetwork   MessageID = "nav.network"
	MsgNavApps      MessageID = "nav.apps"
	MsgNavSettings  MessageID = "nav.settings"
)

// Header / shell controls.
const (
	MsgHeaderToggleSidebar  MessageID = "header.toggle_sidebar"
	MsgHeaderToggleTheme    MessageID = "header.toggle_theme"
	MsgHeaderNotifications  MessageID = "header.notifications"
	MsgHeaderSearch         MessageID = "header.search"
	MsgHeaderSearchKeyboard MessageID = "header.search.keyboard"
)

// Roles.
const (
	MsgRoleAdmin MessageID = "role.admin"
	MsgRoleUser  MessageID = "role.user"
)

// Page-specific titles and lead copy.
const (
	MsgDashboardTitle    MessageID = "page.dashboard.title"
	MsgDashboardSubtitle MessageID = "page.dashboard.subtitle"
	MsgDashboardEmpty    MessageID = "page.dashboard.empty"
)

// Common button labels.
const (
	MsgBtnRefresh MessageID = "btn.refresh"
	MsgBtnSave    MessageID = "btn.save"
	MsgBtnCancel  MessageID = "btn.cancel"
	MsgBtnApply   MessageID = "btn.apply"
)

// config.json import/export errors and notices.
const (
	MsgConfigExportEmptyState MessageID = "config.export.empty_state"
	MsgConfigInvalidJSON      MessageID = "config.invalid_json"
	MsgConfigDiffNoChanges    MessageID = "config.diff.no_changes"
)

// Login page (added in S1e7eeb).
const (
	MsgLoginTitle            MessageID = "page.login.title"
	MsgLoginSubtitle         MessageID = "page.login.subtitle"
	MsgLoginUsernameLabel    MessageID = "page.login.username"
	MsgLoginPasswordLabel    MessageID = "page.login.password"
	MsgLoginSubmit           MessageID = "page.login.submit"
	MsgLoginInvalidCreds     MessageID = "page.login.invalid_credentials"
	MsgLoginUsernameRequired MessageID = "page.login.username_required"
	MsgLoginPasswordRequired MessageID = "page.login.password_required"
	MsgLoginGenericError     MessageID = "page.login.generic_error"
	MsgLoginLogoutSuccess    MessageID = "page.login.logout_success"
)

// Storage page (added in Se3b190 — read-only views).
const (
	MsgStorageTitle           MessageID = "page.storage.title"
	MsgStorageSubtitle        MessageID = "page.storage.subtitle"
	MsgStorageEmpty           MessageID = "page.storage.empty"
	MsgStorageImportBanner    MessageID = "page.storage.import_banner"
	MsgStorageImportBannerOne MessageID = "page.storage.import_banner_one"
	MsgStorageImportInspect   MessageID = "page.storage.import_inspect"
	MsgStorageImportDeferred  MessageID = "page.storage.import_deferred"
	MsgStorageNewPool         MessageID = "page.storage.new_pool"
	MsgStorageNewPoolDeferred MessageID = "page.storage.new_pool_deferred"
	MsgStoragePoolsHeading    MessageID = "page.storage.pools_heading"
	MsgStorageDisksHeading    MessageID = "page.storage.disks_heading"
	MsgStorageImportHeading   MessageID = "page.storage.import_heading"

	MsgStorageVolumesHeading      MessageID = "page.storage.volumes.heading"
	MsgStorageVolumesEmpty        MessageID = "page.storage.volumes.empty"
	MsgStorageVolumeNew           MessageID = "page.storage.volume.new"
	MsgStorageVolumeColName       MessageID = "page.storage.volume.col.name"
	MsgStorageVolumeColUsed       MessageID = "page.storage.volume.col.used"
	MsgStorageVolumeColAvail      MessageID = "page.storage.volume.col.avail"
	MsgStorageVolumeColQuota      MessageID = "page.storage.volume.col.quota"
	MsgStorageVolumeColPreset     MessageID = "page.storage.volume.col.preset"
	MsgStorageVolumeColMount      MessageID = "page.storage.volume.col.mount"
	MsgStorageVolumeColActions    MessageID = "page.storage.volume.col.actions"
	MsgStorageVolumeRowPoolRoot   MessageID = "page.storage.volume.pool_root"
	MsgStorageVolumeNewTitle      MessageID = "page.storage.volume.new.title"
	MsgStorageVolumeNewName       MessageID = "page.storage.volume.new.name"
	MsgStorageVolumeNewNameHint   MessageID = "page.storage.volume.new.name_hint"
	MsgStorageVolumeNewPool       MessageID = "page.storage.volume.new.pool"
	MsgStorageVolumeNewPoolHint   MessageID = "page.storage.volume.new.pool_hint"
	MsgStorageVolumeNewPath       MessageID = "page.storage.volume.new.path"
	MsgStorageVolumeNewPathHint   MessageID = "page.storage.volume.new.path_hint"
	MsgStorageVolumeNewPreset     MessageID = "page.storage.volume.new.preset"
	MsgStorageVolumeNewQuota      MessageID = "page.storage.volume.new.quota"
	MsgStorageVolumeNewQuotaHint  MessageID = "page.storage.volume.new.quota_hint"
	MsgStorageVolumeNewMountpoint MessageID = "page.storage.volume.new.mountpoint"
	MsgStorageVolumeNewSubmit     MessageID = "page.storage.volume.new.submit"
	MsgStorageVolumeQuotaSet      MessageID = "page.storage.volume.quota.set"
	MsgStorageVolumeQuotaUnset    MessageID = "page.storage.volume.quota.unset"

	MsgStorageSnapshotsHeading        MessageID = "page.storage.snapshots.heading"
	MsgStorageSnapshotsEmpty          MessageID = "page.storage.snapshots.empty"
	MsgStorageSnapshotNew             MessageID = "page.storage.snapshot.new"
	MsgStorageSnapshotColDataset      MessageID = "page.storage.snapshot.col.dataset"
	MsgStorageSnapshotColName         MessageID = "page.storage.snapshot.col.name"
	MsgStorageSnapshotColUsed         MessageID = "page.storage.snapshot.col.used"
	MsgStorageSnapshotColRefer        MessageID = "page.storage.snapshot.col.refer"
	MsgStorageSnapshotColCreated      MessageID = "page.storage.snapshot.col.created"
	MsgStorageSnapshotColActions      MessageID = "page.storage.snapshot.col.actions"
	MsgStorageSnapshotRollback        MessageID = "page.storage.snapshot.rollback"
	MsgStorageSnapshotNewTitle        MessageID = "page.storage.snapshot.new.title"
	MsgStorageSnapshotNewDataset      MessageID = "page.storage.snapshot.new.dataset"
	MsgStorageSnapshotNewName         MessageID = "page.storage.snapshot.new.name"
	MsgStorageSnapshotNewSubmit       MessageID = "page.storage.snapshot.new.submit"
	MsgStorageSnapshotRollbackTitle   MessageID = "page.storage.snapshot.rollback.title"
	MsgStorageSnapshotRollbackWarn    MessageID = "page.storage.snapshot.rollback.warn"
	MsgStorageSnapshotRollbackSubmit  MessageID = "page.storage.snapshot.rollback.submit"
	MsgStorageSnapshotRollbackConfirm MessageID = "page.storage.snapshot.rollback.confirm"

	MsgStorageDestroyPool        MessageID = "page.storage.destroy.pool"
	MsgStorageDestroyVolume      MessageID = "page.storage.destroy.volume"
	MsgStorageDestroySnapshot    MessageID = "page.storage.destroy.snapshot"
	MsgStorageDestroyPoolTitle   MessageID = "page.storage.destroy.pool.title"
	MsgStorageDestroyVolumeTitle MessageID = "page.storage.destroy.volume.title"
	MsgStorageDestroySnapTitle   MessageID = "page.storage.destroy.snapshot.title"
	MsgStorageDestroyWarn        MessageID = "page.storage.destroy.warn"
	MsgStorageDestroyTypeName    MessageID = "page.storage.destroy.type_name"
	MsgStorageDestroyForce       MessageID = "page.storage.destroy.force"
	MsgStorageDestroyForceWarn   MessageID = "page.storage.destroy.force_warn"
	MsgStorageDestroyRecursive   MessageID = "page.storage.destroy.recursive"
	MsgStorageDestroyRecurseWarn MessageID = "page.storage.destroy.recurse_warn"
	MsgStorageDestroySubmit      MessageID = "page.storage.destroy.submit"
	MsgStorageDestroySnapAck     MessageID = "page.storage.destroy.snapshot.ack"

	MsgStoragePoolHealthOnline    MessageID = "page.storage.pool.health.online"
	MsgStoragePoolHealthDegraded  MessageID = "page.storage.pool.health.degraded"
	MsgStoragePoolHealthFaulted   MessageID = "page.storage.pool.health.faulted"
	MsgStoragePoolHealthOffline   MessageID = "page.storage.pool.health.offline"
	MsgStoragePoolHealthUnavail   MessageID = "page.storage.pool.health.unavail"
	MsgStoragePoolHealthRemoved   MessageID = "page.storage.pool.health.removed"
	MsgStoragePoolHealthSuspended MessageID = "page.storage.pool.health.suspended"
	MsgStoragePoolHealthUnknown   MessageID = "page.storage.pool.health.unknown"

	MsgStorageVdevTopology MessageID = "page.storage.vdev.topology"
	MsgStorageVdevDataRow  MessageID = "page.storage.vdev.data_row"
	MsgStorageVdevSpecial  MessageID = "page.storage.vdev.special"
	MsgStorageVdevCache    MessageID = "page.storage.vdev.cache"
	MsgStorageVdevLog      MessageID = "page.storage.vdev.log"
	MsgStorageVdevSpare    MessageID = "page.storage.vdev.spare"

	MsgStoragePoolUsed       MessageID = "page.storage.pool.used"
	MsgStoragePoolFrag       MessageID = "page.storage.pool.frag"
	MsgStoragePoolDataIntact MessageID = "page.storage.pool.data_intact"

	MsgStorageDisksColDevice MessageID = "page.storage.disks.col.device"
	MsgStorageDisksColModel  MessageID = "page.storage.disks.col.model"
	MsgStorageDisksColPool   MessageID = "page.storage.disks.col.pool"
	MsgStorageDisksColSize   MessageID = "page.storage.disks.col.size"
	MsgStorageDisksColTemp   MessageID = "page.storage.disks.col.temp"
	MsgStorageDisksColHours  MessageID = "page.storage.disks.col.hours"
	MsgStorageDisksColSMART  MessageID = "page.storage.disks.col.smart"
	MsgStorageDisksColUsage  MessageID = "page.storage.disks.col.usage"

	MsgStorageDiskUsageFree    MessageID = "page.storage.disk.usage.free"
	MsgStorageDiskUsagePool    MessageID = "page.storage.disk.usage.pool"
	MsgStorageDiskUsageSpare   MessageID = "page.storage.disk.usage.spare"
	MsgStorageDiskUsageForeign MessageID = "page.storage.disk.usage.foreign"
	MsgStorageDiskUsageSystem  MessageID = "page.storage.disk.usage.system"

	MsgStorageSMARTPassed      MessageID = "page.storage.smart.passed"
	MsgStorageSMARTWarning     MessageID = "page.storage.smart.warning"
	MsgStorageSMARTFailed      MessageID = "page.storage.smart.failed"
	MsgStorageSMARTUnavailable MessageID = "page.storage.smart.unavailable"
	MsgStorageSMARTUnsupported MessageID = "page.storage.smart.unsupported"

	MsgStorageImportColName     MessageID = "page.storage.import.col.name"
	MsgStorageImportColGUID     MessageID = "page.storage.import.col.guid"
	MsgStorageImportColState    MessageID = "page.storage.import.col.state"
	MsgStorageImportColTopology MessageID = "page.storage.import.col.topology"

	MsgStorageError               MessageID = "page.storage.error"
	MsgStorageErrorZFSUnavailable MessageID = "page.storage.error.zfs_unavailable"
	MsgStorageErrorZFSPermission  MessageID = "page.storage.error.zfs_permission"
	MsgStorageErrorTimeout        MessageID = "page.storage.error.timeout"
	MsgStorageErrorUnknown        MessageID = "page.storage.error.unknown"
)

// Storage write-side (added in S9db742): Create Pool dialog, Volume / Snapshot
// tabs, presets, import flow, validation errors.
const (
	MsgStorageNewPoolDialogTitle MessageID = "page.storage.new_pool.title"
	MsgStorageNewPoolName        MessageID = "page.storage.new_pool.name"
	MsgStorageNewPoolNameHint    MessageID = "page.storage.new_pool.name_hint"
	MsgStorageNewPoolLayout      MessageID = "page.storage.new_pool.layout"
	MsgStorageNewPoolDisks       MessageID = "page.storage.new_pool.disks"
	MsgStorageNewPoolDisksHint   MessageID = "page.storage.new_pool.disks_hint"
	MsgStorageNewPoolDisksNone   MessageID = "page.storage.new_pool.disks_none"

	MsgStorageNewPoolErrNameRequired        MessageID = "page.storage.new_pool.err.name_required"
	MsgStorageNewPoolErrDataLayoutRequired  MessageID = "page.storage.new_pool.err.data_layout_required"
	MsgStorageNewPoolErrDataTooFew          MessageID = "page.storage.new_pool.err.data_too_few"
	MsgStorageNewPoolErrSpecialLayoutNeeded MessageID = "page.storage.new_pool.err.special_layout_required"
	MsgStorageNewPoolErrSpecialTooFew       MessageID = "page.storage.new_pool.err.special_too_few"
	MsgStorageNewPoolErrDuplicateDisk       MessageID = "page.storage.new_pool.err.duplicate_disk"
	MsgStorageNewPoolSpecial                MessageID = "page.storage.new_pool.special"
	MsgStorageNewPoolSpecialHint            MessageID = "page.storage.new_pool.special_hint"
	MsgStorageNewPoolSpares                 MessageID = "page.storage.new_pool.spares"
	MsgStorageNewPoolSubmit                 MessageID = "page.storage.new_pool.submit"
	MsgStorageNewPoolCancel                 MessageID = "page.storage.new_pool.cancel"
	MsgStorageNewPoolCreated                MessageID = "page.storage.new_pool.created"

	MsgStorageLayoutSingle MessageID = "page.storage.layout.single"
	MsgStorageLayoutMirror MessageID = "page.storage.layout.mirror"
	MsgStorageLayoutRaidZ1 MessageID = "page.storage.layout.raidz1"
	MsgStorageLayoutRaidZ2 MessageID = "page.storage.layout.raidz2"
	MsgStorageLayoutRaidZ3 MessageID = "page.storage.layout.raidz3"

	MsgStorageTabVolumes   MessageID = "page.storage.tab.volumes"
	MsgStorageTabSnapshots MessageID = "page.storage.tab.snapshots"
	MsgStorageTabDisks     MessageID = "page.storage.tab.disks"
	MsgStorageTabImports   MessageID = "page.storage.tab.imports"

	MsgStorageVolColPath      MessageID = "page.storage.vol.col.path"
	MsgStorageVolColPreset    MessageID = "page.storage.vol.col.preset"
	MsgStorageVolColUsed      MessageID = "page.storage.vol.col.used"
	MsgStorageVolColQuota     MessageID = "page.storage.vol.col.quota"
	MsgStorageVolColSnapshots MessageID = "page.storage.vol.col.snapshots"
	MsgStorageVolEmpty        MessageID = "page.storage.vol.empty"

	MsgStorageSnapColDataset MessageID = "page.storage.snap.col.dataset"
	MsgStorageSnapColName    MessageID = "page.storage.snap.col.name"
	MsgStorageSnapColCreated MessageID = "page.storage.snap.col.created"
	MsgStorageSnapColUsed    MessageID = "page.storage.snap.col.used"
	MsgStorageSnapEmpty      MessageID = "page.storage.snap.empty"

	MsgStoragePresetGeneral  MessageID = "page.storage.preset.general"
	MsgStoragePresetMedia    MessageID = "page.storage.preset.media"
	MsgStoragePresetDatabase MessageID = "page.storage.preset.database"

	MsgStorageImportConfirm   MessageID = "page.storage.import.confirm"
	MsgStorageImportForce     MessageID = "page.storage.import.force"
	MsgStorageImportForceWarn MessageID = "page.storage.import.force_warn"
	MsgStorageImportSuccess   MessageID = "page.storage.import.success"
	MsgStorageImportSubmit    MessageID = "page.storage.import.submit"

	// Validation errors (operator-visible). The wrapped sentinel errors stay
	// developer-side; UI maps them to these IDs.
	MsgStorageErrPoolNameInvalid           MessageID = "page.storage.err.pool_name_invalid"
	MsgStorageErrLayoutMinDisks            MessageID = "page.storage.err.layout_min_disks"
	MsgStorageErrSpecialNotRedundant       MessageID = "page.storage.err.special_not_redundant"
	MsgStorageErrDuplicateDisk             MessageID = "page.storage.err.duplicate_disk"
	MsgStorageErrSmallBlocksWithoutSpecial MessageID = "page.storage.err.small_blocks_without_special"
	MsgStorageErrPresetUnknown             MessageID = "page.storage.err.preset_unknown"
	MsgStorageErrCreateFailed              MessageID = "page.storage.err.create_failed"
	MsgStorageErrImportFailed              MessageID = "page.storage.err.import_failed"
)

// Shares page (added in Sd64f38).
const (
	MsgSharesTitle    MessageID = "page.shares.title"
	MsgSharesSubtitle MessageID = "page.shares.subtitle"
	MsgSharesEmpty    MessageID = "page.shares.empty"
	MsgSharesNew      MessageID = "page.shares.new"

	MsgSharesColName     MessageID = "page.shares.col.name"
	MsgSharesColProtocol MessageID = "page.shares.col.protocol"
	MsgSharesColPath     MessageID = "page.shares.col.path"
	MsgSharesColPreset   MessageID = "page.shares.col.preset"
	MsgSharesColAccess   MessageID = "page.shares.col.access"
	MsgSharesColStatus   MessageID = "page.shares.col.status"

	MsgSharesStatusActive   MessageID = "page.shares.status.active"
	MsgSharesStatusDisabled MessageID = "page.shares.status.disabled"
	MsgSharesStatusError    MessageID = "page.shares.status.error"

	MsgSharesProtocolSMB  MessageID = "page.shares.protocol.smb"
	MsgSharesProtocolNFS  MessageID = "page.shares.protocol.nfs"
	MsgSharesProtocolBoth MessageID = "page.shares.protocol.both"

	MsgSharesPresetGeneral     MessageID = "page.shares.preset.general"
	MsgSharesPresetMedia       MessageID = "page.shares.preset.media"
	MsgSharesPresetTimeMachine MessageID = "page.shares.preset.time_machine"
	MsgSharesPresetDatabase    MessageID = "page.shares.preset.database"

	MsgSharesPresetGeneralHint     MessageID = "page.shares.preset.general.hint"
	MsgSharesPresetMediaHint       MessageID = "page.shares.preset.media.hint"
	MsgSharesPresetTimeMachineHint MessageID = "page.shares.preset.time_machine.hint"
	MsgSharesPresetDatabaseHint    MessageID = "page.shares.preset.database.hint"

	MsgSharesAccessReadOnly  MessageID = "page.shares.access.read_only"
	MsgSharesAccessReadWrite MessageID = "page.shares.access.read_write"

	MsgSharesFormName        MessageID = "page.shares.form.name"
	MsgSharesFormNameHint    MessageID = "page.shares.form.name_hint"
	MsgSharesFormPath        MessageID = "page.shares.form.path"
	MsgSharesFormPathHint    MessageID = "page.shares.form.path_hint"
	MsgSharesFormProtocol    MessageID = "page.shares.form.protocol"
	MsgSharesFormPreset      MessageID = "page.shares.form.preset"
	MsgSharesFormAccess      MessageID = "page.shares.form.access"
	MsgSharesFormACL         MessageID = "page.shares.form.acl"
	MsgSharesFormACLHint     MessageID = "page.shares.form.acl_hint"
	MsgSharesFormDescription MessageID = "page.shares.form.description"
	MsgSharesFormSubmit      MessageID = "page.shares.form.submit"
	MsgSharesFormCancel      MessageID = "page.shares.form.cancel"

	MsgSharesDefaultsHeading MessageID = "page.shares.defaults.heading"
	MsgSharesDefaultsLead    MessageID = "page.shares.defaults.lead"

	MsgSharesErrInvalidName       MessageID = "page.shares.err.invalid_name"
	MsgSharesErrInvalidPath       MessageID = "page.shares.err.invalid_path"
	MsgSharesErrInvalidProtocol   MessageID = "page.shares.err.invalid_protocol"
	MsgSharesErrInvalidPreset     MessageID = "page.shares.err.invalid_preset"
	MsgSharesErrInvalidAccess     MessageID = "page.shares.err.invalid_access"
	MsgSharesErrInvalidACL        MessageID = "page.shares.err.invalid_acl"
	MsgSharesErrNameTaken         MessageID = "page.shares.err.name_taken"
	MsgSharesErrPathConflict      MessageID = "page.shares.err.path_conflict"
	MsgSharesErrPathOutsideVolume MessageID = "page.shares.err.path_outside_volume"
	MsgSharesErrTestparmFailed    MessageID = "page.shares.err.testparm_failed"
	MsgSharesErrReloadFailed      MessageID = "page.shares.err.reload_failed"
	MsgSharesErrUnknownPrincipal  MessageID = "page.shares.err.unknown_principal"
	MsgSharesErrGeneric           MessageID = "page.shares.err.generic"
	MsgSharesErrCreateFailed      MessageID = "page.shares.err.create_failed"
)

// Apps page (added in S65b510).
const (
	MsgAppsTitle    MessageID = "page.apps.title"
	MsgAppsSubtitle MessageID = "page.apps.subtitle"

	MsgAppsTabInstalled MessageID = "page.apps.tab.installed"
	MsgAppsTabStore     MessageID = "page.apps.tab.store"
	MsgAppsCategoryAll  MessageID = "page.apps.category.all"

	MsgAppsBtnInstall   MessageID = "page.apps.btn.install"
	MsgAppsBtnUpdate    MessageID = "page.apps.btn.update"
	MsgAppsBtnUninstall MessageID = "page.apps.btn.uninstall"
	MsgAppsBtnOpen      MessageID = "page.apps.btn.open"
	MsgAppsBtnCancel    MessageID = "page.apps.btn.cancel"

	MsgAppsStateRunning      MessageID = "page.apps.state.running"
	MsgAppsStateStopped      MessageID = "page.apps.state.stopped"
	MsgAppsStateInstalling   MessageID = "page.apps.state.installing"
	MsgAppsStateFailed       MessageID = "page.apps.state.failed"
	MsgAppsStateUninstalling MessageID = "page.apps.state.uninstalling"

	MsgAppsInstallTitle    MessageID = "page.apps.install.title"
	MsgAppsInstallSubmit   MessageID = "page.apps.install.submit"
	MsgAppsInstallSetupHdr MessageID = "page.apps.install.setup_heading"
	MsgAppsInstallProgress MessageID = "page.apps.install.progress"
	MsgAppsInstallSuccess  MessageID = "page.apps.install.success"
	MsgAppsInstallFailed   MessageID = "page.apps.install.failed"

	MsgAppsUninstallTitle      MessageID = "page.apps.uninstall.title"
	MsgAppsUninstallWarn       MessageID = "page.apps.uninstall.warn"
	MsgAppsUninstallSubmit     MessageID = "page.apps.uninstall.submit"
	MsgAppsUninstallDeleteData MessageID = "page.apps.uninstall.delete_data"
	MsgAppsUninstallDeleteHint MessageID = "page.apps.uninstall.delete_hint"

	MsgAppsEmptyInstalled MessageID = "page.apps.empty.installed"
	MsgAppsEmptyStore     MessageID = "page.apps.empty.store"

	MsgAppsErrDocker          MessageID = "page.apps.err.docker"
	MsgAppsErrRegistry        MessageID = "page.apps.err.registry"
	MsgAppsErrSignature       MessageID = "page.apps.err.signature"
	MsgAppsErrHealthcheck     MessageID = "page.apps.err.healthcheck"
	MsgAppsErrAlreadyInstalled MessageID = "page.apps.err.already_installed"

	MsgAppsStageStart           MessageID = "page.apps.stage.start"
	MsgAppsStagePullImages      MessageID = "page.apps.stage.pull_images"
	MsgAppsStageProvisionData   MessageID = "page.apps.stage.provision_data"
	MsgAppsStageReservePorts    MessageID = "page.apps.stage.reserve_ports"
	MsgAppsStageStoreSecrets    MessageID = "page.apps.stage.store_secrets"
	MsgAppsStageRenderConfigs   MessageID = "page.apps.stage.render_configs"
	MsgAppsStageStartContainers MessageID = "page.apps.stage.start_containers"
	MsgAppsStageHealthcheck     MessageID = "page.apps.stage.healthcheck"
	MsgAppsStageRegisterRoute   MessageID = "page.apps.stage.register_route"
	MsgAppsStageDone            MessageID = "page.apps.stage.done"
	MsgAppsStageRollback        MessageID = "page.apps.stage.rollback"
	MsgAppsStageError           MessageID = "page.apps.stage.error"
	MsgAppsStageSnapshot        MessageID = "page.apps.stage.snapshot"
	MsgAppsStagePullUpdate      MessageID = "page.apps.stage.pull_update"
	MsgAppsStageStop            MessageID = "page.apps.stage.stop"
	MsgAppsStageRemove          MessageID = "page.apps.stage.remove"
)

// Users page (added in S822961). Visual SSOT: prototype/claude_design/Users.html.
const (
	MsgUsersTitle      MessageID = "page.users.title"
	MsgUsersSubtitle   MessageID = "page.users.subtitle"
	MsgUsersTabUsers   MessageID = "page.users.tab.users"
	MsgUsersTabGroups  MessageID = "page.users.tab.groups"
	MsgUsersTabAuth    MessageID = "page.users.tab.auth"
	MsgUsersTabOIDC    MessageID = "page.users.tab.oidc"

	MsgUsersColUser    MessageID = "page.users.col.user"
	MsgUsersColRole    MessageID = "page.users.col.role"
	MsgUsersColMethods MessageID = "page.users.col.methods"
	MsgUsersColGroups  MessageID = "page.users.col.groups"
	MsgUsersColLast    MessageID = "page.users.col.last"
	MsgUsersColActions MessageID = "page.users.col.actions"

	MsgUsersAuthLocal           MessageID = "page.users.auth.local"
	MsgUsersAuthLocalDesc       MessageID = "page.users.auth.local.desc"
	MsgUsersAuthGoogle          MessageID = "page.users.auth.google"
	MsgUsersAuthGoogleDesc      MessageID = "page.users.auth.google.desc"
	MsgUsersAuthAutoProvision   MessageID = "page.users.auth.auto_provision"
	MsgUsersAuthLink            MessageID = "page.users.auth.link"
	MsgUsersAuthUnlink          MessageID = "page.users.auth.unlink"
	MsgUsersAuthLinkGoogleBtn   MessageID = "page.users.auth.link_google_btn"
	MsgUsersAuthEnabled         MessageID = "page.users.auth.enabled"
	MsgUsersAuthDisabled        MessageID = "page.users.auth.disabled"
	MsgUsersAuthClientIDLabel   MessageID = "page.users.auth.client_id_label"
	MsgUsersAuthSecretLabel     MessageID = "page.users.auth.secret_label"
	MsgUsersAuthSecretPlaceholder MessageID = "page.users.auth.secret_placeholder"

	MsgUsersOIDCColClient   MessageID = "page.users.oidc.col.client"
	MsgUsersOIDCColIssuer   MessageID = "page.users.oidc.col.issuer"
	MsgUsersOIDCColRedirect MessageID = "page.users.oidc.col.redirect"
	MsgUsersOIDCColCreated  MessageID = "page.users.oidc.col.created"
	MsgUsersOIDCEmpty       MessageID = "page.users.oidc.empty"

	MsgUsersGroupsEmpty MessageID = "page.users.groups.empty"
)

// Users CRUD (Sfix001-1) — modal labels and validation.
const (
	MsgUsersAddBtn               MessageID = "page.users.add_btn"
	MsgUsersAddTitle             MessageID = "page.users.add.title"
	MsgUsersFormUsername         MessageID = "page.users.form.username"
	MsgUsersFormUsernameHint     MessageID = "page.users.form.username_hint"
	MsgUsersFormDisplayName      MessageID = "page.users.form.display_name"
	MsgUsersFormPassword         MessageID = "page.users.form.password"
	MsgUsersFormPasswordHint     MessageID = "page.users.form.password_hint"
	MsgUsersFormRole             MessageID = "page.users.form.role"
	MsgUsersFormCancel           MessageID = "page.users.form.cancel"
	MsgUsersFormSubmit           MessageID = "page.users.form.submit"
	MsgUsersEditBtn              MessageID = "page.users.edit_btn"
	MsgUsersEditTitle            MessageID = "page.users.edit.title"
	MsgUsersEditSubmit           MessageID = "page.users.edit.submit"
	MsgUsersDeleteBtn            MessageID = "page.users.delete_btn"
	MsgUsersDeleteConfirm        MessageID = "page.users.delete.confirm"
	MsgUsersErrUsernameRequired  MessageID = "page.users.err.username_required"
	MsgUsersErrPasswordShort     MessageID = "page.users.err.password_short"
	MsgUsersErrUsernameTaken     MessageID = "page.users.err.username_taken"
	MsgUsersErrUsernameInvalid   MessageID = "page.users.err.username_invalid"
	MsgUsersErrInvalidRole       MessageID = "page.users.err.invalid_role"
	MsgUsersErrSelfDeleteForbid  MessageID = "page.users.err.self_delete_forbidden"
	MsgUsersErrLastAdminProtect  MessageID = "page.users.err.last_admin_protected"
	MsgUsersErrCreateFailed      MessageID = "page.users.err.create_failed"
	MsgUsersErrUpdateFailed      MessageID = "page.users.err.update_failed"
	MsgUsersErrDeleteFailed      MessageID = "page.users.err.delete_failed"
)

// Groups CRUD (Sfix001-2).
const (
	MsgGroupsAddBtn              MessageID = "page.groups.add_btn"
	MsgGroupsAddTitle            MessageID = "page.groups.add.title"
	MsgGroupsFormName            MessageID = "page.groups.form.name"
	MsgGroupsFormNameHint        MessageID = "page.groups.form.name_hint"
	MsgGroupsFormDescription     MessageID = "page.groups.form.description"
	MsgGroupsEditMembersBtn      MessageID = "page.groups.edit_members_btn"
	MsgGroupsEditMembersTitle    MessageID = "page.groups.edit_members.title"
	MsgGroupsEditMembersHint     MessageID = "page.groups.edit_members.hint"
	MsgGroupsDeleteBtn           MessageID = "page.groups.delete_btn"
	MsgGroupsDeleteConfirm       MessageID = "page.groups.delete.confirm"
	MsgGroupsColName             MessageID = "page.groups.col.name"
	MsgGroupsColMembers          MessageID = "page.groups.col.members"
	MsgGroupsColDescription      MessageID = "page.groups.col.description"
	MsgGroupsColActions          MessageID = "page.groups.col.actions"
	MsgGroupsErrInvalidName      MessageID = "page.groups.err.invalid_name"
	MsgGroupsErrNameTaken        MessageID = "page.groups.err.name_taken"
	MsgGroupsErrInUseShares      MessageID = "page.groups.err.in_use_shares"
	MsgGroupsErrCreateFailed     MessageID = "page.groups.err.create_failed"
	MsgGroupsErrDeleteFailed     MessageID = "page.groups.err.delete_failed"
	MsgGroupsErrSetMembersFailed MessageID = "page.groups.err.set_members_failed"
)

// Share ACL row picker (Sfix001-3).
const (
	MsgSharesACLPickerAddBtn   MessageID = "page.shares.acl.picker.add_btn"
	MsgSharesACLPickerRemove   MessageID = "page.shares.acl.picker.remove"
	MsgSharesACLPickerKindUser MessageID = "page.shares.acl.picker.kind.user"
	MsgSharesACLPickerKindGrp  MessageID = "page.shares.acl.picker.kind.group"
	MsgSharesACLPickerEmpty    MessageID = "page.shares.acl.picker.empty"
	MsgSharesACLModeRW         MessageID = "page.shares.acl.mode.rw"
	MsgSharesACLModeR          MessageID = "page.shares.acl.mode.r"
)

// Setup wizard (added in S1e7eeb — Step 1 only).
const (
	MsgSetupTitle              MessageID = "page.setup.title"
	MsgSetupSubtitle           MessageID = "page.setup.subtitle"
	MsgSetupStepWelcome        MessageID = "page.setup.step.welcome"
	MsgSetupStepAdmin          MessageID = "page.setup.step.admin"
	MsgSetupStepStorage        MessageID = "page.setup.step.storage"
	MsgSetupStepShare          MessageID = "page.setup.step.share"
	MsgSetupStepRemote         MessageID = "page.setup.step.remote"
	MsgSetupStepDone           MessageID = "page.setup.step.done"
	MsgSetupAdminTitle         MessageID = "page.setup.admin.title"
	MsgSetupAdminLead          MessageID = "page.setup.admin.lead"
	MsgSetupAdminUsername      MessageID = "page.setup.admin.username"
	MsgSetupAdminDisplayName   MessageID = "page.setup.admin.display_name"
	MsgSetupAdminPassword      MessageID = "page.setup.admin.password"
	MsgSetupAdminPasswordAgain MessageID = "page.setup.admin.password_again"
	MsgSetupAdminSubmit        MessageID = "page.setup.admin.submit"
	MsgSetupAdminError         MessageID = "page.setup.admin.error"
	MsgSetupAdminPasswordShort MessageID = "page.setup.admin.password_short"
	MsgSetupAdminPasswordMatch MessageID = "page.setup.admin.password_mismatch"
	MsgSetupAdminUsernameTaken MessageID = "page.setup.admin.username_taken"
	MsgSetupAdminUsernameRule  MessageID = "page.setup.admin.username_rule"
	MsgSetupDeferredNote       MessageID = "page.setup.deferred_note"
)
