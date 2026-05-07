# UI Design System (KuraOS Phase 1)

> 視覚的・構造的なデザインの SSOT は `prototype/claude_design/`。本ドキュメントはそこから抽出した design language の要約 + 実装方針。プロトタイプ HTML/CSS と本ドキュメントが矛盾した場合は **プロトタイプの実体が正**。

## 出典

- `prototype/claude_design/index.html` — 全 9 画面のインデックス
- `prototype/claude_design/shared/shared.css` (822 行) — 全コンポーネントの実体
- `prototype/claude_design/shared/shared.jsx` (314 行) — Icon SVG / `<Shell>` / `useLang` / `useTheme` 等の挙動仕様
- 各画面: `Dashboard.html` / `Storage.html` / `Shares.html` / `Users.html` / `Network.html` / `Apps.html` / `Settings.html` / `Portal.html` / `Setup.html`

## 実装方針: prototype は React、production は htmx

プロトタイプは React + Babel で書かれているが、これは design 探索のため。本実装は **htmx + Tailwind CSS + html/template** で同等の見た目を再現する。

- **CSS は基本そのまま流用可能** — `shared.css` の class 名 (`.card`, `.btn`, `.tbl`, `.tabs`, `.field`, `.bar`, `.badge`, `.stat`, `.banner`, `.wizard-*` 等) を Tailwind の `@layer components` に再定義するか、CSS をそのまま `embed.FS` で同梱する
- **CSS 変数 (Fog palette / spacing / radius / fonts) はそのまま Tailwind config の theme.extend に移植**
- **React のロジック (タブ切替・モーダル開閉・テーマ/言語切替) は htmx + 小さな vanilla JS で書き換え** — 状態を持つほどではないので、サーバ側で持つか `data-` 属性で表現する
- **アイコンは `shared.jsx` の SVG path をそのまま `ui/icons/*.svg` として保存し `<svg>{{ template "icon-storage" }}</svg>` 等で展開**

> プロトタイプの HTML は実装時の **画面構造とマークアップの参照** として読み、コピーできる。React 固有部分 (`useState` / `onClick` 等) は htmx の `hx-get` / `hx-post` / `hx-target` / `hx-swap` に置換する。

## ブランド名

正式名は **"KuraOS"**。プロトタイプ初版では "KuraNAS" 表記が混在していたが、2026-05-07 時点で全ファイルを KuraOS に統一済み。

## 色 — "Fog" palette + jade accent

| トークン | 用途 |
|---|---|
| `--fog-0 .. --fog-1000` | グレースケール (oklch ベース、わずかに 240 度 = 青寄り) |
| `--accent` (jade `oklch(62% 0.10 175)`) | アクション・active 状態・進捗バー・focus ring |
| `--ok` `--warn` `--crit` `--info` | ステータス色 (badge / dot / bar / banner) |

**ライト/ダーク両対応必須**。`[data-theme="dark"]` 切替で全トークンが入れ替わる。プロトタイプ準拠で v1 から実装する。

## タイポグラフィ

- 本文: **IBM Plex Sans** (300/400/500/600/700)
- mono: **IBM Plex Mono** (400/500/600) — パス・SN・cron 式・サイズ・温度等の technical fields
- `font-feature-settings: 'cv02','cv03','cv04','cv11'` を body に指定
- 見出しサイズ: page-title 22px / wizard-title 30px / index-title 36px / card-title 13px / stat-value 26px

## レイアウト寸法

| 要素 | サイズ |
|---|---|
| Sidebar | 232px (展開) / 60px (折畳) |
| Header | 56px (sticky, backdrop-filter blur) |
| Content padding | 28px 32px 64px |
| Content max-width | 1320px (通常) / 960px (narrow) |
| Wizard aside | 280px、main は max 760px |

## コンポーネント語彙 (実装で再現する単位)

- **Card** (`.card` + `.card-head` / `.card-body` / `.card-foot`) — 全画面の主構造単位
- **Stat** (`.stat-label` / `.stat-value` / `.unit` / `.stat-meta`) — ダッシュボードの数値表示
- **Sparkline** (40-60 点の SVG パス、area + line、status カラー) — トレンド表示
- **Badge** (`.badge` + `.ok|warn|crit|info`、`.badge-dot` 付き) — ステータス
- **Bar** (`.bar` + 内側 `<span>` の width%、status 色) — 容量・進捗
- **Tabs** (`.tabs` / `.tab[data-active]`) — Storage / Apps / Settings 内タブ
- **Switch** (`.switch[data-on]`) — 通知オン/オフ等のトグル
- **Banner** (`.banner` + `.warn|crit|ok`、左端 3px ボーダー) — インラインアラート
- **Modal** (`.modal-backdrop` + `.modal`、blur backdrop) — アプリインストール詳細等
- **Search** (`.search` + 内側 `<input>`) — ヘッダ内検索 (⌘K プレースホルダ)
- **Table** (`.tbl`、`.td-mono` / `.td-num`) — ディスク・スナップショット・ユーザー一覧
- **Wizard** (`.wizard-shell` / `.wizard-aside` / `.wizard-step[data-state="active|done|todo"]` / `.wizard-main`) — 初期セットアップウィザード
- **Sidebar nav** (`.nav` / `.nav-section-label` / `.nav-item[data-active]` の active インジケータが `::before` の 2px 縦バー) — 左固定ナビ
- **Brand mark** (`<BrandMark size>`、jade ベースの蔵 (kura) ロゴ — `shared.jsx` 参照) — ヘッダ・wizard・ログイン画面
- **Icon** (1-stroke SVG、`currentColor`、size 12/14/16/18 等) — `shared.jsx` の Icon コンポーネントに約 30 種定義済み

## 形・余白・角丸スケール

```
spacing: 4 / 8 / 12 / 16 / 20 / 24 / 32 / 40 / 48 px
radius:  4 / 6 / 8 / 12 / 16 px
shadow:  sm / md / lg (light/dark でアルファ強度を切替)
```

## 確立した UX パターン

- **Dashboard**: グリッドの Stat カード (CPU/MEM/Net/Temp、Sparkline 付) → プール一覧 (PoolRow) → アプリ稼働状況 (AppRow) → 直近イベント (EventRow)
- **Storage**: プールカード 2 列 → タブ (ボリューム / ディスク / スナップショット / Scrub) → タブごとに table または card
- **Apps**: 切替トグル (Installed / Store) → カテゴリフィルタ → カードグリッド → クリックでモーダル install フォーム
- **Setup**: 6 ステップ (welcome / admin / storage / share / remote / done)、左 aside にステップリスト、メインに大きな見出し + 説明 + フォーム + 下部 next/back ボタン
- **Header actions**: 各画面ごとに右上に search / 言語 (JA/EN) / テーマ (sun/moon) / プライマリ CTA (新規プール・新規共有 等)

## 実装時のチェックポイント

- [ ] CSS 変数を Tailwind config に移植 (`theme.extend.colors.fog.*`, `accent`, `ok|warn|crit|info`)
- [ ] IBM Plex Sans / Mono を Google Fonts または self-host で読み込み
- [ ] light/dark トグルを `<html data-theme>` で実装、設定は SQLite (ユーザー単位) に保存
- [ ] アイコン SVG を `ui/icons/*.svg` に切り出し、テンプレートで展開
- [ ] プロトタイプ HTML を画面ごとの「視覚仕様書」として実装担当の sub-agent に渡す (各 sprint の story でファイル名を明記)

## Sprint との対応

| Sprint | プロトタイプ参照画面 |
|---|---|
| `S464e47` UI シェル | `index.html`、`shared.css` 全体、`Dashboard.html` (placeholder 比較用) |
| `S1e7eeb` Auth + Setup | `Setup.html` (Step 1〜2 だけ実装) |
| `Se3b190` / `S9db742` Storage | `Storage.html` |
| `Sd64f38` Share | `Shares.html` |
| `S65b510` App lifecycle | `Apps.html` (Installed タブ + Store タブ + install モーダル) |
| `S822961` Auth (Users) | `Users.html` |
| `S8a756d` Monitor + Notify | `Dashboard.html` の Stat / Sparkline / EventRow、`Settings.html` の通知設定 |
| `Se1e7a6` Backup | `Settings.html` のバックアップタブ |
| `Sf92666` Network / TLS / Logs | `Network.html`、`Settings.html` の TLS / 更新 / ログタブ |
| `S0eedaa` File API + Files UI | (プロトタイプには Files 画面が無い → 設計時に追加プロトタイプを作成) |
| `S99702c` Portal + Wizard 完成 | `Portal.html`、`Setup.html` 全 6 ステップ |
