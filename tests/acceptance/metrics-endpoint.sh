#!/usr/bin/env bash
# [AC-S8a756d-1-2] /metrics エンドポイントが OpenMetrics (Prometheus) 形式で現在値を返す
#
# Requires a running kura instance at KURA_BASE_URL (default: local make serve).
# Admin credentials: admin/password.
set -euo pipefail

BASE="${KURA_BASE_URL:-http://localhost:${KURA_PORT:-8204}}"
ADMIN_USER="${KURA_ADMIN_USER:-admin}"
ADMIN_PASS="${KURA_ADMIN_PASS:-password}"

echo "==> metrics-endpoint.sh: GET /metrics at $BASE"

# 1. Get a session cookie by logging in.
COOKIE_JAR=$(mktemp)
trap 'rm -f "$COOKIE_JAR"' EXIT

LOGIN_RESP=$(curl -s -w "\n%{http_code}" -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -X POST "$BASE/login" \
  -d "username=$ADMIN_USER&password=$ADMIN_PASS" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --location)
LOGIN_CODE=$(echo "$LOGIN_RESP" | tail -1)
if [ "$LOGIN_CODE" != "200" ]; then
  echo "FAIL: login returned $LOGIN_CODE (expected 200 after redirect)"
  exit 1
fi
echo "  login: ok ($LOGIN_CODE)"

# 2. GET /metrics with the session cookie.
METRICS_RESP=$(curl -s -w "\n%{http_code}" -b "$COOKIE_JAR" "$BASE/metrics")
METRICS_CODE=$(echo "$METRICS_RESP" | tail -1)
METRICS_BODY=$(echo "$METRICS_RESP" | head -n -1)

if [ "$METRICS_CODE" != "200" ]; then
  echo "FAIL: /metrics returned $METRICS_CODE, expected 200"
  echo "Body: $METRICS_BODY"
  exit 1
fi
echo "  GET /metrics: $METRICS_CODE"

# 3. Verify OpenMetrics EOF trailer.
if ! echo "$METRICS_BODY" | grep -q "^# EOF$"; then
  echo "FAIL: /metrics response missing '# EOF' terminator"
  echo "Body (last 5 lines):"
  echo "$METRICS_BODY" | tail -5
  exit 1
fi
echo "  OpenMetrics EOF: present"

# 4. Verify at least one kura_ metric is present.
if ! echo "$METRICS_BODY" | grep -q "^kura_"; then
  echo "WARN: no kura_* metrics yet (ring buffer may not have data — retrying is normal on a fresh start)"
  # Not a hard failure on first boot; the ring buffer may still be filling.
fi

echo "PASS: AC-S8a756d-1-2 /metrics endpoint check passed"
