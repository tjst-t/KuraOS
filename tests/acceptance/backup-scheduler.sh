#!/usr/bin/env bash
# backup-scheduler.sh — Acceptance tests for Se1e7a6 backup/scheduler HTTP handlers.
#
# Acceptance criteria covered (handler-level, not GUI):
#   [AC-Se1e7a6-1-1] schedules CRUD visible via /ui/admin/settings?tab=backup
#   [AC-Se1e7a6-2-1] backend CRUD visible via /ui/admin/settings?tab=backup
#   [AC-Se1e7a6-3-1] /ui/admin/settings/upgrade/run handler responds 200
#
# Full GUI validation is in tests/e2e/backup-*.e2e.spec.ts.
# Requires a running kura at KURA_BASE_URL (default http://192.168.1.42:8204).

set -euo pipefail

BASE="${KURA_BASE_URL:-http://192.168.1.42:8204}"
USER="${KURA_TEST_ADMIN_USERNAME:-admin}"
PASS="${KURA_TEST_ADMIN_PASSWORD:-password}"

PASS_COUNT=0
FAIL_COUNT=0
fail() { echo "FAIL: $*"; FAIL_COUNT=$((FAIL_COUNT+1)); }
pass() { echo "PASS: $*"; PASS_COUNT=$((PASS_COUNT+1)); }

COOKIE_JAR=$(mktemp)
trap 'rm -f "$COOKIE_JAR"' EXIT

# ── Login ─────────────────────────────────────────────────────────────────────
login_status=$(curl -s -o /dev/null -w "%{http_code}" -c "$COOKIE_JAR" \
  -X POST "${BASE}/login" \
  -d "username=${USER}&password=${PASS}" -L)
if [[ "$login_status" != "200" ]]; then
  echo "FATAL: login failed (status $login_status). Is kura running at $BASE?"
  exit 2
fi
pass "login"

# ── [AC-Se1e7a6-1-1] Backup tab reachable ───────────────────────────────────
tab_status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_JAR" \
  "${BASE}/ui/admin/settings?tab=backup")
if [[ "$tab_status" != "200" ]]; then
  fail "[AC-Se1e7a6-1-1] GET /ui/admin/settings?tab=backup returned $tab_status"
else
  tab_body=$(curl -s -b "$COOKIE_JAR" "${BASE}/ui/admin/settings?tab=backup")
  if echo "$tab_body" | grep -q "schedules-card"; then
    pass "[AC-Se1e7a6-1-1] backup tab contains schedules-card"
  else
    fail "[AC-Se1e7a6-1-1] backup tab missing schedules-card"
  fi
fi

# ── [AC-Se1e7a6-1-1] Schedule CRUD ──────────────────────────────────────────
sched_create=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_JAR" \
  -X POST "${BASE}/ui/admin/settings/backup/schedules/create" \
  -d "name=se1e7a6-acceptance&cron_expr=0+*+*+*+*&datasets=tank%2Fphotos&ret_hourly=24&ret_daily=7&ret_monthly=3")
if [[ "$sched_create" != "200" ]]; then
  fail "[AC-Se1e7a6-1-1] schedule create returned $sched_create"
else
  pass "[AC-Se1e7a6-1-1] schedule create 200"
  list=$(curl -s -b "$COOKIE_JAR" "${BASE}/ui/admin/settings?tab=backup")
  if echo "$list" | grep -q "se1e7a6-acceptance"; then
    pass "[AC-Se1e7a6-1-1] created schedule visible in list"
  else
    fail "[AC-Se1e7a6-1-1] created schedule not found in list"
  fi
fi

# ── [AC-Se1e7a6-2-1] Backend CRUD ───────────────────────────────────────────
be_create=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_JAR" \
  -X POST "${BASE}/ui/admin/settings/backup/backends/create" \
  -d "name=se1e7a6-be&kind=restic&repo=s3%3Abucket%2Fkura")
if [[ "$be_create" != "200" ]]; then
  fail "[AC-Se1e7a6-2-1] backend create returned $be_create"
else
  pass "[AC-Se1e7a6-2-1] backend create 200"
  belist=$(curl -s -b "$COOKIE_JAR" "${BASE}/ui/admin/settings?tab=backup")
  if echo "$belist" | grep -q "se1e7a6-be"; then
    pass "[AC-Se1e7a6-2-1] created backend visible in list"
  else
    fail "[AC-Se1e7a6-2-1] created backend not found in list"
  fi
fi

# ── [AC-Se1e7a6-3-1] Upgrade handler responds ───────────────────────────────
upgrade_status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_JAR" \
  -X POST "${BASE}/ui/admin/settings/upgrade/run")
if [[ "$upgrade_status" != "200" ]]; then
  fail "[AC-Se1e7a6-3-1] POST /ui/admin/settings/upgrade/run returned $upgrade_status"
else
  pass "[AC-Se1e7a6-3-1] upgrade/run returns 200"
fi

echo ""
echo "backup-scheduler.sh: $PASS_COUNT passed, $FAIL_COUNT failed"
test "$FAIL_COUNT" -eq 0
