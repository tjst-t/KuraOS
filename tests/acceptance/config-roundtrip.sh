#!/usr/bin/env bash
# Acceptance tests for Sprint S464e47 Story 2 (config.json import/export).
#
#   [AC-S464e47-2-1]: kura config export prints a config.json that re-parses
#                     and stays empty when SQLite is empty.
#   [AC-S464e47-2-2]: kura config apply <file> --dry-run prints a diff.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

PASS=0
FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL + 1)); }
pass() { echo "PASS: $*"; PASS=$((PASS + 1)); }

if [ ! -x bin/kura ]; then
    make build >/tmp/kuraos-acceptance-config-build.log 2>&1 || {
        cat /tmp/kuraos-acceptance-config-build.log >&2
        echo "FAIL: build prerequisite failed" >&2
        exit 1
    }
fi

# ---------------------------------------------------------------------------
# [AC-S464e47-2-1] kura config export
# ---------------------------------------------------------------------------
EXPORT_FILE="$(mktemp)"
trap 'rm -f "$EXPORT_FILE" "$EXPORT_FILE.2" "$APPLY_FILE" "$APPLY_OUT" 2>/dev/null || true' EXIT

if ./bin/kura config export >"$EXPORT_FILE" 2>/tmp/kuraos-acceptance-config-export.log; then
    if grep -q '"schema_version"' "$EXPORT_FILE"; then
        pass "[AC-S464e47-2-1] kura config export wrote schema_version field"
    else
        cat "$EXPORT_FILE" >&2
        fail "[AC-S464e47-2-1] export missing schema_version"
    fi
else
    cat /tmp/kuraos-acceptance-config-export.log >&2
    fail "[AC-S464e47-2-1] kura config export exited non-zero"
fi

# Round-trip: export -> apply --dry-run on the same file should be a noop
# plan (or all entries `noop`).
APPLY_FILE="$EXPORT_FILE"
APPLY_OUT="$(mktemp)"
if ./bin/kura config apply --dry-run "$APPLY_FILE" >"$APPLY_OUT" 2>/tmp/kuraos-acceptance-config-apply.log; then
    if grep -q "^add\|^remove\|^update" "$APPLY_OUT"; then
        cat "$APPLY_OUT" >&2
        fail "[AC-S464e47-2-1] round-trip apply --dry-run produced changes for empty export"
    else
        pass "[AC-S464e47-2-1] round-trip apply --dry-run is a noop on empty export"
    fi
else
    cat /tmp/kuraos-acceptance-config-apply.log >&2
    fail "[AC-S464e47-2-1] apply --dry-run failed for empty export"
fi

# ---------------------------------------------------------------------------
# [AC-S464e47-2-2] apply --dry-run shows diff entries when sections appear
# ---------------------------------------------------------------------------
NONEMPTY_FILE="$(mktemp)"
cat >"$NONEMPTY_FILE" <<'JSON'
{
  "schema_version": 1,
  "storage": {},
  "shares": {}
}
JSON
trap 'rm -f "$EXPORT_FILE" "$NONEMPTY_FILE" "$APPLY_OUT" 2>/dev/null || true' EXIT

if ./bin/kura config apply --dry-run "$NONEMPTY_FILE" >"$APPLY_OUT" 2>/tmp/kuraos-acceptance-config-apply2.log; then
    if grep -q '^add[[:space:]].*storage' "$APPLY_OUT" && \
       grep -q '^add[[:space:]].*shares' "$APPLY_OUT"; then
        pass "[AC-S464e47-2-2] apply --dry-run reports add for non-empty sections"
    else
        cat "$APPLY_OUT" >&2
        fail "[AC-S464e47-2-2] apply --dry-run output missing expected add entries"
    fi
else
    cat /tmp/kuraos-acceptance-config-apply2.log >&2
    fail "[AC-S464e47-2-2] apply --dry-run exited non-zero"
fi

# ---------------------------------------------------------------------------
# [AC-Ssys001-2-2] TestUsersExportContainsOnlyPlaceholder — when a config
# is hand-authored to include a users section, every entry MUST carry only
# `credential_state` and never argon2id verifier / NT-hash / raw secret.
# ---------------------------------------------------------------------------
USERS_FILE="$(mktemp)"
cat >"$USERS_FILE" <<'JSON'
{
  "schema_version": 1,
  "users": {
    "users": [
      {"username": "alice", "role": "admin", "credential_state": "set"},
      {"username": "bob",   "role": "user",  "credential_state": "set"}
    ]
  }
}
JSON

if ! ./bin/kura config apply --dry-run "$USERS_FILE" >"$APPLY_OUT" 2>/tmp/kuraos-acceptance-users-apply.log; then
    cat /tmp/kuraos-acceptance-users-apply.log >&2
    fail "[AC-Ssys001-2-2] apply --dry-run rejected a users section that holds only placeholders"
else
    pass "[AC-Ssys001-2-2] apply --dry-run accepts users section with credential_state placeholders"
fi

# Same file with an embedded credential MUST be rejected (DisallowUnknownFields)
LEAK_FILE="$(mktemp)"
cat >"$LEAK_FILE" <<'JSON'
{
  "schema_version": 1,
  "users": {
    "users": [
      {"username": "alice", "role": "admin", "credential_state": "set", "argon2id": "$argon2id$leak"}
    ]
  }
}
JSON

if ./bin/kura config apply --dry-run "$LEAK_FILE" >/dev/null 2>/tmp/kuraos-acceptance-leak-apply.log; then
    fail "[AC-Ssys001-2-2] apply --dry-run accepted a config with an embedded argon2id field"
else
    pass "[AC-Ssys001-2-2] apply --dry-run rejects embedded credential fields (vault is the only secret store)"
fi
rm -f "$USERS_FILE" "$LEAK_FILE"

echo
echo "config-roundtrip.sh: $PASS passed, $FAIL failed"
exit "$FAIL"
