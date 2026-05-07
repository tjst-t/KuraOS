# KuraOS 設計ドキュメント

## 1. プロジェクト概要

### ビジョン

Linux ベースの自作 NAS OS「KuraOS」。Synology のようなデスクトップメタファーは持たず、TrueNAS 程度のシンプルな管理 UI を提供する。既存 OSS アプリ（immich, Jellyfin 等）をワンクリックでインストールできるアプリエコシステムを持ち、ストレージスペシャリストの知見を活かした賢いデフォルト設定で、ユーザーに複雑さを見せない。

### スコープ外

KuraOS は以下を担わない。これらが必要なユーザーは専用アプリ（アプリストアから）または別途のサービスを利用する：

- ドキュメント協調編集（Collabora/OnlyOffice）
- カレンダー・連絡先同期（CalDAV/CardDAV）
- 共有リンク（外部公開 URL）による不特定多数へのファイル共有
- WebDAV 互換のフルファイルサーバ
- メールサーバ・チャット・グループウェア機能
- エンタープライズ向け機能（SAML、LDAP、SCIM、クラスタリング）

これらは Nextcloud のような統合プラットフォームの守備範囲。KuraOS は NAS OS としての本分（ストレージ、Share、アプリホスティング、ユーザー管理）に集中する。

### 開発ロードマップ

- **Phase 1**: ansible-nas 上に WebUI + API を構築。KuraOS としての機能セットと UX を確立する
- **Phase 2/3**: 基盤を NixOS ベースの宣言的構成、またはイメージベースの独自ディストロ（mkosi/systemd-sysext 等）に移行する

Phase 1 の成果物（UI、API、Engine）はそのまま Phase 2/3 のフロントエンド・ビジネスロジックとして持ち越す設計。

### 技術スタック

- **バックエンド**: Go（単一バイナリ、モジュラーモノリス）
- **フロントエンド**: htmx + Tailwind CSS（Fog パレット踏襲）
- **設定ストア**: SQLite（ランタイム状態）
- **メトリクス**: 自前バイナリ Ring Buffer
- **ログ**: JSONL ファイル（日次ローテーション）
- **アプリ管理**: Docker API（compose YAML を生成・適用）
- **ストレージ**: ZFS（CLI ラッパー経由）
- **OIDC Provider**: zitadel/oidc ライブラリ組み込み
- **ACME**: go-acme/lego ベース、DNS プロバイダはモジュール化
- **i18n**: 自前実装、embed.FS でロケール埋め込み

---

## 2. アーキテクチャ

### コンポーネント構成

```
┌─────────────────────────────────────────────────┐
│                   ブラウザ                        │
└──────────────────────┬──────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────┐
│              Gateway (リバプロ)                    │
│  - 認証 (セッション/JWT)                          │
│  - /ui/*         → ユーザーポータル                │
│  - /ui/admin/*   → 管理UI                        │
│  - /apps/:id/*   → アプリコンテナへ転送            │
│  - /api/files/*  → File API                      │
│  - /api/*        → Core API                      │
│  - /oidc/*       → OIDC Provider                 │
│  - /metrics      → OpenMetrics (Prometheus形式)   │
└──┬──────────┬──────────┬───────────┬────────────┘
   │          │          │           │
┌──▼───┐ ┌───▼────┐ ┌───▼────┐ ┌───▼──────────┐
│  UI  │ │Core API│ │File API│ │アプリコンテナ  │
│(htmx)│ │        │ │        │ │(Docker)       │
└──────┘ └───┬────┘ └───┬────┘ └──────────────┘
             │          │
        ┌────▼──────────▼────┐
        │    NAS Engine       │
        │  - Storage (ZFS)    │
        │  - Share (SMB/NFS)  │
        │  - User/Auth/OIDC   │
        │  - App Lifecycle    │
        │  - Network          │
        │  - Monitoring       │
        │  - Notification     │
        │  - Backup           │
        │  - i18n             │
        └─────────┬──────────┘
                  │
     ┌────────────┼────────────┐
     │            │            │
 ┌───▼────┐ ┌────▼─────┐ ┌───▼───┐
 │ SQLite │ │Ring Buff. │ │ JSONL │
 │(設定)   │ │(メトリクス)│ │(ログ)  │
 └────────┘ └──────────┘ └───────┘
```

### バイナリ構成（モジュラーモノリス）

Phase 1 では全コンポーネントを単一 Go バイナリに収める。

```
kura (単一バイナリ)
├── gateway/       # ルーティング・認証ミドルウェア・TLS終端
├── api/           # Core API handlers
├── fileapi/       # File API handlers
├── engine/
│   ├── storage/   # ZFS操作の抽象化
│   ├── share/     # SMB/NFS設定生成
│   ├── app/       # Docker管理・マニフェスト処理
│   ├── user/      # ユーザー・グループ
│   ├── auth/
│   │   ├── oidc/         # zitadel/oidc 組み込み OP
│   │   ├── federation/   # 外部 IdP (Google等) 連携
│   │   └── session/      # ブラウザセッション
│   ├── monitor/   # メトリクス収集・アラート評価
│   ├── notify/    # 通知チャネル管理・Event Bus
│   ├── backup/    # スナップショット・リモートバックアップ
│   ├── network/
│   │   └── acme/         # ACME 実装（DNS プロバイダはモジュール化）
│   └── logging/   # ログ収集・ストア
├── i18n/          # i18n フレームワーク + ロケールファイル
├── ui/            # embed.FSで静的ファイル配信
├── store/         # SQLite
└── config/        # 宣言的設定のimport/export・diff/apply
```

---

## 3. 横断機能

### 3.1 宣言的設定（IaC ネイティブ）

#### SSOT 設計

宣言的な設定ファイル（`config.json`）が本当の SSOT。SQLite はランタイムのキャッシュ兼状態保持。

```
config.json (宣言的定義)
    ↓ import (差分検出・適用)
  SQLite (ランタイム状態)
    ↓ export
config.json (再現可能)
```

#### config.json 全体構造

```json
{
  "pools": [
    {
      "name": "tank",
      "topology": {"type": "raidz2", "disks": ["sda","sdb","sdc","sdd"]},
      "special": {"type": "mirror", "disks": ["nvme0n1","nvme1n1"]},
      "small_block_threshold": "32K",
      "spares": ["sde"]
    },
    {
      "name": "fast",
      "topology": {"type": "mirror", "disks": ["nvme2n1","nvme3n1"]}
    }
  ],
  "volumes": [
    {"name": "tank/media", "quota": "2T", "preset": "media"},
    {"name": "tank/docs", "quota": "500G", "preset": "general"},
    {"name": "fast/apps", "preset": "general"}
  ],
  "shares": [
    {
      "name": "movies",
      "type": "smb",
      "path": "tank/media/movies",
      "access": [
        {"user": "tjstkm", "level": "readwrite"},
        {"group": "family", "level": "readonly"}
      ]
    }
  ],
  "apps": [
    {
      "name": "immich",
      "version": "1.111.0",
      "settings": {"upload_limit": "10G"},
      "shares": [{"name": "photos", "access": "readwrite"}],
      "visibility": {"groups": ["family"]}
    }
  ],
  "app_registries": [
    {
      "name": "official",
      "url": "https://registry.kuraos.org",
      "trust": {
        "type": "cosign_keyless",
        "identity_regex": "^https://github.com/kuraos-org/"
      }
    }
  ],
  "auth": {
    "provider": {
      "local": true,
      "google": {
        "enabled": true,
        "client_id": "xxx.apps.googleusercontent.com",
        "client_secret_env": "GOOGLE_CLIENT_SECRET",
        "allowed_domains": []
      }
    },
    "auto_provision": false
  },
  "backup": {
    "snapshots": {
      "schedule": "0 3 * * *",
      "retention": {"hourly": 24, "daily": 30, "monthly": 12}
    },
    "remote": [
      {
        "name": "offsite-nas",
        "type": "zfs_send",
        "target": "ssh://backup@remote-nas/tank/backup",
        "schedule": "0 4 * * *",
        "datasets": ["shares/*", "apps/*/db"]
      }
    ]
  },
  "tls": {
    "mode": "self_signed",
    "self_signed": {"ca_cn": "KuraOS Local CA"}
  },
  "remote_access": {
    "mode": "tailscale",
    "tailscale": {"auth_key_env": "TS_AUTH_KEY", "funnel": false}
  },
  "notifications": {
    "channels": [
      {
        "name": "my-ntfy",
        "type": "ntfy",
        "url": "https://ntfy.sh/my-nas-alerts",
        "events": ["critical", "warning"]
      }
    ]
  },
  "monitoring": {
    "interval": "30s",
    "retention": {"raw": "24h", "5min": "7d", "1hour": "90d"},
    "alerts": [
      {"name": "pool_capacity_warning", "metric": "pool.*.used_percent", "condition": "> 80", "severity": "warning"},
      {"name": "pool_capacity_critical", "metric": "pool.*.used_percent", "condition": "> 90", "severity": "critical"},
      {"name": "disk_temp_high", "metric": "disk.*.temperature", "condition": "> 55", "severity": "warning"}
    ]
  },
  "storage": {
    "scrub": {
      "schedule": "0 2 * * 0",
      "notify_on_error": true,
      "notify_on_completion": false
    }
  },
  "update": {
    "channel": "stable",
    "auto_check": true,
    "auto_apply": false,
    "system_update": {
      "security_auto": true,
      "reboot": {
        "mode": "manual",
        "schedule": "0 4 * * 0"
      },
      "pin": {
        "zfs-dkms": "2.2.*",
        "samba": "4.19.*",
        "docker-ce": "5:26.*"
      }
    }
  },
  "logging": {
    "retention": {"nas_os": "30d", "apps": "7d", "system": "14d"},
    "max_size": "500MB"
  },
  "i18n": {
    "default_locale": "ja",
    "fallback_locale": "ja"
  }
}
```

#### 差分適用（Terraform 的 plan/apply）

```bash
$ kura config export > current.json
$ vim current.json
$ kura config apply current.json --dry-run
  ~ volume tank/docs: quota 500G → 1T
  + share "reports" (smb, tank/docs/reports)

$ kura config apply current.json
  Applied 2 changes.
```

### 3.2 i18n フレームワーク

KuraOS は日本語 / 英語を含む多言語対応を将来的に提供することを想定し、v1 から i18n フレームワークを組み込む。

#### v1 のスコープ

- 日本語ロケール (`ja.json`) のみリリース
- 英語ロケール (`en.json`) は v1.x 以降で追加
- フォールバック: 該当キーが見つからない場合は `ja.json` → ID 文字列の順

#### 実装方針

- バックエンド: `i18n.Translator` を context に乗せて全 Engine からアクセス可能にする
- フロントエンド: `html/template` の関数 `T` 経由で文字列展開（htmx 前提のサーバーサイドレンダリング）
- ロケールファイルは `embed.FS` で単一バイナリに埋め込み
- ユーザーごとの言語設定は SQLite に保存

#### 翻訳カバー範囲

| 対象 | i18n 対応 |
|------|----------|
| UI ラベル・見出し・ボタン | ✅ |
| エラーメッセージ・通知本文 | ✅ |
| アプリマニフェストの display_name / description / label | ✅ |
| 構造化ログのフィールド名 | ❌ |
| API レスポンスのステータスコード | ❌ |
| 設定キー名 (config.json) | ❌ |

#### Translator インターフェース

```go
type MessageID string

const (
    MsgPoolDegraded   MessageID = "pool.degraded"
    MsgDiskTempHigh   MessageID = "disk.temp.high"
    MsgBackupFailed   MessageID = "backup.failed"
    MsgInstallSuccess MessageID = "app.install.success"
)

type Translator struct {
    locales map[string]map[string]string
    current string
}

func (t *Translator) T(id MessageID, args ...any) string {
    template, ok := t.locales[t.current][string(id)]
    if !ok {
        if template, ok = t.locales["ja"][string(id)]; !ok {
            return string(id)
        }
    }
    return fmt.Sprintf(template, args...)
}
```

#### 設計上の規約

- コード中にハードコードされた日本語文字列を残さない
- ユーザー向け文字列は必ず `t.T(MessageID, args...)` 経由で出力
- v1.x で静的解析ツールによる検出を導入予定（v1 では PR レビューで担保）

#### ロケールファイルの例

```json
// i18n/locales/ja.json
{
  "pool.degraded": "プール %s が degraded 状態です",
  "disk.temp.high": "ディスク %s の温度が %d°C に達しました",
  "backup.failed": "バックアップ %s が失敗しました: %s",
  "app.install.success": "%s のインストールが完了しました"
}
```

---

## 4. ストレージ管理（Engine 層）

### 4.1 概念の抽象化

| KuraOS の概念 | ZFS の実体 |
|---|---|
| StoragePool | zpool |
| Volume | dataset (filesystem) |
| Share | dataset + SMB/NFS export |
| Snapshot | snapshot |
| Disk | vdev member |

### 4.2 StorageEngine インターフェース

```go
type StorageEngine interface {
    // Pool管理
    ListPools() ([]Pool, error)
    GetPool(name string) (*Pool, error)
    CreatePool(cfg PoolConfig) (*Pool, error)

    // Volume管理
    CreateVolume(pool, name string, opts VolumeOpts) (*Volume, error)
    DestroyVolume(pool, name string) error
    SetQuota(pool, name string, quota uint64) error

    // Snapshot
    CreateSnapshot(volume, name string) (*Snapshot, error)
    ListSnapshots(volume string) ([]Snapshot, error)
    Rollback(volume, snapshot string) error

    // Disk
    ListDisks() ([]Disk, error)
    GetSMART(disk string) (*SMARTInfo, error)

    // 既存プールのインポート
    ScanImportable() ([]ImportablePool, error)
    ImportPool(name string, opts ImportOpts) (*Pool, error)

    // hot spare
    AddSpare(pool string, disk string) error
    RemoveSpare(pool string, disk string) error
    ListSpares(pool string) ([]Spare, error)
}
```

### 4.3 ZFS 実装

CLI ラッパーで実装。libzfs_core の CGo バインディングは不使用（ビルド依存を最小に）。テスト時にモック可能にするため `CmdExecutor` をインターフェース化。

### 4.4 用途別データセットプリセット

| プリセット | recordsize | compression | 用途 |
|---|---|---|---|
| general | 128K | zstd | 汎用ファイル |
| media | 1M | zstd | 大きなファイル（動画・写真） |
| database | 16K | zstd | アプリの DB（logbias=latency） |

### 4.5 マルチプール対応

SSD pool + HDD pool の併用。アプリマニフェストの `pool_hint` でプール選択を自動化。SSD プールがなければ HDD プールにフォールバック。

```
tank/ (HDD pool)
├── shares/
│   ├── photos/
│   ├── documents/
│   └── media/
└── apps/
    └── immich/cache/

fast/ (SSD pool)
└── apps/
    ├── immich/db/
    └── jellyfin/db/
```

### 4.6 special vdev のサポート

メタデータと小ブロックを SSD に逃がすことで、HDD プールの体感速度を大幅に向上させる。HDD + SSD 混在構成では推奨設定として自動提案。

#### config.json での記述

```json
{
  "pools": [
    {
      "name": "tank",
      "topology": {"type": "raidz2", "disks": ["sda","sdb","sdc","sdd"]},
      "special": {
        "type": "mirror",
        "disks": ["nvme0n1","nvme1n1"]
      },
      "small_block_threshold": "32K"
    }
  ]
}
```

#### 重要な制約

- special vdev は喪失するとプール全体が読めなくなる
- そのため**冗長化必須**（mirror、3-way mirror、または raidz）
- KuraOS の UI / config.json バリデーションは、単一 SSD での special vdev 構成を**エラー扱いで拒否**する（warning ではない）
- CLI で `--force-no-redundancy` 等のフラグを付けた場合のみ作成可能

#### small_block_threshold

`recordsize` 未満で、かつこの閾値以下のブロックは special vdev に書かれる。デフォルトは 0（メタデータのみ special に置く）。推奨は 32K〜64K（小さいファイルが SSD に乗るようになる）。

### 4.7 hot spare のサポート

プールに予備ディスクを登録し、故障検出時に自動 resilver を開始する。

#### config.json での記述

```json
{
  "pools": [
    {
      "name": "tank",
      "topology": {"type": "raidz2", "disks": ["sda","sdb","sdc","sdd"]},
      "spares": ["sde", "sdf"]
    }
  ]
}
```

#### 動作

- ZFS の zed (ZFS Event Daemon) が故障を検知し自動的に resilver 開始
- 完了時に Notification Engine 経由で通知
- 障害ディスクの物理交換後、新ディスクを spare として再登録

#### Spare 構造体

```go
type Spare struct {
    Disk   string
    Status string  // available, in-use (resilvering), failed
}
```

#### UI

Storage 画面の各プール詳細に「予備ディスク」セクション。追加・削除・状態確認が可能。

### 4.8 サポート対象外の ZFS 機能

以下は v1 でサポートしない。CLI / 手動設定で利用は可能だが、KuraOS の管理対象外となり、config.json export では捕捉されない。

| 機能 | 理由 |
|------|------|
| SLOG | 自宅 NAS の典型ワークロード（SMB/NFS 非同期書き込み）では効果が薄い。誤設定リスクが高い |
| L2ARC | メモリ追加のほうが有効。ヘッダ管理が ARC を圧迫し、誤設定で性能劣化を招く |
| dedup | メモリ消費が著しく、無効化困難。compression (zstd) で大半のケースに対応可能 |
| ネイティブ暗号化 | 鍵管理・起動時アンロックの UX 設計コストが高い。データの機密性が必要な場合は LUKS によるディスク暗号化を推奨 |

これらの機能を CLI で構成したプールも import 可能だが、KuraOS の UI からはこれらの設定を変更・削除できない。

### 4.9 データ整合性（scrub 管理）

スケジュール設定で定期的に scrub を実行。エラー検出時は通知基盤経由で発報。管理 UI の Storage 画面から手動実行・履歴閲覧が可能。`zpool status -v` の情報をパースして該当ファイルパスも表示。

### 4.10 既存プールのインポート

既存の ZFS ディスク群（ansible-nas からの移行、別サーバーからの移設等）をそのまま取り込む機能。

#### ImportablePool / ImportOpts

```go
type ImportablePool struct {
    Name     string
    GUID     string
    Status   string        // online, degraded, faulted
    Disks    []string
    Datasets []string      // 中に入っているデータセット一覧
}

type ImportOpts struct {
    Force    bool          // 他ホストで最後に export されていない場合
    ReadOnly bool          // まず読み取り専用で import して確認
    AltRoot  string        // 一時的なマウントポイント
}
```

#### UI フロー

```
1. ディスクを物理接続
2. Storage 画面に「インポート可能なプール」が自動表示
3. クリックして確認 → import 実行
4. 既存データセット・スナップショットがそのまま認識される
5. Share 設定を紐付け（自動検出 or 手動）
```

#### Share 自動検出

import したプールのデータセットから Share 候補を提案。`{pool}/apps/*` 配下はアプリ用データセットと推定して候補から除外。

#### CLI

```bash
# 検出
kura storage scan
#  Found: tank (4 disks, 3 datasets, online)

# import
kura storage import tank

# import + Share 自動登録
kura storage import tank --auto-share
```

#### config.json での記述

```json
{
  "pools": [
    {
      "name": "tank",
      "mode": "import",
      "auto_share": true,
      "share_map": {
        "tank/media": {"name": "media", "type": "smb"},
        "tank/documents": {"name": "documents", "type": "smb"},
        "tank/photos": {"name": "photos", "type": "smb"}
      }
    }
  ]
}
```

`"mode": "import"` と `"mode": "create"` で新規作成と区別。`kura init` 時に `zpool import` を実行。

#### Force import

他ホストで最後に使用されたプールの強制インポートはデフォルト OFF。UI で明確に警告を表示し、管理者の明示的な確認を要求。

---

## 5. Share 管理

### 5.1 設計方針

- Share の `path` に Volume 内の任意のサブパスを指定可能（Volume 単位に縛らない）
- smb.conf / exports は直接編集せず、常に SQLite の状態からテンプレート生成
- 共有パスの重複・入れ子はバリデーションで警告

### 5.2 ShareEngine インターフェース

```go
type ShareEngine interface {
    CreateSMBShare(vol *Volume, opts SMBShareOpts) (*Share, error)
    CreateNFSExport(vol *Volume, opts NFSExportOpts) (*Share, error)
    UpdateSMBConfig() error  // smb.confを再生成してreload
}
```

### 5.3 SMB パフォーマンスデフォルト

multichannel、sendfile、AIO、macOS Fruit 互換はデフォルト ON。ユーザーに設定を見せない。管理 UI の「詳細設定」でオーバーライド可能。

```ini
[global]
server multi channel support = yes
aio read size = 1
aio write size = 1
use sendfile = yes
min receivefile size = 16384
socket options = TCP_NODELAY IPTOS_LOWDELAY
vfs objects = catia fruit streams_xattr
fruit:metadata = stream
fruit:model = MacSamba
fruit:posix_rename = yes
fruit:veto_appledouble = no
fruit:nfs_aces = no
fruit:wipe_intentionally_left_blank_rfork = yes
fruit:delete_empty_adfiles = yes
```

### 5.4 NFS デフォルト

NFS 4.2 をデフォルト（server-side copy 対応）。

---

## 6. File API

### 6.1 File API の位置付け

File API は以下を想定して設計する：

- **想定クライアント**: KuraOS が提供する native アプリ（File browser、Memo、写真ビューア等）のみ
- **想定外**: 第三者アプリからの統合、外部 SaaS 連携、サードパーティファイルマネージャからのマウント

このため、以下の機能は意図的にスコープ外：

- WebDAV プロトコル互換性
- ファイルロック機構（DAV LOCK）
- 共有リンク発行
- バージョニング（ZFS スナップショットに委譲）
- コメント・タグ・お気に入り（必要なら個別アプリで実装）
- 全文検索

将来的に第三者統合が必要になった場合は、別エンドポイントとして WebDAV gateway を追加することを検討する（v1 では実装しない）。

### 6.2 性能特性とスコープ

File API は以下の操作に最適化する：

- メタデータ取得・ディレクトリ一覧（高頻度・低レイテンシ）
- 中小ファイル（〜数十 MB）の読み書き
- 部分読み取り（Range request、サムネイル生成等）

以下の用途には**SMB/NFS の直接マウントを推奨**する：

- 大容量ファイル（GB 級）の転送
- メディアストリーミング（動画再生）
- 動画編集等の重い I/O

native アプリ側でも、大容量データを扱う場合は HTTP File API ではなく、コンテナ内に bind mount された Share への直接アクセスを使う（legacy アプリと同じ経路）。

### 6.3 実装上の注意

- Range request 対応は v1 から実装（写真ビューア・動画シーク用）
- TLS 終端は Gateway で実施。HTTP/2 を有効化
- 大容量レスポンスでは `io.Copy` を使用（splice/sendfile が効く場合は活用）
- Content-Length が大きいリクエスト/レスポンスはタイムアウトを別系統に

### 6.4 エンドポイント

```
GET    /api/files/{share}/{path...}?meta=true    # メタデータ取得
GET    /api/files/{share}/{path...}               # ファイル読み出し(ストリーミング)
GET    /api/files/{share}/{path...}?list=true     # ディレクトリ一覧
PUT    /api/files/{share}/{path...}               # ファイル書き込み(ストリーミング)
DELETE /api/files/{share}/{path...}               # 削除
POST   /api/files/{share}/{path...}?op=mkdir      # ディレクトリ作成
POST   /api/files/{share}/{path...}?op=copy&to=   # コピー
POST   /api/files/{share}/{path...}?op=move&to=   # 移動
```

バッチ操作 (`POST /api/files/_batch`) は、native アプリの実装で実際に必要になった時点で追加する。v1 ではクライアント側で逐次叩く。

### 6.5 レスポンス例

`GET /api/files/documents/reports/?list=true`

```json
{
  "path": "/documents/reports/",
  "entries": [
    {
      "name": "2025-q1.pdf",
      "type": "file",
      "size": 2048576,
      "mtime": "2025-03-15T10:30:00Z",
      "permissions": "rw"
    },
    {
      "name": "archive",
      "type": "dir",
      "mtime": "2025-02-01T08:00:00Z",
      "permissions": "r"
    }
  ],
  "total": 2,
  "offset": 0,
  "limit": 100
}
```

### 6.6 認証・認可フロー

Gateway がセッションからユーザーを特定し、内部ヘッダに変換:

```
X-Kura-App: filebrowser
X-Kura-User: tjstkm
X-Kura-Token: (HMAC署名)
```

POSIX ACL の詳細はアプリに見せず、read/write/none の 3 値に簡略化。

### 6.7 アプリごとのアクセススコープ

```json
{
  "app": "filebrowser",
  "scopes": [
    {"share": "*", "access": "readwrite"},
    {"path_deny": ["/secrets/**"]}
  ]
}
```

アプリインストール時にユーザーが許可するモデル（スマホアプリのパーミッションと同じ発想）。

---

## 7. アプリエコシステム

### 7.1 アプリの 2 種類

| 種類 | データアクセス | 用途 |
|---|---|---|
| native | File API 経由 (HTTP) | 自作アプリ。細かい権限制御・監査ログが可能 |
| legacy | bind mount（指定 Share のみ） | 既存 OSS（immich, Jellyfin 等）互換 |

legacy アプリのリスク軽減: read-only をデフォルト、指定 Share のみマウント、uid/gid マッピングをマニフェストで宣言、AppArmor プロファイルでアクセス制限。

### 7.2 マニフェスト仕様

```yaml
apiVersion: v1
name: immich
version: "1.111.0"
type: legacy
display_name:
  ja: "Immich"
  en: "Immich"
description:
  ja: "セルフホスト型の写真・動画管理"
  en: "Self-hosted photo & video management"
homepage: https://immich.app

containers:
  server:
    image: ghcr.io/immich-app/immich-server:v1.111.0
    ports:
      - "3001:3001"
    env:
      DB_URL: "postgres://immich:${db_password}@db:5432/immich"
    depends_on: [db, redis]
  db:
    image: postgres:16
    volumes:
      - type: dataset
        name: immich-db
        mountpoint: /var/lib/postgresql/data
  redis:
    image: redis:7

shares:
  - name: photos
    access: readwrite
    mountpoint: /usr/src/app/upload

storage:
  datasets:
    - name: db
      pool_hint: ssd
      backup: true
      quota: 5G
    - name: cache
      pool_hint: ssd
      backup: false
      quota: 20G

setup:
  required:
    - key: photo_share
      type: share_picker
      label:
        ja: "写真の保存先"
        en: "Photo storage location"
  optional: []

settings:
  - key: db_password
    type: secret
    required: true
  - key: upload_limit
    type: string
    default: "10G"

resources:
  storage: 10G
  memory: 2G

routing:
  mode: path
  base_path_env: IMMICH_BASE_URL
  strip_prefix: true
  port: null
  subdomain: null

auth:
  mode: forward_auth
  header_user: X-Forwarded-User

health:
  endpoint: /api/server/ping
  container: server
  interval: 30s

backup:
  datasets: [db]
  strategy: daily

configs:
  - source: custom-config.yml.tmpl
    mountpoint: /etc/app/config.yml
```

### 7.3 setup.required の型

| 型 | 用途 | 例 |
|---|---|---|
| `share_picker` | Share を選ぶ | 写真の保存先 |
| `select` | 選択肢から選ぶ | トランスコード品質 |
| `toggle` | ON/OFF | GPU アクセラレーション |
| `text` | テキスト入力（最小限に） | 外部公開ドメイン |

大半のアプリは `share_picker` 1 つで済む。DB 接続、Redis、内部ポート等はユーザーに一切見せず、マニフェストから自動解決。

### 7.4 Install フロー

1. マニフェスト署名検証（後述）
2. マニフェストのバリデーション
3. ストレージ準備（dataset 自動作成、プール自動選択）
4. 内部パスワード自動生成（DB password 等）
5. ポート自動割り当て
6. Docker network 作成
7. configs テンプレート展開・注入
8. compose YAML 生成・起動
9. Gateway ルーティング登録
10. OIDC client 自動登録（auth: oidc の場合）
11. ヘルスチェック通過を待って「利用可能」表示

### 7.5 Uninstall

`deleteData=false` がデフォルト。アプリを消してもデータセットは残る。再インストールで復元可能。データのライフサイクルはアプリのライフサイクルと独立。

### 7.6 Update フロー

1. 新マニフェスト取得・署名検証・バリデーション
2. backup.datasets のスナップショット作成（自動ロールバック用）
3. docker compose pull（新イメージ取得）
4. docker compose up -d（更新）
5. ヘルスチェック → OK なら完了 / NG なら旧イメージ + スナップショット復元で自動ロールバック

### 7.7 データセット構造

アプリごとに別データセット。スナップショット粒度、quota 制御、クリーンなアンインストールのため。ZFS dataset は軽量なので数十個作っても性能影響なし。

```
tank/
├── shares/              # ユーザーのShare
│   ├── photos/
│   ├── documents/
│   └── media/
└── apps/                # アプリ用（ユーザーには基本見せない）
    ├── immich/
    │   ├── db/
    │   └── cache/
    ├── jellyfin/
    │   ├── db/
    │   └── transcode/
    └── nextcloud/
        └── db/
```

### 7.8 ルーティング

3 方式をサポート。マニフェストの推奨を管理者がオーバーライド可能。

| 優先順 | 方式 | 用途 |
|---|---|---|
| 1 | path + base_path_env | アプリが base path 対応 |
| 2 | path + strip_prefix | Gateway 側でパス書き換え |
| 3 | port | パスベースで動かないアプリ向け。DNS 不要 |
| 4 | subdomain | DNS 設定済みの環境向け |

ユーザーポータルからクリックするだけでアプリにアクセス。ポート番号や FQDN を意識する必要なし。

### 7.9 アプリレジストリと署名検証

#### レジストリ構造

GitHub Releases / GitHub Pages で以下のディレクトリ構造を公開：

```
registry/
├── registry.json              # アプリ一覧 + 各マニフェストの SHA256
├── registry.json.sig          # cosign 署名
└── apps/{app}/{version}/
    ├── manifest.yaml
    ├── manifest.yaml.sig
    ├── driver.yaml            # ある場合のみ
    └── driver.yaml.sig
```

#### registry.json のスキーマ

```json
{
  "schema_version": "1",
  "updated_at": "2026-04-15T10:00:00Z",
  "apps": {
    "immich": {
      "versions": {
        "1.111.0": {
          "manifest_sha256": "abc123...",
          "driver_sha256": "def456...",
          "released_at": "2026-04-01T00:00:00Z"
        }
      },
      "latest": "1.111.0"
    }
  }
}
```

#### 検証ロジック

```go
type RegistryClient interface {
    FetchRegistry(ctx context.Context) (*Registry, error)
    FetchManifest(ctx context.Context, app, version string) (*Manifest, error)
}

func (c *registryClient) FetchRegistry(ctx context.Context) (*Registry, error) {
    body := c.download("registry.json")
    sig := c.download("registry.json.sig")
    if err := c.verifySignature(body, sig); err != nil {
        return nil, fmt.Errorf("registry signature verification failed: %w", err)
    }
    return parseRegistry(body)
}

func (c *registryClient) FetchManifest(ctx context.Context, app, version string) (*Manifest, error) {
    reg, _ := c.FetchRegistry(ctx)
    expectedHash := reg.Apps[app].Versions[version].ManifestSHA256

    body := c.download(...)
    sig := c.download(...)

    // 1. ハッシュ検証（registry.json と一致するか）
    actualHash := sha256.Sum256(body)
    if hex.EncodeToString(actualHash[:]) != expectedHash {
        return nil, ErrManifestHashMismatch
    }

    // 2. 署名検証（独立した検証）
    if err := c.verifySignature(body, sig); err != nil {
        return nil, fmt.Errorf("manifest signature verification failed: %w", err)
    }

    return parseManifest(body)
}
```

#### 鍵管理: cosign keyless 署名

- 鍵ファイル管理が不要
- KuraOS プロジェクトの GitHub Actions から自動署名
- 検証時は GitHub identity を確認

```bash
# 検証コマンド
cosign verify-blob \
  --certificate-identity-regexp '^https://github.com/kuraos-org/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --signature registry.json.sig \
  registry.json
```

#### 検証失敗時の挙動

| 失敗ケース | 動作 |
|----------|------|
| 署名検証失敗 | アプリインストール / 更新を即停止、エラー表示 |
| ハッシュ不一致 | 同上 |
| ネットワーク経路の改ざん | HTTPS で防御、加えて署名検証で二重チェック |
| バージョンロールバック攻撃 | registry.json の updated_at とローカルキャッシュを比較。古い registry を渡されたら警告（v1.x で対応、v1 では未実装でも可） |

#### サードパーティレジストリへの拡張

将来的に Homebrew tap 方式でサードパーティレジストリ追加を許可する場合：

- ユーザーが追加したレジストリは別の信頼ルートを持つ
- レジストリ追加時に「このレジストリを信頼しますか？」と確認
- 信頼設定は config.json の `app_registries` に明示記録

```json
{
  "app_registries": [
    {
      "name": "official",
      "url": "https://registry.kuraos.org",
      "trust": {
        "type": "cosign_keyless",
        "identity_regex": "^https://github.com/kuraos-org/"
      }
    },
    {
      "name": "community",
      "url": "https://example.com/kura-apps",
      "trust": {
        "type": "cosign_keyless",
        "identity_regex": "^https://github.com/example-org/"
      }
    }
  ]
}
```

ユーザーが明示的に追加しない限り、official 以外のレジストリは有効化されない。

### 7.10 configs テンプレート

マニフェストの `configs` フィールドで、KuraOS の環境情報を使った設定ファイルを自動生成してコンテナに注入。Go template で変数展開（`{{ .KuraOS.MetricsHost }}` 等）。

monitoring-stack（Grafana + Prometheus）のように、インストールした瞬間に NAS ダッシュボードが見える状態にするために活用。

### 7.11 アプリ設定の宣言的管理（ドライバモデル）

#### 方針

アプリ内部の設定も config.json で管理可能にする。ただし**双方向同期ではなく、項目ごとに責務を分ける**。

```
変更方向:
  config.json → アプリ: OK (KuraOS ドライバ経由)
  アプリ UI   → config.json: 自動では吸い上げない

競合が発生した場合:
  項目ごとに定義された drift_policy に従う
```

Terraform の State drift 問題を避けるため、日常的な双方向同期は意図的にサポートしない。

#### v1 のスコープ

ドライバモデルは段階的に実装する：

| バージョン | 対応範囲 |
|----------|---------|
| v1   | `stage: post_install` のみ。インストール直後に 1 回だけ実行する初期化フック |
| v1.5 | `stage: any` の `kuranas_only/overwrite` を追加。継続的な再適用が可能になる |
| v2+  | `scope: user` の初期値提供を追加 |

最初から `stage: any` を入れると drift 管理の複雑さが顕在化するため、v1 では「インストール時の初期化だけ」と割り切る。

#### マニフェストのドライバ定義

```yaml
name: immich
configurable:
  # KuraOS 管轄 (インフラ設定)
  - key: admin_email
    scope: kuraos_only
    stage: post_install         # インストール直後に 1 回だけ
    path: "/api/auth/admin-signup"
    method: POST
    drift_policy: overwrite      # アプリ UI で変えても次の apply で上書き

  - key: storage_quota
    scope: kuraos_only
    stage: any                   # いつでも再適用可能（v1.5+）
    path: "/api/system-config"
    method: PUT
    mapping:
      storageQuota: "${value}"
    drift_policy: overwrite

  # ユーザー管轄 (日常設定、v2+)
  - key: transcoding_policy
    scope: user
    stage: post_install
    drift_policy: preserve       # 初期値のみ適用、以降はアプリ UI に委ねる
    path: "/api/system-config"
    method: PUT
    mapping:
      ffmpeg.preset: "${value.preset}"
      ffmpeg.crf: "${value.crf}"

driver:
  type: http_api
  base_url: "http://server:3001"
  auth:
    type: bearer_token
    token_source: admin_api_key  # KuraOS がインストール時に取得・保管
```

#### scope と drift_policy

| scope | drift_policy | 意味 |
|-------|--------------|------|
| kuraos_only | overwrite | インフラ設定。アプリ UI で変えても config apply で上書き。drift をログ通知 |
| kuraos_only | preserve | インストール時のみ適用。以降は触らない（管理者が明示的に変更する場合のみ再適用） |
| user | preserve | 初期値のみ提供。日常運用はアプリ UI で自由に変更（v2+） |

#### config.json での利用

```json
{
  "apps": [
    {
      "name": "immich",
      "settings": {
        "admin_email": "admin@example.com",
        "storage_quota": "500G",
        "transcoding_policy": {
          "preset": "fast",
          "crf": 23
        }
      }
    }
  ]
}
```

#### ドライバの種類

アプリによって API/設定の流儀が違うため、ドライバ型を差し替え可能にする。

| ドライバ型 | 用途 | 例 |
|-----------|------|-----|
| http_api | REST API 経由 | immich, Nextcloud, Gitea |
| config_file | 設定ファイル書き換え | qBittorrent, Transmission |
| env_only | 環境変数のみ | シンプルなアプリ |
| cli | コンテナ内コマンド実行 | DB マイグレーション等 |

```yaml
# config_file ドライバの例
driver:
  type: config_file
  path: /config/qBittorrent/qBittorrent.conf
  format: ini
  reload: signal:USR1
```

#### 初期スコープ

v1 で `kuraos_only/post_install` の項目を保守的に絞る:

- 管理者メール / 初期認証情報
- バックアップ対象の指定
- 監視メトリクスの出力先

ストレージ割り当て (quota) のような継続的に変更が必要な項目は v1.5 で `stage: any` 対応時に有効化する。

#### やらないこと

- **アプリ UI の読み取り専用化**: アプリ側がそれを想定していないため実装不可能
- **アプリ UI 変更の自動吸い上げ**: State drift 地獄になる
- **双方向の継続的同期**: 責任範囲が曖昧になり、サポート困難

#### ドライバの配布

アプリマニフェストと同じく GitHub レジストリで配布。署名検証も同じ仕組みを通る（§7.9 参照）。KuraOS プロジェクト側で主要アプリのドライバをメンテ。ユーザーは config.json を書くだけで完全な環境を宣言的に復元できる。

---

## 8. 認証基盤

### 8.1 構成

KuraOS 自体が OIDC Provider として動作。アプリは常に KuraOS の OIDC Provider とだけ通信する。

#### 採用ライブラリ: zitadel/oidc

Go の OIDC Provider 実装として `github.com/zitadel/oidc/v3` を採用。

- OpenID Certified 取得済み
- ライブラリとして組み込む設計（フレームワーク化されていない）
- `op` パッケージで Provider 実装、`Storage` インターフェースで永続層を選択
- KuraOS では SQLite-backed Storage を実装

### 8.2 認証基盤のスコープ前提

KuraOS の OIDC Provider は以下を前提に設計する：

- **想定アクセス**: 自宅 LAN または Tailscale 等の VPN 経由
- **想定 Relying Party**: KuraOS が動的登録する内部アプリのみ（immich、Jellyfin 等の OIDC 対応アプリ）
- **想定外**: 外部公開された第三者サービスからの SSO 連携、企業向けの SAML 連携、エンタープライズ向け要件

この前提により、初期実装では以下を省略する：

- MFA（必要なら外部 IdP federation で Google 等に委譲）
- デバイスフロー（CLI ログイン用。後から追加可能）
- Dynamic Client Registration の RFC 7591 準拠 API（内部用は KuraOS が直接 SQLite に書き込めばよい）
- Consent screen の細かいスコープ管理
- パスキー / WebAuthn

これらは zitadel/oidc が機能として持っていても、KuraOS の UI とバックエンドで対応する必要があるため、初期スコープから外す。

### 8.3 外部 IdP フェデレーション

Google, GitHub, Microsoft 等の外部 IdP とフェデレーション可能。ユーザーがどの認証方法を使ったかはアプリ側に関係ない。MFA が必要な場合は外部 IdP に委譲する（Google Workspace 等の MFA 設定をそのまま利用）。

### 8.4 アプリとの連携

| auth mode | 用途 |
|---|---|
| `oidc` | OIDC 対応アプリ。client_id/secret を自動生成・注入 |
| `forward_auth` | OIDC 非対応アプリ。Gateway が認証を代行 |
| `none` | 認証不要（公開ダッシュボード等） |

### 8.5 ユーザーモデル

```go
type User struct {
    ID          string
    Name        string
    Role        Role           // admin | user
    AuthMethods []AuthMethod   // 複数の認証方法を紐付け可能
}

type AuthMethod struct {
    Type    string   // local, google, github
    Subject string   // local: username, google: email
}
```

1 ユーザーに複数の認証方法を紐付け可能。管理者が事前に紐付け（デフォルト）。`auto_provision: true` で初回認証時の自動ユーザー作成も可能（デフォルト OFF）。

### 8.6 OIDC エンドポイント

```
/oidc/.well-known/openid-configuration
/oidc/authorize
/oidc/token
/oidc/userinfo
/oidc/jwks
/oidc/end_session
```

Gateway がこれらをルーティングし、auth/oidc モジュールが処理。

---

## 9. 管理 UI

### 9.1 画面構成

左サイドバー固定。階層メニューにしない。

| 画面 | 役割 |
|---|---|
| Dashboard | 全体の健康状態。プール容量、CPU/メモリ、Share 一覧、アプリ状態 |
| Storage | プール・ディスク・スナップショット管理。温度推移・SMART 履歴 |
| Shares | 共有の作成・権限設定。用途プリセット選択 |
| Users | ユーザー・グループ管理。認証方法の紐付け |
| Network | IP・DNS・ボンディング設定 |
| Apps | インストール済みアプリ管理 + アプリストア |
| Settings | システム設定・通知・バックアップ・TLS・更新・config export/import・ログビューア・言語設定 |

### 9.2 アプリストア

ワンクリックインストール体験。設定項目は最小限（大半は share_picker 1 つ）。カテゴリでブラウズ可能。

### 9.3 アプリ公開制御

管理者がインストール → ユーザー/グループに公開設定。ユーザーが自分でインストールするのではなく、管理者が管理するモデル。

---

## 10. ユーザーポータル

管理者でないユーザー向けのランチャー画面（`/ui/*`）。

**見えるもの**: 使えるアプリ一覧、Files（ビルトイン native ファイルブラウザ）、最近のファイル、自分のストレージ使用量

**見えないもの**: プール管理、ディスク状態、Docker、ネットワーク設定、他ユーザーの情報

---

## 11. モニタリング

### 11.1 方針

ビルトインは自前の軽量実装。本格運用は monitoring-stack アプリ（Grafana + Prometheus）に委譲。KuraOS 自体は `/metrics` エンドポイントを OpenMetrics 形式で公開することだけを保証。

### 11.2 収集メトリクス

- **Storage**: プール使用量/空き/fragmentation、データセット使用量/quota、ディスク SMART/温度/I/O、scrub 状態
- **System**: CPU 使用率/load average、メモリ used/available/swap、ネットワーク tx/rx/errors、uptime
- **Apps**: コンテナ CPU%/memory/network I/O、ヘルスチェック状態、再起動回数

### 11.3 メトリクスストア（バイナリ Ring Buffer）

固定サイズのバイナリファイル。古いデータは自動的に上書き（削除処理不要）。

```
metrics/{metric_name}/
  raw.bin    ← 直近24h, 30秒間隔, 2880エントリ (45KB)
  5min.bin   ← 直近7日, 5分間隔, 2016エントリ (32KB)
  1hour.bin  ← 直近90日, 1時間間隔, 2160エントリ (34KB)

1エントリ = 16 bytes (timestamp int64 + value float64)
50メトリクス × 3ファイル ≈ 5.5MB
```

### 11.4 アラートルール

config.json でメトリクスに対する閾値を定義。超過時に Event Bus にイベント発行→通知チャネルへ。

### 11.5 monitoring-stack アプリ

Grafana + Prometheus をワンクリックインストール。configs テンプレートで scrape 先・datasource・NAS 専用ダッシュボードを自動設定。設定項目ゼロ。

---

## 12. ログ管理

### 12.1 ストア

JSONL ファイル。日次ローテーション。retention 超過で自動削除。

```
logs/
  2026-03-31.jsonl
  2026-03-30.jsonl
  ...
```

### 12.2 ソース

KuraOS 自身のログ（直接書き込み）、Docker コンテナのログ（goroutine で読み取り）、systemd journal。

### 12.3 管理 UI のログビューア

Source / Level / テキスト検索でフィルタ。「ライブ」モードで SSE リアルタイムストリーミング。

---

## 13. 通知基盤

### 13.1 Event Bus

各 Engine がイベントを発行し、NotificationEngine がルーティング。Go の channel で実装。

```go
type Event struct {
    ID        string
    Severity  Severity    // critical, warning, info
    Category  string      // storage, app, backup, system, auth
    Title     string
    Body      string
    Timestamp time.Time
    Meta      map[string]string
}
```

### 13.2 通知イベント

**Critical**: ディスク障害、プール degraded/faulted、バックアップ連続失敗、KuraOS 異常終了

**Warning**: SMART 値劣化傾向、プール容量 80% 超過、scrub エラー（修復済み）、アプリ crash loop、証明書期限接近

**Info**: scrub 完了、スナップショット作成完了、アプリ更新完了、KuraOS 更新利用可能

### 13.3 通知チャネル

| タイプ | 用途 |
|---|---|
| `ntfy` | プッシュ通知（Android/iOS 対応）。家庭用に最適 |
| `webhook` | Discord, Slack, Teams 等の汎用 webhook |
| `line_notify` | LINE 通知 |
| `smtp` | メール通知 |
| `gotify` | セルフホスト通知 |

チャネルごとに `events`（severity フィルタ）と `categories`（カテゴリフィルタ）で通知対象を制御。

### 13.4 イベントログ

全イベントは SQLite の `event_log` テーブルに保存（通知設定に関係なく記録）。管理 UI で検索・フィルタ・既読管理。

### 13.5 テスト通知

管理画面でチャネル設定後に「テスト送信」ボタン。障害時に「通知設定が壊れていた」を防ぐ。

---

## 14. バックアップ・リストア

### 14.1 ローカル（ZFS スナップショット）

cron スケジュールで定期実行。retention policy で自動管理（hourly/daily/monthly）。

### 14.2 オフサイト（リモートバックアップ）

```go
type BackupBackend interface {
    Send(ctx context.Context, dataset string, snapshot string, opts SendOpts) error
    Restore(ctx context.Context, dataset string, snapshot string) error
    List(ctx context.Context) ([]RemoteSnapshot, error)
}
```

| バックエンド | 用途 |
|---|---|
| `zfs_send` | NAS 同士のレプリケーション |
| `restic` | S3/B2 等のオブジェクトストレージ |
| `rclone` | Google Drive 等の汎用リモート |

### 14.3 リモートバックアップ先の信頼前提

KuraOS は dataset レベルの暗号化をサポートしない。リモートバックアップを利用する際は以下の前提で運用する：

- **zfs_send**: バックアップ先が信頼できる環境（自宅内別 NAS、家族・友人の自宅 NAS）に限定する。データはネットワーク上 SSH で暗号化されるが、バックアップ先のディスク上では平文で保存される
- **restic**: クライアントサイド暗号化があるため、信頼できない先（B2、S3、他社クラウド）にも安全にバックアップ可能
- **rclone**: バックエンドの暗号化機能（rclone crypt）を併用すれば信頼できない先にも対応可能

信頼できない先にデータを送る場合は restic か rclone+crypt の利用を推奨する。

### 14.4 フルリストア

1. 新しい KuraOS をインストール
2. `config.json` を apply（構造が全部再現される）
3. リモートから zfs receive または restic restore（データ復元）
4. アプリのコンテナを pull & 起動 → 完全復元

---

## 15. TLS 証明書管理

### 15.1 モード

| mode | 用途 |
|---|---|
| `self_signed` | LAN 内利用（デフォルト）。ローカル CA を生成、ブラウザへのインストール案内 |
| `acme` | 外部公開時。Let's Encrypt / ZeroSSL。DNS-01 チャレンジ対応 |
| `manual` | 独自証明書をアップロード |

### 15.2 ACME 実装

ACME クライアントは [`go-acme/lego`](https://github.com/go-acme/lego) を採用。DNS プロバイダはコード内モジュールとして実装し、プロバイダ追加を容易にする：

- v1: Cloudflare DNS のみ
- v1.x で需要に応じて追加（Route 53、Sakura DNS、お名前.com 等）

#### 設計方針

外部プラグインローディング（Go plugin パッケージや別プロセス）は採用しない。プロバイダ追加は KuraOS のコード変更とリビルドが必要。ただし**追加コストは最小限**になるよう、内部モジュール構造を整える。

#### モジュール構造

```
engine/network/acme/
├── acme.go                 # ACME クライアント本体（lego ラッパー）
├── provider.go             # DNSProvider interface 定義
├── registry.go             # プロバイダ登録の中央レジストリ
└── providers/
    ├── init.go             # blank import で全プロバイダの init() を発火
    ├── cloudflare.go       # v1 で実装
    └── (将来: route53.go, sakura.go, ...)
```

#### インターフェース

```go
type DNSProvider interface {
    Name() string
    DisplayName() string
    Fields() []ProviderField
    Validate(creds map[string]string) error
    NewLegoProvider(creds map[string]string) (challenge.Provider, error)
}

type ProviderField struct {
    Key      string
    Label    string
    Type     string  // "secret" | "text"
    Required bool
    Help     string
}
```

#### 中央レジストリ

```go
var providers = map[string]DNSProvider{}

func Register(p DNSProvider) {
    providers[p.Name()] = p
}

func Get(name string) (DNSProvider, error) {
    p, ok := providers[name]
    if !ok {
        return nil, fmt.Errorf("dns provider %q not supported", name)
    }
    return p, nil
}

func List() []DNSProvider {
    result := make([]DNSProvider, 0, len(providers))
    for _, p := range providers {
        result = append(result, p)
    }
    return result
}
```

#### Cloudflare 実装例

```go
type CloudflareProvider struct{}

func (p *CloudflareProvider) Name() string        { return "cloudflare" }
func (p *CloudflareProvider) DisplayName() string { return "Cloudflare" }

func (p *CloudflareProvider) Fields() []acme.ProviderField {
    return []acme.ProviderField{
        {
            Key:      "CLOUDFLARE_DNS_API_TOKEN",
            Label:    "API Token",
            Type:     "secret",
            Required: true,
            Help:     "Zone:DNS:Edit 権限のトークン",
        },
    }
}

func (p *CloudflareProvider) Validate(creds map[string]string) error {
    if creds["CLOUDFLARE_DNS_API_TOKEN"] == "" {
        return errors.New("CLOUDFLARE_DNS_API_TOKEN is required")
    }
    return nil
}

func (p *CloudflareProvider) NewLegoProvider(creds map[string]string) (challenge.Provider, error) {
    cfg := cloudflare.NewDefaultConfig()
    cfg.AuthToken = creds["CLOUDFLARE_DNS_API_TOKEN"]
    return cloudflare.NewDNSProviderConfig(cfg)
}

func init() {
    acme.Register(&CloudflareProvider{})
}
```

#### 新プロバイダ追加の手順

1. `providers/{name}.go` を作成し DNSProvider interface を実装
2. `init()` で `acme.Register(&XxxProvider{})` を呼ぶ
3. ビルド & リリース

UI は `acme.List()` で動的にプロバイダ一覧を取得するため、UI 側のコード変更は不要。

#### lego が対応していないプロバイダ

将来、lego が対応していない国内プロバイダ（さくら、お名前.com で独自 API を使う場合等）は、KuraOS 側で `challenge.Provider` interface を直接実装する。lego ラッパーである必要はない。

#### config.json での記述

```json
{
  "tls": {
    "mode": "acme",
    "acme": {
      "email": "admin@example.com",
      "domains": ["nas.example.com"],
      "challenge": {
        "type": "dns-01",
        "provider": "cloudflare",
        "credentials_env": {
          "CLOUDFLARE_DNS_API_TOKEN": "CF_TOKEN_FROM_ENV"
        }
      },
      "directory_url": "https://acme-v02.api.letsencrypt.org/directory"
    }
  }
}
```

---

## 16. 外部アクセス

| mode | 用途 |
|---|---|
| `tailscale` | 最も簡単。auth key を入れるだけ。Funnel での公開もオプション |
| `cloudflare_tunnel` | FQDN での公開向け |
| `wireguard` | 上級者向け |

---

## 17. アップデート

### 17.1 kura バイナリの更新

単一バイナリのアトミック置き換え + systemd restart。

1. GitHub Releases から新バイナリダウンロード
2. SHA256 + cosign 署名検証
3. `kura.bak` ← 現行バイナリをバックアップ
4. `kura.new` → `kura` にアトミックリネーム
5. `systemctl restart kura`
6. 起動後セルフチェック、失敗なら旧バイナリに自動ロールバック

`auto_apply` はパッチバージョン（v0.1.x）のみ。マイナー/メジャーは手動確認。

### 17.2 ベース OS パッケージの更新

#### 方針

```
セキュリティパッチ  → unattended-upgrades で自動適用
その他のパッケージ → 管理 UI から手動実行
カーネル更新      → 管理 UI から手動、再起動タイミングはユーザーが選択
```

#### 更新前のスナップショット作成

apt upgrade 実行前に、整合性のある状態で ZFS スナップショットを自動作成する。

##### スナップショット対象

pre-upgrade スナップショットは以下のデータセットに対して**同時に**作成する：

- rootfs（OS パッケージとバイナリ）
- アプリの DB データセット（マニフェストの `backup: true` がついたもの）

ユーザーの Share データセット（写真、動画、ドキュメント等）は対象外。これらは apt upgrade で破壊される性質のものではないため。

##### スナップショット手順

```
1. 全アプリのコンテナを graceful stop（DB 等の整合性確保のため）
2. 対象データセットすべてに同名スナップショットを作成
   例: rpool/ROOT/ubuntu@pre-upgrade-20260401-1530
       tank/apps/immich/db@pre-upgrade-20260401-1530
       tank/apps/jellyfin/db@pre-upgrade-20260401-1530
3. apt upgrade 実行
4. アプリのコンテナ起動
5. 結果表示（成功/失敗、再起動要否）
```

#### ロールバックは UI から提供しない（v1）

v1 では管理 UI 経由のワンクリック自動ロールバックは**実装しない**。理由：

- 整合性のとれたロールバックには graceful stop の順序、依存関係解析、失敗時の中途半端な状態の処理など、エッジケースが膨大
- 管理 UI 経由の自動ロールバックという「便利な落とし穴」を作るより、安全側に倒す
- Phase 2 で A/B パーティションが入れば、この問題は構造的に解決する。Phase 1 で頑張って実装したコードは捨てる可能性が高い

```
✓ pre-upgrade スナップショット自動作成（rootfs + アプリ DB、graceful stop 込み）
✓ スナップショット一覧の閲覧 UI
✗ ワンクリック自動ロールバック UI ← 実装しない
→ 復旧は CLI で手動。手順書を README に明記
```

#### 手動復旧手順（README に記載）

```bash
# 1. KuraOS と全アプリを停止
systemctl stop kura
docker compose -f /var/lib/kura/compose/*.yml down

# 2. 対象データセットを rollback
zfs rollback rpool/ROOT/ubuntu@pre-upgrade-20260401-1530
zfs rollback tank/apps/immich/db@pre-upgrade-20260401-1530
# (他のアプリも同様)

# 3. 再起動
reboot
```

#### スコープ外の注意

スナップショット作成時刻 (T0) からロールバック実行時刻 (T2) までの間にアプリに発生したデータ変更（例: 新しい写真のアップロード）は失われる。ファイル本体は Share に残るが、アプリ DB からは見えなくなり、再スキャンが必要になる。

#### パッケージのピン留め

kura が依存する重要パッケージのメジャーバージョンをピン留め。意図しない破壊的変更を防ぐ:

```
# /etc/apt/preferences.d/kura
Package: zfs-dkms
Pin: version 2.2.*
Pin-Priority: 990

Package: samba
Pin: version 4.19.*
Pin-Priority: 990

Package: docker-ce
Pin: version 5:26.*
Pin-Priority: 990
```

マイナー/パッチ更新は通すが、メジャーバージョンアップは kura のリリースに合わせてピンを更新。

#### 再起動管理

カーネル更新後の再起動はユーザーに委ねる。NAS は常時稼働なので勝手に再起動しない。

```yaml
system_update:
  security_auto: true
  reboot:
    mode: manual | scheduled
    schedule: "0 4 * * 0"    # scheduled 時: 毎週日曜 4 時
```

再起動が必要なときは Dashboard に常時バナー表示:

```
⚠ カーネル更新済み。再起動が必要です。 [ 今すぐ再起動 ] [ スケジュール ]
```

#### Phase 2 への展望

Phase 1 の apt ベースの更新管理が運用上辛くなったタイミングで Phase 2 に移行:

```
Phase 1: Ubuntu + apt + ZFS snapshot でベストエフォート、ロールバックは手動
Phase 2: A/B パーティション or NixOS でアトミック更新 + ワンクリックロールバック
```

---

## 18. 初期セットアップ

### 18.1 ウィザード（UI）

初回ブラウザアクセス時に自動起動:

1. 管理者アカウント作成（ローカル + 任意で Google 連携）
2. ストレージ構成（ディスク検出 → 推奨構成を自動提案）
3. 最初の Share 作成（プリセットから選択）
4. 完了 → Dashboard 表示

### 18.2 推奨構成ロジック

| ディスク構成 | 推奨 |
|---|---|
| HDD 2 本 | mirror |
| HDD 3 本 | RAIDZ1 |
| HDD 4-5 本 | RAIDZ1 or RAIDZ2（選択） |
| HDD 6 本以上 | RAIDZ2 |
| SSD 単体 | single（アプリ用プール） |
| SSD 2 本以上 | mirror（アプリ用プール） |
| HDD + SSD 1本 | HDD = RAIDZ、SSD = アプリ用 single プール（special vdev は冗長必須のため不可） |
| HDD + SSD 2本 | 選択肢を提示:<br>(a) HDD = RAIDZ + SSD 2本を special vdev mirror（推奨）<br>(b) HDD = RAIDZ + SSD 2本を別プール (mirror) |
| HDD + SSD 3本以上 | HDD = RAIDZ + SSD 2本を special vdev、残り SSD はアプリ用プール |

#### special vdev か別プールかの判断

UI で以下を提示してユーザーに選ばせる：

- **special vdev mirror（推奨）**: HDD プールの体感速度が大幅向上。ただし special vdev 喪失でプール全体喪失のリスク（mirror で軽減）
- **別 SSD pool**: SSD と HDD を完全分離。アプリのデータは SSD pool、メディアは HDD pool。special vdev のリスクは無し

### 18.3 コンフィグ流し込み（ヘッドレス）

```bash
# ファイル配置
cp config.json /etc/kura/config.json
systemctl start kura

# CLI
kura init --config config.json

# stdin（自動化向け）
curl -s https://my-configs/nas.json | kura init --config -

# 管理者パスワード（平文を避ける）
kura init --config config.json --admin-password-env KURA_ADMIN_PASSWORD
```

起動時に `/etc/kura/config.json` が存在すればウィザードをスキップ。

---

## 19. 電源管理

UPS 連携（NUT）、ディスクスピンダウン、シャットダウン/再起動の UI。UPS のバッテリー残量が閾値を下回った場合、安全にシャットダウン。

---

## 20. プラットフォーム API 全体像

KuraOS の native アプリ（KuraOS プロジェクトが提供するアプリ）から使える API。第三者アプリからの統合は想定しない。

legacy アプリ（既存 OSS の Docker コンテナ）は bind mount でファイルアクセスし、これらの API は使わない。

| API | エンドポイント | 用途 |
|---|---|---|
| File API | `GET/PUT/DELETE /api/files/{share}/{path}` | ファイル CRUD・メタデータ・一覧 |
| Storage API | `GET /api/storage/pools`, `/usage` | プール情報、アプリの使用量 |
| User API | `GET /api/users/me`, `/me/shares` | 現在のユーザー情報、アクセス可能な Share |
| Notification API | `POST /api/notify` | アプリから通知を発行 |
| Settings API | `GET/PUT /api/apps/me/settings` | 自アプリの設定取得・更新 |
| Auth API | `GET /api/auth/userinfo` | OIDC ユーザー情報 |

アプリはこの API だけ知っていれば KuraOS の内部実装（ZFS、Docker、SQLite）は見えない。SDK は需要に応じて後から提供。

---

## 21. データストア使い分け

| データ種別 | ストア | 理由 |
|---|---|---|
| 設定 | SQLite | トランザクション、JOIN |
| イベントログ | SQLite | 既読管理、低頻度書き込み |
| OIDC データ（authorize code、refresh token 等） | SQLite | 短期保存、トランザクション必須 |
| メトリクス | バイナリ Ring Buffer | 高頻度、固定サイズ、削除不要 |
| アプリログ | JSONL ファイル | 高頻度、テキスト検索、日次ローテーション |
| ロケールファイル | embed.FS | バイナリ埋め込み、起動時読み込み |