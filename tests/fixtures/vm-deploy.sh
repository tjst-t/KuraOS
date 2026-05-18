#!/usr/bin/env bash
# vm-deploy.sh — build kura locally and deploy to the dev VM
# (192.168.1.42) in one of two modes.
#
# Usage:
#   tests/fixtures/vm-deploy.sh simple        # 8204 plain HTTP, no Caddy / no mock IdP
#   tests/fixtures/vm-deploy.sh federation    # 8205 behind Caddy on 8204 TLS + mock IdP up
#
# simple mode is the default development target — `make e2e` and curl
# against http://192.168.1.42:8204 work out of the box, no TLS hassles.
# federation mode is for testing the Google login / pending-approval
# flow end-to-end. Both modes preserve the same state.db.
#
# Why both modes are reversible from one script:
#   - /etc/kura/federation.env (real Google client_id/secret) stays in
#     place across modes
#   - dev-oidc-mock.service + caddy.service stay installed; we only
#     toggle systemctl start/stop
#   - kura's KURA_FED_GOOGLE_* env is set only in federation mode, so
#     simple mode boots a vanilla kura that doesn't know about Google
#
# Pre-reqs (one-time VM setup, not handled here):
#   - /etc/kura/federation.env exists with real Google credentials
#   - /etc/systemd/system/dev-oidc-mock.service exists
#   - /etc/caddy/Caddyfile points kuraos-test.tjstkm.net:8204 →
#     reverse_proxy 127.0.0.1:8205
#   - DNS kuraos-test.tjstkm.net → 192.168.1.42
#   - Google Cloud Console OAuth client allows redirect URI
#     https://kuraos-test.tjstkm.net:8204/federation/google/callback
set -euo pipefail

MODE="${1:-}"
VM="${KURA_VM:-ubuntu@192.168.1.42}"
PUBLIC_HOST="${KURA_VM_PUBLIC_HOST:-kuraos-test.tjstkm.net}"

case "$MODE" in
  simple|federation) ;;
  *)
    echo "usage: $0 {simple|federation}" >&2
    exit 2
    ;;
esac

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

echo "==> building bin/kura (CGO_ENABLED=0)"
CGO_ENABLED=0 go build -o bin/kura ./cmd/kura

echo "==> rsync to $VM"
rsync -az bin/kura "$VM:/home/ubuntu/kuraos/kura.new"

# Render the remote launch command per mode. The two modes differ in:
#   - which port kura binds to (8204 vs 8205)
#   - whether KURA_PUBLIC_ORIGIN points at the Caddy TLS frontage
#   - whether KURA_FED_GOOGLE_* env is sourced from /etc/kura/federation.env
#   - whether Caddy + dev-oidc-mock systemd units are running
case "$MODE" in
simple)
  REMOTE_CMD=$(cat <<'EOF'
sudo systemctl stop caddy 2>/dev/null || true
sudo systemctl stop dev-oidc-mock 2>/dev/null || true
sudo pkill -x kura || true
sleep 2
sudo mv /home/ubuntu/kuraos/kura.new /home/ubuntu/kuraos/kura
sudo chmod +x /home/ubuntu/kuraos/kura
sudo bash -c '
  nohup env KURA_PORT=8204 KURA_STATE_DB=/home/ubuntu/kuraos/state.db \
    /home/ubuntu/kuraos/kura \
    > /home/ubuntu/kuraos/kura.log 2>&1 </dev/null &
'
sleep 2
curl -fsS http://127.0.0.1:8204/healthz
echo
EOF
)
  ;;
federation)
  REMOTE_CMD=$(cat <<EOF
sudo systemctl start caddy
sudo systemctl start dev-oidc-mock
sudo pkill -x kura || true
sleep 2
sudo mv /home/ubuntu/kuraos/kura.new /home/ubuntu/kuraos/kura
sudo chmod +x /home/ubuntu/kuraos/kura
sudo bash -c '
  set -a
  source /etc/kura/federation.env
  set +a
  nohup env KURA_PORT=8205 KURA_STATE_DB=/home/ubuntu/kuraos/state.db \\
    KURA_PUBLIC_ORIGIN=https://${PUBLIC_HOST}:8204 \\
    KURA_FED_GOOGLE_CLIENT_ID=\$KURA_FED_GOOGLE_CLIENT_ID \\
    KURA_FED_GOOGLE_CLIENT_SECRET=\$KURA_FED_GOOGLE_CLIENT_SECRET \\
    KURA_FED_GOOGLE_AUTO_PROVISION=\$KURA_FED_GOOGLE_AUTO_PROVISION \\
    /home/ubuntu/kuraos/kura \\
    > /home/ubuntu/kuraos/kura.log 2>&1 </dev/null &
'
sleep 2
curl -fsS http://127.0.0.1:8205/healthz
echo
EOF
)
  ;;
esac

echo "==> deploying in '$MODE' mode"
ssh "$VM" "$REMOTE_CMD"

case "$MODE" in
simple)
  echo
  echo "==> kura running at http://192.168.1.42:8204 (plain HTTP)"
  echo "    KURA_BASE_URL=http://192.168.1.42:8204 make e2e"
  ;;
federation)
  echo
  echo "==> kura at https://${PUBLIC_HOST}:8204 (via Caddy → 8205)"
  echo "    direct: http://192.168.1.42:8205 (bypasses TLS for curl/e2e)"
  echo "    mock IdP at 127.0.0.1:9998 (federation e2e fixture)"
  ;;
esac
