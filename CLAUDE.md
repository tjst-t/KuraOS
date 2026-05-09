# KuraOS

> Linux ベースの自作 NAS OS。Go 単一バイナリ `kura` がルーティング・API・Engine を統合した管理スタックを提供する。

## Tech Stack

Go (単一バイナリ・モジュラーモノリス) / htmx + Tailwind CSS / SQLite / ZFS CLI / Docker API / zitadel/oidc / go-acme/lego。i18n は自前実装で v1 は `ja` のみ。Phase 1 は ansible-nas 上で動作させる前提。

## Commands

- `make serve` — `kura` をバックグラウンド起動 (portman で port 確保)
- `make stop` — バックグラウンド `kura` を停止
- `make build` — `kura` バイナリをビルド (`bin/kura`)
- `make test` — `go test ./...`
- `make lint` — `gofmt -l` + `go vet`
- `make tidy` — `go mod tidy`

## Development Rules

- **設定の SSOT は `config.json`**。SQLite はランタイム状態のキャッシュ。新機能を追加するときは config.json スキーマ → import/export/diff/apply の挙動を先に設計する
- **ハードコードされたユーザー向け文字列を残さない**。必ず `i18n.T(MessageID, args...)` 経由で出力。MessageID は定数化して集中管理
- **副作用を持つ依存は interface 経由**。ZFS / Docker / SMB / lego / 外部 IdP は `CmdExecutor`, `DockerClient`, `DNSProvider` 等の interface 越しに使い、テストでモック化
- **エラーは `fmt.Errorf("...: %w", err)` で wrap**。エラーメッセージは開発者向け英語のまま。ユーザー表示は i18n 経由
- **context.Context を全 IO 関数の第 1 引数で渡す**
- **テーブルドリブンテスト + `t.Run` でケース名**を基本とする
- **JSON tag は snake_case、Go フィールドは CamelCase**
- **コメントは「なぜ」だけ書く**。「何を」は型と関数名で表現する。基本コメント無し
- **外部プラグインローディングは採用しない** (Go plugin / 別プロセス)。DNS プロバイダ等はコード内モジュールで `registry.Register` に登録
- **破壊的操作 (zpool destroy 等) は確認なしで実行しない**。バリデーション失敗 (例: single SSD で special vdev) は warning ではなく error で拒否
- **アプリの内部秘密 (DB password, OIDC client secret) を config.json export 時に平文で含めない**。env 参照のみ記録

## Server

- `make serve` は portman で port を確保し、`bin/kura` をバックグラウンド起動する
- 再実行で前回プロセスを自動 kill (PID file `.kura.pid`)
- ログ: `/tmp/kuraos-dev.log`
- ポート番号をコード内にハードコードしない (環境変数 `KURA_PORT` 経由、portman が割当)

## Test Environment

ZFS / SMB / Docker / systemd / lego ACME など実 OS 機能を伴うテストは Ubuntu 検証 VM で実行する:

- **Host**: `192.168.1.42` (LAN 内)
- **SSH**: `ssh ubuntu@192.168.1.42` (sudo パスワード無し)
- **用途**: Engine 層の実環境テスト (ZFS pool 作成、smbd reload、docker compose 起動、apt upgrade, snapshot rollback など)
- **ローカル CI**: `go test ./...` は CmdExecutor / DockerClient / DNSProvider などのモックで完結する単体テストのみを対象とし、実 OS が必要なテストは VM 側で実行する

### 動作確認は必ず VM にデプロイして行う

- ローカルの `go test` / `make build` 通過は最低条件、それだけで「動作確認済み」と報告しない
- 「動作確認」「デモ」「実機検証」を伴うタスクは VM 192.168.1.42 にバイナリをデプロイして実機で再現すること
- **VM 上の既存 `kura` プロセスは無断で kill して新バイナリに差し替えて良い** (dev VM・共有データ無し前提)。事前確認不要、ただし state DB ファイル `/home/ubuntu/kuraos/state.db` は消さない (admin / 設定が消える)
- 本番 NAS データを VM に持ち込まない

### Deploy recipe

VM には Go / make が入っていない。ローカルでビルドしてバイナリだけ送る:

```
CGO_ENABLED=0 go build -o bin/kura ./cmd/kura
rsync -az bin/kura ubuntu@192.168.1.42:/home/ubuntu/kuraos/kura.new
ssh ubuntu@192.168.1.42 'sudo pkill -x kura; sleep 2; \
  sudo mv /home/ubuntu/kuraos/kura.new /home/ubuntu/kuraos/kura && \
  sudo chmod +x /home/ubuntu/kuraos/kura && \
  cd /home/ubuntu/kuraos && \
  sudo nohup env KURA_PORT=8204 KURA_STATE_DB=/home/ubuntu/kuraos/state.db \
    /home/ubuntu/kuraos/kura > /home/ubuntu/kuraos/kura.log 2>&1 </dev/null & \
  disown'
```

確認は `curl -s http://192.168.1.42:8204/healthz` の `started_at` が直近のデプロイ時刻と一致すること。

## References

詳細は必要なときだけ読む:

- 製品ビジョンと Out-of-scope: `docs/VISION.json`
- 設計原則 (優先順位 / 禁則): `docs/DESIGN_PRINCIPLES.json`
- アーキテクチャ全体図とコンポーネント: `docs/ARCHITECTURE.md`
- UI デザインシステム (色 / 余白 / コンポーネント語彙): `docs/UI_DESIGN.md`
- **UI 視覚仕様の SSOT**: `prototype/claude_design/` (全 9 画面の HTML/CSS) — 実装時はここをレイアウト参照とする。React で書かれているがロジックは htmx に置換、CSS と HTML 構造はそのまま流用
- 全機能の詳細設計 (権威ある原典): `docs/initial-input/Kuraos-design.md`
- スプリント計画と進捗: `docs/ROADMAP.json`
- スプリント実行ログ: `docs/sprint-logs/{SprintID}/`
