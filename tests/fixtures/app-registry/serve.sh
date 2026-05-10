#!/usr/bin/env bash
# Serve the signed fixture registry over HTTP for VM consumption.
# Listens on 0.0.0.0:9999 by default — the VM at 192.168.1.42 fetches
# from this host's LAN address.
#
# Usage:
#   bash tests/fixtures/app-registry/serve.sh           # 0.0.0.0:9999
#   PORT=8080 bash tests/fixtures/app-registry/serve.sh
set -euo pipefail
cd "$(dirname "$0")"
PORT="${PORT:-9999}"
if [[ ! -f registry.json ]]; then
  echo "error: registry.json not present. Run 'bash sign.sh' first (or 'make app-registry-sign')." >&2
  exit 1
fi
echo "Serving $PWD on 0.0.0.0:$PORT (Ctrl-C to stop)"
echo "On the VM:"
echo "  kura app registry add --name dev --url http://$(hostname -I | awk '{print $1}'):$PORT --identity 'kuraos-dev-fixture'"
exec python3 -m http.server "$PORT" --bind 0.0.0.0
