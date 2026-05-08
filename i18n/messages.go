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
	MsgStorageNewPoolSpecial     MessageID = "page.storage.new_pool.special"
	MsgStorageNewPoolSpecialHint MessageID = "page.storage.new_pool.special_hint"
	MsgStorageNewPoolSpares      MessageID = "page.storage.new_pool.spares"
	MsgStorageNewPoolSubmit      MessageID = "page.storage.new_pool.submit"
	MsgStorageNewPoolCancel      MessageID = "page.storage.new_pool.cancel"
	MsgStorageNewPoolCreated     MessageID = "page.storage.new_pool.created"

	MsgStorageLayoutSingle MessageID = "page.storage.layout.single"
	MsgStorageLayoutMirror MessageID = "page.storage.layout.mirror"
	MsgStorageLayoutRaidZ1 MessageID = "page.storage.layout.raidz1"
	MsgStorageLayoutRaidZ2 MessageID = "page.storage.layout.raidz2"
	MsgStorageLayoutRaidZ3 MessageID = "page.storage.layout.raidz3"

	MsgStorageTabVolumes   MessageID = "page.storage.tab.volumes"
	MsgStorageTabSnapshots MessageID = "page.storage.tab.snapshots"
	MsgStorageTabDisks     MessageID = "page.storage.tab.disks"

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
