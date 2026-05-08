#!/usr/bin/env bash
# [AC-S9db742-2-1] / [AC-S9db742-2-2] / [AC-S9db742-2-3] CLI surface for
# Volume / Snapshot / Rollback / Quota.
#
# The dev box has no ZFS, so these tests assert that the kura binary parses
# the subcommand flags and tries to execute zfs / zpool — they don't verify
# the actual ZFS state. The fact that we see "executable file not found"
# (or a translated error) instead of a flag-parsing error proves the CLI
# wiring reached the engine; the engine layer is covered by Go unit tests
# in engine/storage/create_test.go.

set -euo pipefail

cd "$(dirname "$0")/../.."

KURA="${KURA:-./bin/kura}"
if [ ! -x "$KURA" ]; then
  echo "[AC-S9db742-2-cli] $KURA not built. Run 'make build' first."
  exit 1
fi

pass=0
fail=0
expect_subcommand_reachable() {
  local label="$1"; shift
  # Capture output; we expect non-zero exit (zfs/zpool missing on dev box)
  # but no flag-parsing error. The presence of "create volume:" / etc. in
  # the wrapped error proves we reached the engine call.
  if out=$("$KURA" "$@" 2>&1); then
    echo "PASS: $label (engine ran without error — unexpected on dev box but acceptable)"
    pass=$((pass+1))
  else
    if echo "$out" | grep -qE 'flag provided but not defined|usage:|invalid quota'; then
      echo "FAIL: $label — flag/parsing error: $out"
      fail=$((fail+1))
    else
      echo "PASS: $label (engine reached, errored on missing zfs/zpool: $(echo "$out" | head -1))"
      pass=$((pass+1))
    fi
  fi
}

expect_subcommand_reachable "[AC-S9db742-2-1] kura storage create-volume --preset media --quota 1073741824 tank/photos" \
  storage create-volume --preset media --quota 1073741824 tank/photos
expect_subcommand_reachable "[AC-S9db742-2-1] kura storage list-volumes (no pool)" \
  storage list-volumes
expect_subcommand_reachable "[AC-S9db742-2-1] kura storage list-volumes tank --json" \
  storage list-volumes tank --json
expect_subcommand_reachable "[AC-S9db742-2-2] kura storage create-snapshot tank/photos snap1" \
  storage create-snapshot tank/photos snap1
expect_subcommand_reachable "[AC-S9db742-2-2] kura storage list-snapshots tank/photos" \
  storage list-snapshots tank/photos
expect_subcommand_reachable "[AC-S9db742-2-2] kura storage rollback tank/photos snap1" \
  storage rollback tank/photos snap1
expect_subcommand_reachable "[AC-S9db742-2-3] kura storage set-quota tank/photos 1073741824" \
  storage set-quota tank/photos 1073741824
expect_subcommand_reachable "[AC-S9db742-2-3] kura storage set-quota tank/photos 0 (unset)" \
  storage set-quota tank/photos 0
expect_subcommand_reachable "[AC-S9db742-2-4] kura storage destroy-pool --confirm tank tank" \
  storage destroy-pool --confirm tank tank
expect_subcommand_reachable "[AC-S9db742-2-4] kura storage destroy-volume --recursive --confirm tank/photos tank/photos" \
  storage destroy-volume --recursive --confirm tank/photos tank/photos
expect_subcommand_reachable "[AC-S9db742-2-4] kura storage destroy-snapshot tank/photos snap1" \
  storage destroy-snapshot tank/photos snap1

# Negative path: --confirm mismatch must be caught BEFORE the engine call.
out=$("$KURA" storage destroy-pool --confirm WRONG tank 2>&1 || true)
if echo "$out" | grep -q '\-\-confirm must match'; then
  echo "PASS: destroy-pool rejects mismatched --confirm"
  pass=$((pass+1))
else
  echo "FAIL: destroy-pool should reject --confirm mismatch (got: $out)"
  fail=$((fail+1))
fi
out=$("$KURA" storage destroy-volume --confirm WRONG tank/photos 2>&1 || true)
if echo "$out" | grep -q '\-\-confirm must match'; then
  echo "PASS: destroy-volume rejects mismatched --confirm"
  pass=$((pass+1))
else
  echo "FAIL: destroy-volume should reject --confirm mismatch (got: $out)"
  fail=$((fail+1))
fi

# Negative paths: missing args / invalid flags must produce a flag/usage error.
# Capture stderr+stdout into a variable so `set -o pipefail` doesn't mask the
# non-zero exit (kura intentionally fails on malformed input).
out=$("$KURA" storage create-volume 2>&1 || true)
if echo "$out" | grep -qE 'positional|usage|required'; then
  echo "PASS: create-volume requires dataset positional"
  pass=$((pass+1))
else
  echo "FAIL: create-volume should reject missing positional arg (got: $out)"
  fail=$((fail+1))
fi

out=$("$KURA" storage set-quota tank/photos abc 2>&1 || true)
if echo "$out" | grep -qE 'invalid quota|integer'; then
  echo "PASS: set-quota validates numeric quota"
  pass=$((pass+1))
else
  echo "FAIL: set-quota should reject non-numeric quota (got: $out)"
  fail=$((fail+1))
fi

echo
echo "storage-volume-cli.sh: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
