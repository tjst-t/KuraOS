#!/usr/bin/env bash
# Acceptance tests for Sprint S0ff37f Story 1 (binary skeleton + /healthz).
# Each assertion is tagged with [AC-S0ff37f-1-N] for traceability.
#
# Run from repo root:  bash tests/acceptance/bootstrap.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

PASS=0
FAIL=0
fail() {
    echo "FAIL: $*" >&2
    FAIL=$((FAIL + 1))
}
pass() {
    echo "PASS: $*"
    PASS=$((PASS + 1))
}

# ---------------------------------------------------------------------------
# [AC-S0ff37f-1-1] make build creates bin/kura
# ---------------------------------------------------------------------------
rm -f bin/kura
if make build >/tmp/kuraos-acceptance-build.log 2>&1 && [ -x bin/kura ]; then
    pass "[AC-S0ff37f-1-1] make build produces bin/kura"
else
    cat /tmp/kuraos-acceptance-build.log >&2 || true
    fail "[AC-S0ff37f-1-1] make build did not produce executable bin/kura"
fi

# ---------------------------------------------------------------------------
# [AC-S0ff37f-1-2] make serve starts kura on portman-allocated port and
# /healthz returns 200.
# ---------------------------------------------------------------------------
PORT_FILE="$(mktemp)"
trap 'rm -f "$PORT_FILE"' EXIT

# Use portman directly so we control the env file location and don't rely on
# the Makefile's /tmp default leaking between runs.
portman env --name kura --expose --output "$PORT_FILE" >/dev/null
# shellcheck disable=SC1090
. "$PORT_FILE"

# Use a temporary state DB so this test doesn't require write access to /var/lib/kura
TMP_STATE_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_STATE_DIR"; rm -f "$PORT_FILE"; [ -n "${KURA_PID:-}" ] && kill "$KURA_PID" 2>/dev/null || true' EXIT

KURA_PORT="$KURA_PORT" KURA_STATE_DB="$TMP_STATE_DIR/state.db" \
    ./bin/kura >/tmp/kuraos-acceptance-kura.log 2>&1 &
KURA_PID=$!

# Wait up to 5s for the server to come up.
HEALTHZ_OK=0
for _ in $(seq 1 50); do
    if curl -fsS -o /tmp/kuraos-acceptance-healthz.json "http://127.0.0.1:${KURA_PORT}/healthz"; then
        HEALTHZ_OK=1
        break
    fi
    sleep 0.1
done

if [ "$HEALTHZ_OK" -eq 1 ]; then
    if grep -q '"status"' /tmp/kuraos-acceptance-healthz.json && \
       grep -q '"version"' /tmp/kuraos-acceptance-healthz.json && \
       grep -q '"started_at"' /tmp/kuraos-acceptance-healthz.json; then
        pass "[AC-S0ff37f-1-2] /healthz returns 200 with status/version/started_at"
    else
        cat /tmp/kuraos-acceptance-healthz.json >&2
        fail "[AC-S0ff37f-1-2] /healthz body missing required fields"
    fi
else
    cat /tmp/kuraos-acceptance-kura.log >&2 || true
    fail "[AC-S0ff37f-1-2] kura did not respond on /healthz within 5s"
fi

kill "$KURA_PID" 2>/dev/null || true
wait "$KURA_PID" 2>/dev/null || true
KURA_PID=""

portman release --name kura >/dev/null 2>&1 || true

echo
echo "bootstrap.sh: $PASS passed, $FAIL failed"
exit "$FAIL"
