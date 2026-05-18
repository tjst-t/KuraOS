# KuraOS リリースノート

## Phase 1 完成 (S99702c — 2026-05-18)

KuraOS Phase 1 の全 18 スプリントが完了。Go 単一バイナリ `kura` として出荷。

### 新機能 (S99702c)

- **ユーザーポータル** (`/ui`): 非管理者向けランディングページ。インストール済みアプリのタイル表示、ファイルショートカット、ストレージ使用量・共有数統計
- **セットアップウィザード (Steps 2-5)**: 初回ウィザードが完全 6 ステップに拡張。ストレージ推奨・共有作成を UI で完結できる
- **`kura init` CLI**: HTTP サーバーを起動せずに管理者アカウントを作成するヘッドレスコマンド。CI / 無人インストールに対応
- **設定言語タブ**: 設定画面に言語タブ追加。v1 は日本語のみ対応 (英語は v1.x バッジで将来予告)
- **config.json エクスポート / インポート UI**: 設定タブからワンクリックでバックアップ・復元。ZFS プール構成・共有・アプリの宣言的状態を config.json として保存/適用

### 既知の制限 (Phase 2 で解決予定)

- 英語 UI は未対応 (v1.x)
- OS ロールバック UI は未実装 (手動 CLI で復旧)
- アトミック OS 更新は未実装 (apt + ZFS スナップショットのベストエフォート)
- config.json インポート時の ZFS / smbd コマンドはテスト環境では失敗する (実 VM が必要)

---

## S8a756d — 監視・通知・メトリクス (2026-04)

- Ring Buffer メトリクス・EventBus
- Slack / Discord / Webhook 通知チャンネル
- OpenMetrics エンドポイント (`/metrics`)
- ログビューア・TLS 管理・ネットワーク設定・自己更新

## S0eedaa — File API・ファイルブラウザ (2026-03)

- ブラウザファイルマネージャ (`/ui/files`)
- File API (`/api/files/*`, X-Kura-Token 認証)

## S822961 — OIDC OP・Google フェデレーション (2026-02)

- KuraOS 内蔵 OpenID Provider
- Google OAuth2 フェデレーション
- 保留中ユーザー承認フロー

## Se1e7a6 — バックアップ (2026-01)

- ZFS スナップショット管理
- スナップショットローテーション・復元

## S65b510 — アプリエンジン (2025-12)

- Docker アプリのインストール・更新・アンインストール
- アプリルーティング (`/apps/<name>/`)

## S464e47 — config.json 基盤 (2025-11)

- config.json スキーマ・SSOT 設計
- ApplyAdapter / ExportAdapter 登録

## S1e7eeb — 初回セットアップ Step 1・認証基盤 (2025-10)

- 管理者アカウント作成ウィザード Step 1
- セッション認証・ロールベースアクセス制御

## 以前のスプリント

Storage / Share / Auth / Monitor / System / Network 各 Engine の実装。
詳細は `docs/sprint-logs/{SprintID}/` を参照。
