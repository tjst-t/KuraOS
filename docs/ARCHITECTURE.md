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

#### engine/system (System Identity & Provisioning)

- **Responsibility**: KuraOS user (engine/user の SSOT) を Linux uid/gid と各 protocol-specific credential / 設定ファイルに projection する **cross-protocol identity 層**。同時に、すべての credential を集約する **vault** の owner でもある。
  - **uid/gid allocator**: KuraOS user_id → Linux uid (private 範囲 30000-39999、決定的、衝突回避)
  - **Linux NSS projection**: `/etc/passwd` / `/etc/group` を atomic 書き換え (Phase 1 では tmp+rename、Phase 2 では NixOS module)
  - **Credential vault**: 全 credential (argon2id, NT-hash, OIDC client secret, app DB password, TLS 秘密鍵 等) を state.db の credentials テーブルに集約。パスワード設定時に平文 (`Plaintext.Use(fn)` で memory lifetime 最短化) → argon2id + NT-hash + 必要なら他の protocol-specific 鍵を 1 トランザクションで生成 → vault に保存。
  - **System file fragments**: `/etc/samba/smb.conf` への include 行追加、systemd unit、sudoers 限定エントリ (`zfs/zpool/smbpasswd` 限定) を冪等に書き込む
  - **Reconcile**: 起動時 (`make serve` 早期段階) に SQLite (config + vault) を真として全 projection を再構築。drift 検出
  - **Backup / Restore**: `kura backup` で config.json + vault を tarball 化、vault は **age で常に暗号化** (案 X、case file-level)。`kura restore` で復号 → state.db に書き戻し → Reconcile。詳細は Data Flow / Backup セクション参照
- **Location**: `engine/system/`
- **Key interfaces**: `Engine.AllocateUID(user) → (uid, gid)`, `Engine.SyncIdentity(user)`, `Engine.SyncCredential(user, plaintext Plaintext)`, `Engine.EnsureSystemFiles()`, `Engine.Reconcile(ctx)`, `Engine.ApplyShareOwnership(share)`, `Engine.ExportVault(passphrase) → []byte`, `Engine.ImportVault(data []byte, passphrase)`
- **Depends on**: engine/user (SSOT 読み取り), engine/share (chown 連携), CmdExecutor (smbpasswd / sudo zpool 等), `filippo.io/age` (vault 暗号化)、副作用は OS の `/etc/passwd`, `/etc/group`, Samba `tdbsam`, ZFS dataset owner
- **Architectural rule** (DESIGN_PRINCIPLES priority #10 / #11): 他の engine が直接 `useradd` / `smbpasswd` / `chown` / `/etc/passwd` を呼ぶこと、独自の env file / secret store を持つことは **禁止**。すべて engine/system / vault 経由。新 protocol (Kerberos KDC 等) を追加する時も engine/system 内で吸収。

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

### Cross-Protocol Identity Resolution (engine/system)

KuraOS user は engine/user の SQLite を SSOT とし、engine/system がすべての protocol への projection を所有する。各 protocol が「KuraOS user X」を解決する経路:

```
engine/user (SSOT)
  ├ User { id, name, email, groups, password_argon2id, smb_enabled, ... }
  ↓ Create / Update / Delete / SetPassword
engine/system (本層 — 全 projection の owner)
  ├─ uid/gid allocator (private 範囲 30000-39999、決定的)
  ├─ /etc/passwd / /etc/group (Linux NSS)
  ├─ Samba tdbsam に NT-hash 書き込み (smbpasswd -s -a 経由)
  ├─ /etc/samba/smb.conf に include 行を冪等追加
  └─ ZFS dataset owner (engine/share.Apply 後に chown gid:gid mode 2775)

各 protocol からの認証 / 識別解決:

  HTTP セッション (Web UI / Core API)
    │ session cookie → engine/auth.SessionStore で user_id 取得
    │ user_id → engine/system.LookupUID(user_id) → uid
    └→ uid を File API / ZFS perms 検査に使う

  SMB クライアント (Windows / macOS Finder)
    │ NTLMv2 challenge/response (server 側に NT-hash 必須)
    │ Samba が tdbsam で NT-hash を引き、challenge を verify
    │ Samba の username map で SMB user → Linux user → uid
    └→ ZFS dataset の uid/gid と照合

  NFSv4 クライアント
    │ sys (uid 数値そのまま) または krb5 (将来)
    │ サーバ側 idmapd は不要 (kura が KuraOS user の uid を /etc/passwd に置くため、NFS 側と uid が一致する)
    └→ ZFS owner と直接照合

  App コンテナ (legacy: bind mount + forward auth)
    │ docker compose の `user: <uid>:<gid>` で起動 (engine/app が engine/system から取得)
    │ bind mount は engine/share の path、ZFS owner は engine/system が chown 済み
    └→ コンテナ内 uid と外側 uid が一致 → ファイル書き込み成功

  App コンテナ (native: OIDC + File API)
    │ OIDC ID Token の `sub` = KuraOS user_id
    │ アプリは Bearer JWT で File API 呼び出し
    │ File API は session 解決 → uid → ZFS perms 検査
    └→ App が直接 ZFS を mount しない設計、すべて File API 経由

  外部 IdP federation (将来)
    │ Google / GitHub OIDC token で KuraOS にログイン
    │ engine/auth.federation が JIT で engine/user.Create
    └→ engine/system が即時 projection (uid 採番 + /etc/passwd + tdbsam)
```

**鍵となる不変条件**: 「KuraOS user X が SMB / NFS / File API のどこから入っても、最終的に同じ Linux uid に解決される」。これにより ZFS dataset の owner / mode による単一の権限モデルが全 protocol を統制する。

### Backup / Restore (config.json + vault)

KuraOS の SSOT は **config.json (declarative state)** と **vault (credential bundle)** の 2 ファイル。バックアップは両方を含む tarball を 1 つ作る。

```
runtime layout:
  /etc/kura/config.json        ← root:0644 (admin が読める)、構造のみ
  /var/lib/kura/state.db       ← root:0600、ランタイム状態 + credentials テーブル

backup primitive:
  $ kura backup -o backup.tar.gz [--encrypt-passphrase]

  backup.tar.gz の中身 (case X = file-level encryption):
    ├ config.json              ← 平文 (構造のみ、機微情報なし)
    └ secrets.kura.age         ← age で常に暗号化された credential vault
                                  passphrase は --encrypt-passphrase で対話入力
                                  v1 は passphrase mode のみ (recipient mode は v1.x)

restore primitive:
  $ kura restore backup.tar.gz
  Enter passphrase: ****
    → tarball を展開、secrets.kura.age を age で復号
    → engine/system が state.db に credentials を import
    → engine/system.Reconcile() で /etc/passwd / tdbsam / smb.conf 等を再 projection
    → 元通り (Web UI、SMB、NFS、apps すべて復旧)
```

**Vault スキーマ** (`secrets.kura` 復号後):

```json
{
  "version": 1,
  "users": [
    {"id": "u1", "argon2id": "$argon2id$...", "nt_hash": "ABC...", "kerberos_keys": []}
  ],
  "oidc_clients": [{"id": "immich", "client_secret": "..."}],
  "app_secrets":  [{"app": "immich", "key": "DB_PASSWORD", "value": "..."}],
  "tls_keys":     [{"cert_id": "default", "private_key_pem": "..."}]
}
```

**設計原則**:

- 全 credential を 1 vault に集約 (`engine/system` が owner)。`engine/{share,app,backup}` 等は env file / 別 secret store を持たない。
- `config.json` は credential を持たず、各エンティティに `credential_state: "set" | "unset"` placeholder のみを置く → ユーザーから見た「1 ユーザー 1 パスワード」 mental model を保ち、片肺バックアップ (config だけ / state だけ) を構造的に許さない (DESIGN_PRINCIPLES priority #1)。
- 暗号化は **age** (`filippo.io/age`、pure Go、CGO 不要)。openssl enc は openssl バージョン間でフォーマットがブレるため不採用。systemd-creds は machine-local 用途なので backup には不適。Phase 2 で sops-nix / agenix と互換が取れる副次効果。
- `config.json` 単独であれば git に push しても credential は漏れない (構造のみ)。`backup.tar.gz` は機微情報として README で警告 — 暗号化必須にはせず、operator の運用判断 (LAN 内転送なら平文 OK、外部にコピーなら暗号化推奨) に委ねる。

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
