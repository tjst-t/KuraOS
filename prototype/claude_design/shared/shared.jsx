// KuraOS shared components & helpers
// Loaded as: <script type="text/babel" src="shared/shared.jsx"></script>
// Exposes everything on window so other Babel scripts can use them.

const { useState, useEffect, useMemo, useRef, useCallback } = React;

// ---- i18n ---------------------------------------------------------------
const DICT = {
  ja: {
    nav_overview: "概要", nav_dashboard: "ダッシュボード", nav_storage: "ストレージ",
    nav_shares: "共有", nav_users: "ユーザー", nav_network: "ネットワーク",
    nav_apps: "アプリ", nav_settings: "設定", nav_admin: "システム管理",
    nav_user: "ユーザー領域", nav_portal: "ポータル", nav_index: "画面一覧",
    nav_setup: "初期セットアップ",
    role_admin: "管理者", role_user: "ユーザー",
    btn_save: "保存", btn_cancel: "キャンセル", btn_create: "作成",
    btn_install: "インストール", btn_apply: "適用", btn_back: "戻る",
    btn_next: "次へ", btn_finish: "完了", btn_done: "完了",
    status_online: "オンライン", status_healthy: "正常", status_warning: "注意",
    status_critical: "異常", status_running: "稼働中", status_stopped: "停止",
    storage_pool: "プール", storage_volume: "ボリューム", storage_disk: "ディスク",
    apps_installed: "インストール済み", apps_store: "アプリストア",
    used: "使用中", free: "空き", total: "合計", quota: "クォータ",
    last_updated: "最終更新", just_now: "たった今", never: "未実行",
    health: "健全性", capacity: "容量", performance: "パフォーマンス",
    add: "追加", edit: "編集", remove: "削除", details: "詳細",
    search: "検索", filter: "フィルター",
    notifications: "通知", logs: "ログ", backup: "バックアップ",
    update: "アップデート", security: "セキュリティ", network: "ネットワーク",
    overview: "概要", config: "構成", advanced: "詳細設定",
    welcome: "ようこそ", new_share: "新規共有", new_user: "新規ユーザー",
    new_pool: "新規プール",
  },
  en: {
    nav_overview: "Overview", nav_dashboard: "Dashboard", nav_storage: "Storage",
    nav_shares: "Shares", nav_users: "Users", nav_network: "Network",
    nav_apps: "Apps", nav_settings: "Settings", nav_admin: "Administration",
    nav_user: "User", nav_portal: "Portal", nav_index: "All screens",
    nav_setup: "Initial setup",
    role_admin: "Administrator", role_user: "User",
    btn_save: "Save", btn_cancel: "Cancel", btn_create: "Create",
    btn_install: "Install", btn_apply: "Apply", btn_back: "Back",
    btn_next: "Next", btn_finish: "Finish", btn_done: "Done",
    status_online: "Online", status_healthy: "Healthy", status_warning: "Warning",
    status_critical: "Critical", status_running: "Running", status_stopped: "Stopped",
    storage_pool: "Pool", storage_volume: "Volume", storage_disk: "Disk",
    apps_installed: "Installed", apps_store: "App Store",
    used: "Used", free: "Free", total: "Total", quota: "Quota",
    last_updated: "Last updated", just_now: "just now", never: "never",
    health: "Health", capacity: "Capacity", performance: "Performance",
    add: "Add", edit: "Edit", remove: "Remove", details: "Details",
    search: "Search", filter: "Filter",
    notifications: "Notifications", logs: "Logs", backup: "Backup",
    update: "Update", security: "Security", network: "Network",
    overview: "Overview", config: "Configuration", advanced: "Advanced",
    welcome: "Welcome", new_share: "New share", new_user: "New user",
    new_pool: "New pool",
  }
};

function useLang() {
  const [lang, setLangState] = useState(() => localStorage.getItem('kura-lang') || 'ja');
  const setLang = (l) => { localStorage.setItem('kura-lang', l); setLangState(l); };
  const t = (k) => (DICT[lang] && DICT[lang][k]) || k;
  return { lang, setLang, t };
}

function useTheme() {
  const [theme, setThemeState] = useState(() => localStorage.getItem('kura-theme') || 'light');
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
    localStorage.setItem('kura-theme', theme);
  }, [theme]);
  return { theme, setTheme: setThemeState, toggleTheme: () => setThemeState(t => t === 'light' ? 'dark' : 'light') };
}

// ---- Icons (1-stroke, currentColor) ----
const Icon = ({ name, size = 16, className = "" }) => {
  const paths = {
    dashboard: <><rect x="3" y="3" width="7" height="9" rx="1.5"/><rect x="14" y="3" width="7" height="5" rx="1.5"/><rect x="14" y="12" width="7" height="9" rx="1.5"/><rect x="3" y="16" width="7" height="5" rx="1.5"/></>,
    storage: <><ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v6c0 1.66 3.58 3 8 3s8-1.34 8-3V6"/><path d="M4 12v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6"/></>,
    share: <><circle cx="6" cy="12" r="2.5"/><circle cx="18" cy="6" r="2.5"/><circle cx="18" cy="18" r="2.5"/><line x1="8" y1="11" x2="16" y2="7"/><line x1="8" y1="13" x2="16" y2="17"/></>,
    users: <><circle cx="9" cy="8" r="3.5"/><path d="M3 20c0-3.3 2.7-6 6-6s6 2.7 6 6"/><circle cx="17" cy="9" r="2.5"/><path d="M15 14c2.5 0 6 1.7 6 5"/></>,
    network: <><circle cx="12" cy="12" r="9"/><path d="M3 12h18"/><path d="M12 3a13 13 0 0 1 0 18M12 3a13 13 0 0 0 0 18"/></>,
    apps: <><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><circle cx="17.5" cy="17.5" r="3.5"/></>,
    settings: <><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3h0a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8v0a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></>,
    portal: <><path d="M3 12L12 3l9 9"/><path d="M5 10v10h14V10"/><rect x="10" y="14" width="4" height="6"/></>,
    bell: <><path d="M6 8a6 6 0 1 1 12 0c0 7 3 7 3 9H3c0-2 3-2 3-9z"/><path d="M10 21a2 2 0 0 0 4 0"/></>,
    sun: <><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></>,
    moon: <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/>,
    plus: <><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></>,
    chevron_right: <polyline points="9 6 15 12 9 18"/>,
    chevron_down: <polyline points="6 9 12 15 18 9"/>,
    chevron_left: <polyline points="15 6 9 12 15 18"/>,
    search: <><circle cx="11" cy="11" r="7"/><line x1="20" y1="20" x2="16.5" y2="16.5"/></>,
    check: <polyline points="4 12 10 18 20 6"/>,
    x: <><line x1="6" y1="6" x2="18" y2="18"/><line x1="6" y1="18" x2="18" y2="6"/></>,
    alert: <><path d="M12 3l10 18H2z"/><line x1="12" y1="10" x2="12" y2="14"/><circle cx="12" cy="17.5" r="0.5" fill="currentColor"/></>,
    info: <><circle cx="12" cy="12" r="9"/><line x1="12" y1="11" x2="12" y2="16"/><circle cx="12" cy="8" r="0.5" fill="currentColor"/></>,
    folder: <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>,
    file: <><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><polyline points="14 3 14 9 20 9"/></>,
    download: <><path d="M12 3v13"/><polyline points="7 11 12 16 17 11"/><line x1="4" y1="20" x2="20" y2="20"/></>,
    upload: <><path d="M12 16V3"/><polyline points="7 8 12 3 17 8"/><line x1="4" y1="20" x2="20" y2="20"/></>,
    refresh: <><polyline points="20 8 20 4 16 4"/><polyline points="4 16 4 20 8 20"/><path d="M20 4l-3.5 3.5a8 8 0 0 0-12.5 5.5"/><path d="M4 20l3.5-3.5a8 8 0 0 0 12.5-5.5"/></>,
    play: <polygon points="6 4 20 12 6 20 6 4"/>,
    pause: <><rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/></>,
    power: <><path d="M12 3v9"/><path d="M5.6 6.6a9 9 0 1 0 12.8 0"/></>,
    cpu: <><rect x="5" y="5" width="14" height="14" rx="2"/><rect x="9" y="9" width="6" height="6"/><line x1="9" y1="2" x2="9" y2="5"/><line x1="15" y1="2" x2="15" y2="5"/><line x1="9" y1="19" x2="9" y2="22"/><line x1="15" y1="19" x2="15" y2="22"/><line x1="2" y1="9" x2="5" y2="9"/><line x1="2" y1="15" x2="5" y2="15"/><line x1="19" y1="9" x2="22" y2="9"/><line x1="19" y1="15" x2="22" y2="15"/></>,
    memory: <><rect x="3" y="6" width="18" height="12" rx="1.5"/><line x1="7" y1="6" x2="7" y2="18"/><line x1="11" y1="6" x2="11" y2="18"/><line x1="15" y1="6" x2="15" y2="18"/><line x1="19" y1="6" x2="19" y2="18"/></>,
    disk: <><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/><line x1="12" y1="3" x2="12" y2="9"/></>,
    shield: <path d="M12 3l8 3v6c0 4.5-3.5 8.3-8 9-4.5-.7-8-4.5-8-9V6z"/>,
    key: <><circle cx="8" cy="14" r="4"/><line x1="11" y1="11" x2="20" y2="2"/><line x1="17" y1="5" x2="20" y2="8"/><line x1="14" y1="8" x2="17" y2="11"/></>,
    cloud: <path d="M7 18a5 5 0 0 1-1-9.9 7 7 0 0 1 13 2.1A4 4 0 0 1 18 18z"/>,
    snapshot: <><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></>,
    docker: <><rect x="3" y="11" width="3" height="3"/><rect x="7" y="11" width="3" height="3"/><rect x="11" y="11" width="3" height="3"/><rect x="15" y="11" width="3" height="3"/><rect x="7" y="7" width="3" height="3"/><rect x="11" y="7" width="3" height="3"/><rect x="11" y="3" width="3" height="3"/><path d="M3 14c0 4 4 6 9 6 8 0 11-6 12-9-1 1-3 1-4 0"/></>,
    sidebar: <><rect x="3" y="4" width="18" height="16" rx="1.5"/><line x1="9" y1="4" x2="9" y2="20"/></>,
    grid: <><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/></>,
    list: <><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3" y2="6.01"/><line x1="3" y1="12" x2="3" y2="12.01"/><line x1="3" y1="18" x2="3" y2="18.01"/></>,
    thermometer: <><path d="M14 14V5a2 2 0 1 0-4 0v9a4 4 0 1 0 4 0z"/></>,
    activity: <polyline points="3 12 7 12 10 4 14 20 17 12 21 12"/>,
    dot: <circle cx="12" cy="12" r="4"/>,
  };
  return (
    <svg className={"icon " + className} width={size} height={size} viewBox="0 0 24 24"
      fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      {paths[name] || null}
    </svg>
  );
};

// ---- Brand mark: stylized 蔵 (storehouse roof) -------------------------
const BrandMark = ({ size = 26 }) => (
  <svg width={size} height={size} viewBox="0 0 32 32" fill="none">
    {/* roof — gable */}
    <path d="M4 13 L16 5 L28 13" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round"/>
    {/* horizontal eave */}
    <line x1="3" y1="14.5" x2="29" y2="14.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round"/>
    {/* body */}
    <rect x="6" y="14.5" width="20" height="13" stroke="currentColor" strokeWidth="1.6" rx="0.5"/>
    {/* door (jade accent) */}
    <rect x="13" y="18" width="6" height="9.5" fill="var(--accent)" stroke="none"/>
    {/* horizontal namako band */}
    <line x1="6" y1="22" x2="26" y2="22" stroke="currentColor" strokeWidth="0.8" opacity="0.5"/>
  </svg>
);

// ---- Sparkline ---------------------------------------------------------
function Sparkline({ data, width = 100, height = 28, color = "", area = true, strokeWidth = 1.4 }) {
  const w = width, h = height;
  if (!data || data.length === 0) return <svg width={w} height={h} className="spark" />;
  const min = Math.min(...data), max = Math.max(...data);
  const range = max - min || 1;
  const stepX = w / (data.length - 1 || 1);
  const points = data.map((v, i) => [i * stepX, h - ((v - min) / range) * (h - 4) - 2]);
  const path = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' ');
  const areaPath = `${path} L${w},${h} L0,${h} Z`;
  return (
    <svg className="spark" width={w} height={h} viewBox={`0 0 ${w} ${h}`}>
      {area && <path d={areaPath} className={"spark-area " + color} />}
      <path d={path} className={"spark-line " + color} strokeWidth={strokeWidth} />
    </svg>
  );
}

// ---- Generate fake series ----------------------------------------------
function genSeries(n, base, jitter, seed = 1) {
  const out = [];
  let v = base, s = seed;
  for (let i = 0; i < n; i++) {
    s = (s * 9301 + 49297) % 233280;
    v = base + (s / 233280 - 0.5) * jitter * 2 + Math.sin(i / 4) * jitter * 0.4;
    out.push(Math.max(0, v));
  }
  return out;
}

// ---- Sidebar Navigation -------------------------------------------------
const NAV_GROUPS = [
  {
    label_key: "nav_admin",
    items: [
      { id: 'dashboard', icon: 'dashboard', label_key: 'nav_dashboard', href: 'Dashboard.html' },
      { id: 'storage',   icon: 'storage',   label_key: 'nav_storage',   href: 'Storage.html' },
      { id: 'shares',    icon: 'share',     label_key: 'nav_shares',    href: 'Shares.html' },
      { id: 'users',     icon: 'users',     label_key: 'nav_users',     href: 'Users.html' },
      { id: 'network',   icon: 'network',   label_key: 'nav_network',   href: 'Network.html' },
      { id: 'apps',      icon: 'apps',      label_key: 'nav_apps',      href: 'Apps.html', badge: '12' },
      { id: 'settings',  icon: 'settings',  label_key: 'nav_settings',  href: 'Settings.html' },
    ],
  },
  {
    label_key: "nav_user",
    items: [
      { id: 'portal',    icon: 'portal',    label_key: 'nav_portal',    href: 'Portal.html' },
    ],
  },
  {
    label_key: "nav_overview",
    items: [
      { id: 'setup',     icon: 'shield',    label_key: 'nav_setup',     href: 'Setup.html' },
      { id: 'index',     icon: 'grid',      label_key: 'nav_index',     href: 'index.html' },
    ],
  }
];

function Sidebar({ active, collapsed, onCollapse, t }) {
  return (
    <aside className="sidebar">
      <div className="sidebar-head">
        <div className="brand-mark"><BrandMark size={26} /></div>
        <div>
          <div className="brand-name">KuraOS</div>
          <div className="brand-sub">v0.4.2</div>
        </div>
      </div>
      <nav className="nav">
        {NAV_GROUPS.map((g, gi) => (
          <React.Fragment key={gi}>
            <div className="nav-section-label">{t(g.label_key)}</div>
            {g.items.map(it => (
              <a key={it.id} href={it.href} className="nav-item" data-active={active === it.id}>
                <Icon name={it.icon} className="nav-icon" />
                <span className="nav-label">{t(it.label_key)}</span>
                {it.badge && <span className="nav-badge">{it.badge}</span>}
              </a>
            ))}
          </React.Fragment>
        ))}
      </nav>
      <div className="sidebar-foot">
        <div className="avatar">TJ</div>
        <div className="sidebar-foot-info">
          <div className="name">tjstkm</div>
          <div className="role">{t('role_admin')}</div>
        </div>
      </div>
    </aside>
  );
}

// ---- Header ------------------------------------------------------------
function Header({ title, crumbs, lang, setLang, theme, toggleTheme, onToggleSidebar, actions, t }) {
  return (
    <header className="header">
      <button className="btn btn-icon btn-ghost" onClick={onToggleSidebar} title="Toggle sidebar">
        <Icon name="sidebar" size={16} />
      </button>
      {crumbs ? (
        <div className="crumbs">
          {crumbs.map((c, i) => (
            <React.Fragment key={i}>
              {i > 0 && <Icon name="chevron_right" size={12} />}
              {i === crumbs.length - 1 ? <strong>{c}</strong> : <span>{c}</span>}
            </React.Fragment>
          ))}
        </div>
      ) : <h1>{title}</h1>}
      <div className="header-actions">
        {actions}
        <div className="icon-toggle" role="group" aria-label="Language">
          <button data-on={lang === 'ja'} onClick={() => setLang('ja')}>JA</button>
          <button data-on={lang === 'en'} onClick={() => setLang('en')}>EN</button>
        </div>
        <button className="btn btn-icon btn-ghost" onClick={toggleTheme} title="Toggle theme">
          <Icon name={theme === 'light' ? 'moon' : 'sun'} size={16} />
        </button>
        <button className="btn btn-icon btn-ghost" title="Notifications">
          <Icon name="bell" size={16} />
        </button>
      </div>
    </header>
  );
}

// ---- Shell wrapper -----------------------------------------------------
function Shell({ active, title, crumbs, headerActions, children }) {
  const { lang, setLang, t } = useLang();
  const { theme, toggleTheme } = useTheme();
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem('kura-collapsed') === 'true');
  useEffect(() => { localStorage.setItem('kura-collapsed', collapsed); }, [collapsed]);
  return (
    <div className="app" data-collapsed={collapsed}>
      <Sidebar active={active} collapsed={collapsed} onCollapse={() => setCollapsed(c => !c)} t={t} />
      <div className="main">
        <Header
          title={title} crumbs={crumbs}
          lang={lang} setLang={setLang}
          theme={theme} toggleTheme={toggleTheme}
          onToggleSidebar={() => setCollapsed(c => !c)}
          actions={headerActions}
          t={t}
        />
        <div className="content">{children}</div>
      </div>
    </div>
  );
}

// ---- Helpers -----------------------------------------------------------
function fmtBytes(n) {
  if (n == null) return '—';
  if (n >= 1e12) return (n/1e12).toFixed(2) + ' TB';
  if (n >= 1e9)  return (n/1e9).toFixed(2)  + ' GB';
  if (n >= 1e6)  return (n/1e6).toFixed(1)  + ' MB';
  if (n >= 1e3)  return (n/1e3).toFixed(1)  + ' KB';
  return n + ' B';
}
function fmtPct(n) { return (n * 100).toFixed(1) + '%'; }

// ---- Export to global ---------------------------------------------------
Object.assign(window, {
  useLang, useTheme, Icon, BrandMark, Sparkline, Sidebar, Header, Shell,
  genSeries, fmtBytes, fmtPct, NAV_GROUPS, DICT,
});
