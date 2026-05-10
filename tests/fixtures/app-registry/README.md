# App registry test fixture

開発用ローカルアプリレジストリ。VM (192.168.1.42) の Apps Store を
インターネット非依存で試すための最小セット。本番 (公式) レジストリは
別リポジトリで管理する想定 (v1.x backlog)。

## 含まれるアプリ

| アプリ | 用途 | サイズ |
|---|---|---|
| `whoami` 1.10.2 | HTTP リクエストエコー — 最小スモークテスト | ~5 MB |
| `filebrowser` 2.27.0 | 共有ストレージのブラウザ操作 — share + persistent volume の検証 | ~30 MB |

## ワンタイム セットアップ (1 回だけ)

ローカル (このリポジトリの開発機) で:

```bash
make app-registry-sign     # 鍵生成 + 全マニフェスト + registry.json に署名
```

初回実行で:
- `keys/dev-signer.{key,pub}` を生成 (private key は `.gitignore` 済)
- 全 manifest.yaml に対して `manifest.yaml.sig` を生成
- `registry.json` を組み立て、`registry.json.sig` を生成

`apps/` 配下のマニフェストを編集したら **再度 `make app-registry-sign`** で署名し直す。

## 配信開始 (毎回)

```bash
make app-registry-serve    # 0.0.0.0:9999 で配信
```

LAN 上の VM が `http://<開発機の IP>:9999/registry.json` を取れるはず。

## VM 側で 1 回だけ登録

VM (192.168.1.42) の admin から:

```bash
ssh ubuntu@192.168.1.42 'sudo env KURA_STATE_DB=/home/ubuntu/kuraos/state.db \
  /home/ubuntu/kuraos/kura app registry add \
    --name dev \
    --url http://<開発機 IP>:9999 \
    --identity kuraos-dev-fixture'
```

公開鍵は VM の `/var/lib/kura/app-keys/kuraos-dev-fixture.pub` に置く必要があります。
ローカルの `keys/dev-signer.pub` を rsync:

```bash
sudo mkdir -p /var/lib/kura/app-keys
rsync keys/dev-signer.pub ubuntu@192.168.1.42:/tmp/
ssh ubuntu@192.168.1.42 'sudo mkdir -p /var/lib/kura/app-keys && \
  sudo cp /tmp/dev-signer.pub /var/lib/kura/app-keys/kuraos-dev-fixture.pub'
```

## UI から install

http://192.168.1.42:8204/ui/admin/apps → **Store タブ** に whoami と
filebrowser のカードが表示される。

- **whoami** はそのまま install (設定なし) → 完了後
  `http://192.168.1.42:8204/apps/whoami/` でリクエストエコー画面が出る
- **filebrowser** は `target_share` で共有を選択 (= photos 等) → install →
  `http://192.168.1.42:8204/apps/filebrowser/` でファイラ起動

## トラブルシューティング

| 症状 | 原因 | 対処 |
|---|---|---|
| Store タブに何も出ない | レジストリ未登録 or fetch 失敗 | `kura app registry list` で確認、kura.log に `registry fetch failed` 等出てないかチェック |
| `verify failed: no trusted key matched identity` | VM に公開鍵が無い | 上の rsync 手順を実施、kura を `pkill -x kura` で再起動 |
| `manifest_sha256 mismatch` | `registry.json` と `manifest.yaml` の不整合 | `make app-registry-sign` を再実行 |
| docker pull が遅い | Docker Hub のレートリミット | しばらく待つ or マニフェストの `image:` を別タグに変更 |

## ファイル構成

```
tests/fixtures/app-registry/
├── README.md                       (このファイル)
├── .gitignore                      (private key + 生成物を除外)
├── sign.sh                         (キー生成 + 全署名 + registry.json 構築)
├── serve.sh                        (python3 -m http.server 0.0.0.0:9999)
├── keys/
│   ├── dev-signer.key              (生成、gitignore)
│   └── dev-signer.pub              (生成、gitignore)
└── apps/
    ├── whoami/1.10.2/manifest.yaml
    └── filebrowser/2.27.0/manifest.yaml
```

## なぜ private key を gitignore?

dev fixture とはいえ private key を repo に commit するのは事故の元
(運用者が「dev key」と気付かず本番に転用するリスク、cloning した第三者が
正規署名を作れる、等)。public key だけ commit して、private は各
開発者の手元で生成・保持。失くしても `make app-registry-sign` が再生成。
