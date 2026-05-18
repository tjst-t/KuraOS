#!/usr/bin/env bash
# no-rollback-ui.sh — Asserts AC-Se1e7a6-3-2: v1 does NOT provide a one-click
# rollback UI. Verifying absence: the upgrade/rollback endpoint must NOT exist.
#
# [AC-Se1e7a6-3-2] v1 ではワンクリックロールバック UI は提供しない
# (DESIGN_PRINCIPLES forbidden: "ワンクリック OS ロールバック UI を v1 で実装しない")
# (VISION non_goals_until_phase_2: "ワンクリック OS ロールバック UI")

set -euo pipefail

BASE="${KURA_BASE_URL:-http://192.168.1.42:8204}"
USER="${KURA_TEST_ADMIN_USERNAME:-admin}"
PASS="${KURA_TEST_ADMIN_PASSWORD:-password}"

COOKIE_JAR=$(mktemp)
trap 'rm -f "$COOKIE_JAR"' EXIT

login_resp=$(curl -s -D - -c "$COOKIE_JAR" \
  -X POST "${BASE}/login" \
  -d "username=${USER}&password=${PASS}")
login_code=$(echo "$login_resp" | head -1 | awk '{print $2}')
if [[ "$login_code" != "302" ]] && [[ "$login_code" != "200" ]]; then
  echo "FATAL: login failed (status $login_code)"
  exit 2
fi
# Manually follow redirect to seed the cookie.
curl -s -o /dev/null -b "$COOKIE_JAR" "${BASE}/ui/admin/dashboard" >/dev/null

# The rollback endpoint must NOT exist (404 or 405 expected, never 200).
rollback_status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_JAR" \
  -X POST "${BASE}/ui/admin/settings/upgrade/rollback" 2>/dev/null || echo "000")

if [[ "$rollback_status" == "200" ]]; then
  echo "FAIL: [AC-Se1e7a6-3-2] rollback endpoint returned 200 — one-click rollback UI must NOT exist in v1"
  exit 1
fi

# Also check that the Settings page backup tab does not contain rollback button.
backup_tab=$(curl -s -b "$COOKIE_JAR" "${BASE}/ui/admin/settings?tab=backup" 2>/dev/null || true)
if echo "$backup_tab" | grep -qi "rollback\|ロールバック"; then
  echo "FAIL: [AC-Se1e7a6-3-2] backup tab contains rollback UI element — forbidden in v1"
  exit 1
fi

echo "PASS: [AC-Se1e7a6-3-2] no rollback UI endpoint or UI element found (status=$rollback_status)"
echo ""
echo "no-rollback-ui.sh: PASS"
