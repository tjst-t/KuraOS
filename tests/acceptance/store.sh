#!/usr/bin/env bash
# Acceptance test for Sprint S0ff37f Story 2 (SQLite store + migration).
# [AC-S0ff37f-2-1]: kura creates state.db and provisions tables when launched.

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

if [ ! -x bin/kura ]; then
    make build >/tmp/kuraos-acceptance-store-build.log 2>&1 || {
        cat /tmp/kuraos-acceptance-store-build.log >&2
        echo "FAIL: build prerequisite failed" >&2
        exit 1
    }
fi

PORT_FILE="$(mktemp)"
TMP_STATE_DIR="$(mktemp -d)"
DB_PATH="$TMP_STATE_DIR/state.db"
KURA_PID=""
cleanup() {
    [ -n "$KURA_PID" ] && kill "$KURA_PID" 2>/dev/null || true
    rm -f "$PORT_FILE"
    rm -rf "$TMP_STATE_DIR"
    portman release --name kura >/dev/null 2>&1 || true
}
trap cleanup EXIT

portman env --name kura --expose --output "$PORT_FILE" >/dev/null
# shellcheck disable=SC1090
. "$PORT_FILE"

# Sanity: db path does not exist before launch.
if [ -e "$DB_PATH" ]; then
    fail "[AC-S0ff37f-2-1] precondition: $DB_PATH already exists"
    exit 1
fi

KURA_PORT="$KURA_PORT" KURA_STATE_DB="$DB_PATH" \
    ./bin/kura >/tmp/kuraos-acceptance-store-kura.log 2>&1 &
KURA_PID=$!

# Wait until the binary listens (which means migrations completed).
for _ in $(seq 1 50); do
    if curl -fsS -o /dev/null "http://127.0.0.1:${KURA_PORT}/healthz"; then
        break
    fi
    sleep 0.1
done

if [ ! -f "$DB_PATH" ]; then
    cat /tmp/kuraos-acceptance-store-kura.log >&2
    fail "[AC-S0ff37f-2-1] state.db was not created at $DB_PATH"
    exit "$FAIL"
fi

# Inspect via Go (sqlite3 CLI may not be installed on dev hosts).
TABLE_LIST="$(go run ./tests/acceptance/listtables "$DB_PATH")"
if echo "$TABLE_LIST" | grep -q '^schema_version$' && \
   echo "$TABLE_LIST" | grep -q '^kv$'; then
    pass "[AC-S0ff37f-2-1] state.db created with schema_version + kv tables"
else
    echo "tables: $TABLE_LIST" >&2
    fail "[AC-S0ff37f-2-1] expected tables not present in $DB_PATH"
fi

echo
echo "store.sh: $PASS passed, $FAIL failed"
exit "$FAIL"
