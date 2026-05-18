# KuraOS

Linux ベースの自作 NAS OS。Go 単一バイナリ `kura` がルーティング・API・Engine を統合した管理スタックを提供する。

## Phase 1 リリース

Phase 1 (18 スプリント、2025〜2026) が完成。以下の機能セットが含まれる:

| カテゴリ | 内容 |
|---|---|
| **ストレージ** | ZFS プール作成・ボリューム管理・推奨トポロジー自動提案 |
| **共有** | SMB / NFS 共有管理・ACL (ユーザー / グループ)・プリセット |
| **アプリ** | Docker アプリのインストール・更新・アンインストール・ルーティング |
| **認証** | パスワード認証・Google OAuth2 フェデレーション・OIDC OP 内蔵 |
| **監視** | Ring Buffer メトリクス・イベントログ・OpenMetrics エンドポイント |
| **通知** | Slack / Discord / Webhook チャンネル・重大度フィルター |
| **バックアップ** | ZFS スナップショット・ローテーション・復元 |
| **ネットワーク** | Netplan 連携・TLS (自己署名 / Let's Encrypt) |
| **ログ** | JSONL ログビューア・ローテーション |
| **自己更新** | kura バイナリのアトミック更新・ロールバック |
| **ファイル** | ブラウザファイルマネージャ・File API (HMAC 認証) |
| **セットアップ** | 6 ステップ初回ウィザード・`kura init` ヘッドレス CLI |
| **ユーザーポータル** | インストール済みアプリ一覧・ファイルショートカット |
| **設定** | config.json エクスポート / インポート・言語タブ |

## 動作環境

- Ubuntu 22.04+ (ansible-nas 上で検証済み)
- ZFS / Docker / Samba が apt でインストール済みの状態
- Go は不要 (リリースバイナリは CGO_ENABLED=0 でビルド済み)

## クイックスタート

```bash
# 1. バイナリをダウンロード (または手元でビルド)
CGO_ENABLED=0 go build -o kura ./cmd/kura

# 2. 初回管理者アカウントを作成 (HTTP サーバーを起動せずに実行可)
KURA_STATE_DB=/var/lib/kura/state.db ./kura init \
  --admin-user=admin \
  --admin-password=mysecretpassword \
  --display-name="Admin"

# 3. サーバーを起動
KURA_PORT=8204 KURA_STATE_DB=/var/lib/kura/state.db ./kura

# 4. ブラウザで http://<host>:8204 を開いてセットアップウィザードへ
```

## コマンド一覧

| コマンド | 説明 |
|---|---|
| `kura` (引数なし) | HTTP サーバーを起動 |
| `kura init --admin-user=X --admin-password=Y` | ヘッドレス初回セットアップ |
| `kura config export` | 現在の設定を config.json として stdout に出力 |
| `kura config apply [--dry-run] file.json` | config.json を適用 |
| `kura storage list-pools` | ZFS プール一覧 |
| `kura share list` | 共有一覧 |
| `kura app list` | インストール済みアプリ一覧 |
| `kura user list` | ユーザー一覧 |
| `kura version` | バージョン表示 |

## 設定の SSOT

設定の正規文書は `config.json` (デフォルト: `KURA_CONFIG_PATH`)。SQLite は UI / Engine のランタイムキャッシュ。

エクスポートとインポートでプール / 共有 / アプリ / 認証が完全再現される:

```bash
# 現在の状態をエクスポート
kura config export > config.json

# 別マシンに適用
kura config apply config.json
```

## 開発

```bash
make build    # bin/kura をビルド
make test     # go test ./...
make e2e      # Playwright E2E (KURA_BASE_URL=http://192.168.1.42:8204)
make lint     # gofmt + go vet
make serve    # 開発サーバー起動
make stop     # 開発サーバー停止
```

詳細は [CLAUDE.md](CLAUDE.md) を参照。

## Phase 2 への移行

Phase 1 の `kura` バイナリ・Engine・config.json スキーマは Phase 2 でもそのまま使用される。
Phase 2 で変わるのはベース OS 管理層 (apt → NixOS or mkosi) のみ。
