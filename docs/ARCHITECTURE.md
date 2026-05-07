# Architecture: KuraOS

## Overview

KuraOS は Linux ベースの自宅 NAS OS。単一 Go バイナリ `kura` が Gateway / Core API / File API / Engine 群を内包するモジュラーモノリス。設定の SSOT は宣言的 `config.json`、ランタイム状態は SQLite、メトリクスは自前バイナリ Ring Buffer、ログは JSONL。Phase 1 は ansible-nas 上で動作し、Phase 2/3 で NixOS / 独自ディストロに置き換えても UI/API/Engine 層はそのまま流用される。

## Components

### Gateway

- **Responsibility**: HTTP リバースプロキシ。TLS 終端、認証ミドルウェア (セッション/JWT)、ルーティング、内部ヘッダ (`X-Kura-User`, `X-Kura-Token` HMAC) への変換
- **Location**: `gateway/`
- **Key interfaces**: `/ui/*` → ユーザーポータル、`/ui/admin/*` → 管理 UI、`/api/*` → Core API、`/api/files/*` → File API、`/oidc/*` → OIDC Provider、`/apps/:id/*` → アプリコンテナ、`/metrics` → OpenMetrics
- **Depends on**: auth/session, auth/oidc, all engines (rounting metadata)

### Core API

- **Responsibility**: 管理操作と native アプリ向け API (Storage / User / Settings / Notification)。htmx 用のサーバサイドレンダリングフラグメントもここから返す
- **Location**: `api/`
- **Key interfaces**: REST/htmx ハンドラ。Engine 層を呼ぶ薄いレイヤ
- **Depends on**: 全 Engine

### File API

- **Responsibility**: native アプリ向けの HTTP ファイル CRUD。Range request、ストリーミング、ディレクトリ一覧、メタデータ取得を提供
- **Location**: `fileapi/`
- **Key interfaces**: `GET/PUT/DELETE/POST /api/files/{share}/{path}`、アプリスコープ (path_deny / share allowlist) の評価
- **Depends on**: engine/storage, engine/share, engine/user

### Engine 群

#### engine/storage

- **Responsibility**: ZFS の抽象化。Pool / Volume / Snapshot / Disk / Spare 管理、SMART 取得、既存プールの import
- **Location**: `engine/storage/`
- **Key interfaces**: `StorageEngine` interface (ListPools, CreatePool, CreateVolume, SetQuota, CreateSnapshot, ScanImportable, ImportPool, ListDisks, GetSMART, AddSpare 等)
- **Depends on**: `CmdExecutor` interface (テストでモック化される ZFS CLI ラッパー)

#### engine/share

- **Responsibility**: SMB/NFS 共有設定生成。smb.conf / exports は SQLite 状態からテンプレート生成
- **Location**: `engine/share/`
- **Key interfaces**: `ShareEngine` interface (CreateSMBShare, CreateNFSExport, UpdateSMBConfig)
- **Depends on**: engine/storage, OS の samba / nfs-kernel-server サービス

#### engine/app

- **Responsibility**: アプリのインストール / 更新 / 削除、マニフェスト処理、Docker compose 生成、レジストリ署名検証 (cosign keyless)、ドライバモデル (post_install フック)
- **Location**: `engine/app/`
- **Key interfaces**: Manifest, RegistryClient, AppLifecycle
- **Depends on**: engine/storage (dataset 自動作成), engine/share (bind mount), Gateway (ルーティング登録), engine/auth/oidc (client 自動登録), Docker API

#### engine/user

- **Responsibility**: ユーザー / グループ管理、認証方法の紐付け
- **Location**: `engine/user/`
- **Key interfaces**: User, AuthMethod, UserStore (SQLite-backed)

#### engine/auth/{oidc,federation,session}

- **Responsibility**: OIDC Provider (zitadel/oidc 組み込み)、外部 IdP (Google/GitHub) フェデレーション、ブラウザセッション管理
- **Location**: `engine/auth/`
- **Key interfaces**: OIDC エンドポイント (`/oidc/.well-known/openid-configuration` 等)、`auth.Session` ミドルウェア
- **Depends on**: engine/user, store (zitadel/oidc の Storage interface 実装)

#### engine/monitor

- **Responsibility**: メトリクス収集 (storage / system / apps)、自前 Ring Buffer ストア、アラート評価
- **Location**: `engine/monitor/`
- **Key interfaces**: MetricsStore (raw.bin / 5min.bin / 1hour.bin)、AlertEvaluator
- **Depends on**: engine/storage, Docker API, /proc, engine/notify (event publish)

#### engine/notify

- **Responsibility**: Event Bus (Go channel)、通知チャネル (ntfy / webhook / line_notify / smtp / gotify)、event_log への永続化
- **Location**: `engine/notify/`
- **Key interfaces**: `Event`, `NotificationChannel`, EventBus

#### engine/backup

- **Responsibility**: ZFS スナップショット (cron schedule + retention)、リモートバックアップ (zfs_send / restic / rclone)、リストア
- **Location**: `engine/backup/`
- **Key interfaces**: `BackupBackend` interface (Send, Restore, List)、SnapshotScheduler
- **Depends on**: engine/storage, engine/notify

#### engine/network/acme

- **Responsibility**: ACME (Let's Encrypt 等) 証明書発行と DNS-01 チャレンジ。lego ラッパー + プロバイダレジストリ
- **Location**: `engine/network/acme/`
- **Key interfaces**: `DNSProvider` interface、`registry.Register/Get/List`。v1 は Cloudflare のみ

#### engine/logging

- **Responsibility**: KuraOS / Docker / journald のログ収集と JSONL 永続化、SSE ライブストリーム
- **Location**: `engine/logging/`
- **Key interfaces**: LogIngester, LogStore (日次ローテーション)

### i18n

- **Responsibility**: メッセージ ID → ロケール文字列変換、`embed.FS` でロケール埋め込み
- **Location**: `i18n/`
- **Key interfaces**: `Translator.T(id MessageID, args ...any) string`、context への乗せ替え

### UI

- **Responsibility**: htmx + Tailwind の HTML テンプレートと静的アセット。`embed.FS` で配信
- **Location**: `ui/`
- **Key interfaces**: html/template、`T` 関数で i18n 文字列展開

### store

- **Responsibility**: SQLite アクセス。マイグレーション管理 (goose 等)、各 Engine 専用テーブル、`event_log` / OIDC データ
- **Location**: `store/`

### config

- **Responsibility**: 宣言的 `config.json` の import/export、diff 計算、apply (Engine 群への配信)。Terraform 的 plan/apply
- **Location**: `config/`

## Data Flow

### ブラウザリクエスト

```
ブラウザ → Gateway (TLS終端 + 認証)
  ↓ X-Kura-User + X-Kura-Token (HMAC) を内部ヘッダ付与
  ├→ /ui/{admin,*}     → html/template (htmx) → Engine
  ├→ /api/*             → Core API ハンドラ → Engine
  ├→ /api/files/*       → File API → engine/storage + engine/share
  ├→ /apps/:id/*        → アプリコンテナ (bind mount or oidc/forward_auth)
  └→ /oidc/*            → engine/auth/oidc (zitadel/oidc op)
```

### 宣言的設定の apply

```
config.json
  ↓ import (kura config apply)
config パッケージが diff 計算
  ↓ Engine ごとの差分 (pools, volumes, shares, apps, auth, backup, tls...)
各 Engine が冪等に apply (zfs / smbd reload / docker compose / OIDC client 登録 etc.)
  ↓ 成功した変更を SQLite に反映
SQLite (ランタイム状態)
```

### イベント / 通知

```
Engine (storage/app/backup 等) → Event を EventBus に publish
  ↓
NotificationEngine が subscribe
  ├→ event_log テーブルに永続化 (常に)
  └→ severity / category フィルタ → 設定済みチャネル (ntfy / webhook 等) に送信
```

### メトリクス

```
engine/monitor の collector が 30s 間隔で収集
  ↓
Ring Buffer (raw.bin) に append
  ├→ 5 分集約タスクが 5min.bin に書き込み
  ├→ 1 時間集約タスクが 1hour.bin に書き込み
  └→ AlertEvaluator が config.json のルールを評価 → Event publish
Gateway /metrics は raw.bin の最新値を OpenMetrics 形式で出力
```

## Directory Structure

```
KuraOS/
├── cmd/
│   └── kura/             # main.go (単一バイナリ)
├── gateway/              # ルーティング・認証・TLS 終端
├── api/                  # Core API ハンドラ
├── fileapi/              # File API ハンドラ
├── engine/
│   ├── storage/          # ZFS 操作の抽象化
│   ├── share/            # SMB/NFS 設定生成
│   ├── app/              # Docker 管理・マニフェスト処理
│   ├── user/             # ユーザー・グループ
│   ├── auth/
│   │   ├── oidc/         # zitadel/oidc 組み込み OP
│   │   ├── federation/   # 外部 IdP 連携
│   │   └── session/      # ブラウザセッション
│   ├── monitor/          # メトリクス収集・アラート評価
│   ├── notify/           # 通知チャネル・Event Bus
│   ├── backup/           # スナップショット・リモートバックアップ
│   ├── network/
│   │   └── acme/         # ACME + DNS プロバイダ
│   └── logging/          # ログ収集・ストア
├── i18n/                 # i18n フレームワーク + locales/
├── ui/                   # html/template (htmx) + 静的アセット (embed.FS)
├── store/                # SQLite ラッパー・マイグレーション
├── config/               # config.json import/export/diff/apply
├── internal/             # 共通ユーティリティ (cmd 実行、HMAC 等)
└── docs/                 # 設計ドキュメント・ROADMAP・sprint-logs
```

## Infrastructure

- **Database**: SQLite — 設定状態 / event_log / OIDC データ (authorize code, refresh token, dynamic clients)
- **Metrics store**: 自前バイナリ Ring Buffer — `metrics/{name}/{raw,5min,1hour}.bin`
- **Log store**: JSONL ファイル — `logs/{YYYY-MM-DD}.jsonl` (日次ローテーション)
- **Config**: `/etc/kura/config.json` (SSOT) + `embed.FS` (ロケール / UI テンプレート)
- **Storage**: ZFS (Linux) — pool / dataset / snapshot を CLI 経由で操作
- **Container runtime**: Docker (compose YAML を `kura` が動的生成)
- **Auth registry**: アプリレジストリは GitHub Releases / GitHub Pages、署名は cosign keyless

## Related Documents

- `docs/initial-input/Kuraos-design.md` — KuraOS 全機能の権威ある詳細設計 (1874 行)。各 Engine / API / マニフェスト / ドライバモデル / ACME / バックアップ / 初期セットアップウィザードの仕様を含む
- `docs/VISION.json` — プロダクトビジョン、ターゲット、Out-of-scope、技術制約
- `docs/DESIGN_PRINCIPLES.json` — 優先順位付き判断ルールと禁則 (autopilot/sprint auto の自律判断ガイド)
- `docs/UI_DESIGN.md` — UI デザインシステム (Fog palette / IBM Plex / コンポーネント語彙 / 画面 ↔ Sprint 対応表)
- `prototype/claude_design/` — UI 視覚仕様の SSOT (全 9 画面の HTML/CSS)。React 実装だが design 探索用。production は htmx に置換しつつ CSS / 構造は流用
- `docs/ROADMAP.json` — スプリント計画と進捗 (sprint roadmap で生成)
