#!/usr/bin/env bash
# Acceptance test for Sprint S464e47 Story 1.
#
#   [AC-S464e47-1-2]: every UI string is fed through i18n.T and ja.json holds
#                     a translation for every MessageID declared in code.
#
# Strategy: scan i18n/messages.go for MessageID constants, then assert each
# one has a key in i18n/locales/ja.json. We do *not* try to lint the
# templates for raw Japanese here — the unit tests (internal/ui/ui_test.go)
# already verify rendered output uses translated text.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

PASS=0
FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL + 1)); }
pass() { echo "PASS: $*"; PASS=$((PASS + 1)); }

MESSAGES_GO="i18n/messages.go"
JA_JSON="i18n/locales/ja.json"

if [ ! -f "$MESSAGES_GO" ] || [ ! -f "$JA_JSON" ]; then
    fail "[AC-S464e47-1-2] required files missing: $MESSAGES_GO / $JA_JSON"
    exit "$FAIL"
fi

# Extract every MessageID literal of the form "foo.bar" appearing in messages.go
mapfile -t IDS < <(grep -oE 'MessageID = "[^"]+"' "$MESSAGES_GO" | sed -E 's/MessageID = "([^"]+)"/\1/')

if [ "${#IDS[@]}" -eq 0 ]; then
    fail "[AC-S464e47-1-2] no MessageID constants found in $MESSAGES_GO"
    exit "$FAIL"
fi

MISSING=0
for id in "${IDS[@]}"; do
    if ! grep -qE "\"$(printf '%s' "$id" | sed 's/\./\\./g')\"\\s*:" "$JA_JSON"; then
        echo "missing: $id" >&2
        MISSING=$((MISSING + 1))
    fi
done

if [ "$MISSING" -eq 0 ]; then
    pass "[AC-S464e47-1-2] every MessageID has a ja translation (${#IDS[@]} keys)"
else
    fail "[AC-S464e47-1-2] $MISSING MessageID(s) missing from $JA_JSON"
fi

# Also verify that ja.json parses as JSON so a stray comma can't ship.
if ! python3 -c "import json,sys;json.load(open('$JA_JSON'))" 2>/tmp/kuraos-i18n-coverage.log; then
    cat /tmp/kuraos-i18n-coverage.log >&2
    fail "[AC-S464e47-1-2] $JA_JSON is not valid JSON"
else
    pass "[AC-S464e47-1-2] $JA_JSON parses as JSON"
fi

echo
echo "i18n-coverage.sh: $PASS passed, $FAIL failed"
exit "$FAIL"
