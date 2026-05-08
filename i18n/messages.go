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
