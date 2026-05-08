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
