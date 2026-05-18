#!/usr/bin/env bash
# Acceptance test for `kura init` (AC-S99702c-2-2).
#
#   [AC-S99702c-2-2] `kura init --admin-user=... --admin-password=...`
#                    creates the first admin without starting the HTTP server.
#                    Re-running it must fail with "admin already exists".

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

PASS=0
FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL + 1)); }
pass() { echo "PASS: $*"; PASS=$((PASS + 1)); }

if [ ! -x bin/kura ]; then
    make build >/tmp/kuraos-acceptance-init-build.log 2>&1 || {
        cat /tmp/kuraos-acceptance-init-build.log >&2
        echo "FAIL: build prerequisite failed" >&2
        exit 1
    }
fi

# Use a fresh temp DB for isolation.
TMPDB="$(mktemp -d)/init-test.db"
trap 'rm -rf "$(dirname "$TMPDB")"' EXIT

# ---------------------------------------------------------------------------
# [AC-S99702c-2-2] kura init creates first admin
# ---------------------------------------------------------------------------
OUT="$(KURA_STATE_DB="$TMPDB" ./bin/kura init \
    --admin-user=initadmin \
    --admin-password=supersecretpass \
    --display-name="Init Admin" 2>&1)"

if echo "$OUT" | grep -q 'admin user.*created'; then
    pass "[AC-S99702c-2-2] kura init reported admin created"
else
    echo "$OUT" >&2
    fail "[AC-S99702c-2-2] kura init did not report success"
fi

# ---------------------------------------------------------------------------
# [AC-S99702c-2-2] Re-running must fail (idempotency guard)
# ---------------------------------------------------------------------------
if KURA_STATE_DB="$TMPDB" ./bin/kura init \
    --admin-user=initadmin2 \
    --admin-password=supersecretpass2 \
    >/dev/null 2>&1; then
    fail "[AC-S99702c-2-2] kura init succeeded on already-initialised DB (should fail)"
else
    pass "[AC-S99702c-2-2] kura init fails when admin already exists"
fi

# ---------------------------------------------------------------------------
# [AC-S99702c-2-2] Short password must be rejected
# ---------------------------------------------------------------------------
SHORTPW_DB="$(mktemp -d)/shortpw.db"
trap 'rm -rf "$(dirname "$SHORTPW_DB")"' EXIT

if KURA_STATE_DB="$SHORTPW_DB" ./bin/kura init \
    --admin-user=shortadmin \
    --admin-password=tooshort \
    >/dev/null 2>&1; then
    fail "[AC-S99702c-2-2] kura init accepted short password (< 12 chars)"
else
    pass "[AC-S99702c-2-2] kura init rejects password shorter than 12 characters"
fi

echo
echo "headless-init.sh: $PASS passed, $FAIL failed"
exit "$FAIL"
