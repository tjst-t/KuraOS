#!/usr/bin/env bash
# app-cli — smoke test for `kura app lint / dataset-plan / reserve-port`.
# Mirrors the VM smoke checks called out in CLAUDE.md / the S1bccf5
# Sprint brief: parse + validate the immich fixture, plan datasets, and
# show that port reservations are stable across two invocations.
#
# Refuses to skip on missing binary — `make build` should land bin/kura
# before this script runs.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${ROOT}/bin/kura"
FIXTURE="${ROOT}/engine/app/testdata/immich.yaml"

if [ ! -x "$BIN" ]; then
  echo "app-cli: bin/kura missing; run 'make build' first"
  exit 1
fi
if [ ! -f "$FIXTURE" ]; then
  echo "app-cli: fixture $FIXTURE missing"
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
DB="$TMP/state.db"
export KURA_STATE_DB="$DB"
export KURA_SYSTEM_ROOT="$TMP/sysroot"
mkdir -p "$KURA_SYSTEM_ROOT/etc/samba"

echo "+ kura app lint"
"$BIN" app lint --file "$FIXTURE"

echo "+ kura app dataset-plan (ssd hint, ssd pool present)"
"$BIN" app dataset-plan \
  --file "$FIXTURE" --app-id immich.0001 \
  --pools "fast:ssd,tank:tank" \
  | tee "$TMP/plan.json"
grep -q '"chosen_pool": "fast"' "$TMP/plan.json" \
  || (echo "expected fast pool"; cat "$TMP/plan.json"; exit 1)

echo "+ kura app dataset-plan (no ssd pool — fallback to tank)"
DB2="$TMP/state2.db" KURA_STATE_DB="$DB2" "$BIN" app dataset-plan \
  --file "$FIXTURE" --app-id immich.0001 \
  --pools "tank:tank" \
  | tee "$TMP/plan2.json"
grep -q '"chosen_pool": "tank"' "$TMP/plan2.json" \
  || (echo "expected tank fallback"; cat "$TMP/plan2.json"; exit 1)

echo "+ kura app reserve-port — stable across two calls"
"$BIN" app reserve-port \
  --app-id immich.0001 --container server --manifest-port 3001 \
  --range-min 50000 --range-max 50010 \
  | tee "$TMP/port.txt"
grep -q "stable=true" "$TMP/port.txt" \
  || (echo "ports not stable"; cat "$TMP/port.txt"; exit 1)

echo "+ kura app fetch — production verifier fails closed (expected)"
set +e
"$BIN" app fetch \
  --url "https://example.invalid/" \
  --identity "^https://github.com/kuraos-org/" \
  --app immich --version 1.111.0 2>"$TMP/fetch.err"
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
  echo "expected fetch to fail (no real registry / no signature path wired)"
  exit 1
fi
echo "  fetch rc=$rc (expected non-zero)"

echo "app-cli: ok"
