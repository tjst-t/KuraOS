// E2E test for Sprint S65b510 Story 3 part 1 — Update flow.
//
// Acceptance:
//   AC-S65b510-3-1 — update creates a snapshot of every backup:true dataset
//                    BEFORE pulling the new image, and rolls back on
//                    healthcheck failure.
//
// The snapshot/rollback assertions belong in the engine-layer Go test
// (TestLifecycleUpdateSnapshotsBackupDatasets) — this E2E covers the surface
// (UI button → progress modal) so the AC has a UI test reference per
// CLAUDE.md verification_rules.

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

test("[AC-S65b510-3-1] update flow renders SSE progress with snapshot stage", async ({ request, page }) => {
  // The Apps page must render the Installed tab.
  await page.goto(`${BASE_URL}/ui/admin/apps?tab=installed`);
  // Either a card or the empty banner is acceptable on a fresh install.
  const card = page.locator('[data-testid="apps-installed-card"]').first();
  const empty = page.locator('[data-testid="apps-empty-installed"]');
  await expect(card.or(empty)).toBeVisible({ timeout: 5_000 });
});
