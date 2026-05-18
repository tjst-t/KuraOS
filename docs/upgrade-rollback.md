# KuraOS アップグレード後のロールバック手順

v1 では「今すぐ更新」ボタンが `apt upgrade` 前に ZFS スナップショット
`tank/rootfs@pre-upgrade-<ts>` を自動作成します。
アップグレード後にシステムが不安定になった場合、**CLI で以下の手順**を実行して
ロールバックしてください。

> **注意**: v1 には UI からのワンクリックロールバック機能はありません。
> これは VISION `non_goals_until_phase_2` で明示されており、Phase 2 の
> A/B パーティション or イメージスワップで構造的に解決される予定です。

---

## 手順

### 1. スナップショット一覧を確認

```bash
sudo zfs list -t snapshot -o name,creation -s creation | grep pre-upgrade
```

出力例:
```
tank/rootfs@pre-upgrade-20260518T143000Z  2026年 5月18日 月曜日 14:30:00 JST
```

### 2. 対象スナップショットへロールバック

```bash
# ロールバック先スナップショット名を確認して実行
sudo zfs rollback tank/rootfs@pre-upgrade-20260518T143000Z
```

> `zfs rollback` はデフォルトで指定したスナップショット以降のすべてのスナップショットを
> 削除してロールバックします。`-r` フラグを付けると子データセットも同時にロールバックします。

### 3. アプリデータセットのロールバック（必要な場合）

アップグレード前にアプリデータセットも同名スナップショットが作成されます。
対象があれば同様に実行します:

```bash
sudo zfs rollback tank/apps/immich/data@pre-upgrade-20260518T143000Z
```

### 4. kura プロセスを再起動

```bash
sudo systemctl restart kura
# または VM 環境では:
sudo pkill -x kura
cd /home/ubuntu/kuraos && sudo nohup env KURA_PORT=8204 KURA_STATE_DB=/home/ubuntu/kuraos/state.db \
  /home/ubuntu/kuraos/kura > /home/ubuntu/kuraos/kura.log 2>&1 &
```

### 5. 動作確認

```bash
curl -s http://localhost:8204/healthz
```

---

## 不要なスナップショットの削除

ロールバック不要が確認できたら、pre-upgrade スナップショットを削除できます:

```bash
sudo zfs destroy tank/rootfs@pre-upgrade-20260518T143000Z
```

---

## 参考

- design.md §17.2 ロールバック手順
- VISION `tech_constraints_phase_1`: "apt ベース OS 更新管理、kura バイナリ自身のアトミック更新まで"
- VISION `non_goals_until_phase_2`: "ワンクリック OS ロールバック UI"
