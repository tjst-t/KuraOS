#!/usr/bin/env bash
# Acceptance smoke test for Sprint S1e7eeb (auth foundation).
#
# Wraps the Go acceptance suite at tests/acceptance/auth_flow_test.go so the
# same `bash tests/acceptance/auth-flow.sh` invocation pattern S464e47 used
# keeps working. The Go test exercises the real gateway + SQLite store with
# the exact deps wired in cmd/kura/main.go.

set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

if ! command -v go >/dev/null 2>&1; then
    echo "go not on PATH" >&2
    exit 1
fi

PASS=0
FAIL=0

run() {
    local label="$1"; shift
    if go test -count=1 "$@" >/tmp/kuraos-auth-flow.log 2>&1; then
        echo "PASS: $label"
        PASS=$((PASS + 1))
    else
        cat /tmp/kuraos-auth-flow.log >&2
        echo "FAIL: $label" >&2
        FAIL=$((FAIL + 1))
    fi
}

run "[AC-S1e7eeb-1-1/1-2] engine/user store + argon2id"  ./engine/user/...
run "[AC-S1e7eeb-2-x] session store + gateway middleware" ./engine/auth/session/... ./internal/gateway/...
run "[AC-S1e7eeb-2-x / 3-x] full UI auth + setup wizard flow" ./tests/acceptance/...

echo
echo "auth-flow.sh: $PASS passed, $FAIL failed"
exit "$FAIL"
