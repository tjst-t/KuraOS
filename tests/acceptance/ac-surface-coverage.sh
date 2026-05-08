#!/usr/bin/env bash
# ac-surface-coverage — guards against the failure mode that produced the
# S9db742-2 hotfix: an AC text that promised a user-facing surface ("UI から
# 実行できる") was marked done with only an engine-layer Go test as its
# verification reference, so the operator-visible feature was never wired.
#
# Rule:
#   - If an AC's `description` mentions UI / 画面 / フォーム / ブラウザ /
#     /ui/* / /login etc. → its `test` reference MUST include a UI-level
#     test path (tests/e2e/*.spec.ts, tests/mock/*.spec.ts,
#     tests/acceptance/*_test.go (Go acceptance), or internal/ui/*_test.go).
#   - If an AC's `description` mentions CLI / `kura ... ` / make build|serve
#     etc. → its `test` reference MUST include a shell test
#     (tests/acceptance/*.sh) or a Go subprocess test that invokes the binary.
#
# Run from sprint verify (and ad-hoc) to catch surface/test mismatch early.
# Reads docs/ROADMAP.json (override with ROADMAP env var). Exits non-zero
# on the first violation; report lists every offender so one CI run flags
# the full backlog.

set -euo pipefail

ROADMAP="${ROADMAP:-docs/ROADMAP.json}"
if [ ! -f "$ROADMAP" ]; then
  echo "ac-surface-coverage: $ROADMAP not found"
  exit 2
fi

# Surface-keyword regexes for the AC description (extended grep syntax).
ui_keywords='(UI から|UI で|画面|フォーム|サイドバー|ボタン|モーダル|ダイアログ|チェックボックス|ラジオボタン|リダイレクト|hover|クリック|選択肢|/ui/|/login|/setup)'
cli_keywords='(`kura |kura config |kura storage |kura version|make (build|serve|stop|test|lint)|シェル)'

# Allowed test-path patterns per surface. UI surface accepts Go-level UI
# tests, Playwright e2e specs, or Go acceptance tests that boot httptest
# servers — anything that exercises the actual HTML/HTTP path. CLI surface
# accepts shell tests (real binary invocation) or Go acceptance tests with
# kura CLI invocation.
ui_test_paths='(tests/e2e/[^[:space:]]+\.spec\.ts|tests/mock/[^[:space:]]+\.spec\.ts|tests/acceptance/[^[:space:]]*_test\.go|internal/ui/[^[:space:]]*_test\.go|httptest)'
cli_test_paths='(tests/acceptance/[^[:space:]]*\.sh|tests/acceptance/[^[:space:]]*_test\.go.*kura|cmd/kura/[^[:space:]]*_test\.go)'

fail=0
total=0
checked=0

# Iterate every AC of every sprint, regardless of sprint status — incomplete
# sprints are checked too so plan-time surface mismatch is caught before
# implementation starts. (Originally only `done` ACs were checked, but a
# pre-flight check at planning time is cheaper than a post-mortem fix.)
while IFS= read -r ac; do
  total=$((total + 1))
  ac_id=$(echo "$ac" | jq -r '.id')
  desc=$(echo "$ac" | jq -r '.description // ""')
  test_ref=$(echo "$ac" | jq -r '.test // ""')
  status=$(echo "$ac" | jq -r '.status // "pending"')

  # Skip ACs that don't claim any surface — pure engine concerns are fine
  # with engine_test.go references.
  has_ui=false
  has_cli=false
  echo "$desc" | grep -qE "$ui_keywords" && has_ui=true
  echo "$desc" | grep -qE "$cli_keywords" && has_cli=true

  # Negative ACs ("UI は提供しない" / "実装しない" / "採用しない") are fine
  # with a shell-level guard test that asserts the surface is absent — they
  # don't need a positive UI test path.
  if echo "$desc" | grep -qE '(しない|提供しない|実装しない|採用しない|行わない|禁止)'; then
    has_ui=false
  fi

  if ! $has_ui && ! $has_cli; then
    continue
  fi
  checked=$((checked + 1))

  if $has_ui && ! echo "$test_ref" | grep -qE "$ui_test_paths"; then
    echo "❌ $ac_id ($status): description claims UI surface, but test ref points only at engine"
    echo "    desc: $desc"
    echo "    test: $test_ref"
    echo
    fail=$((fail + 1))
  fi
  if $has_cli && ! echo "$test_ref" | grep -qE "$cli_test_paths"; then
    echo "❌ $ac_id ($status): description claims CLI surface, but test ref does not include a shell or CLI subprocess test"
    echo "    desc: $desc"
    echo "    test: $test_ref"
    echo
    fail=$((fail + 1))
  fi
done < <(jq -c '.sprints | to_entries[] | .value.stories | to_entries[] | .value.acceptance_criteria[]?' "$ROADMAP")

if [ "$fail" -gt 0 ]; then
  echo "ac-surface-coverage: $fail surface/test mismatch(es) across $checked surface-claiming ACs (of $total total)"
  exit 1
fi

echo "ac-surface-coverage: $checked surface-claiming ACs all match their declared surface ($total ACs total)"
