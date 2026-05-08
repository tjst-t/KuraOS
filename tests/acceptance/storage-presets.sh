#!/usr/bin/env bash
# [AC-S9db742-2-1] Verify that the general/media/database presets resolve to
# the expected ZFS properties via the storage engine. The dev box has no ZFS
# so this is a Go unit test wrapper — the real engine assertion lives in
# engine/storage/create_test.go (TestBuildCreateVolumeArgs_presetExpansion).
#
# This script runs that single test and exits non-zero if any preset's ZFS
# property bundle drifts from design.md §4.4.

set -euo pipefail

cd "$(dirname "$0")/../.."

go test ./engine/storage/ \
  -run TestBuildCreateVolumeArgs_presetExpansion \
  -count=1 -v
