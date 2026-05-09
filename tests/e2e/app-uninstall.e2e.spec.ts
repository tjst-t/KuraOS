// E2E test for Sprint S65b510 Story 3 part 2 — Uninstall keeps data.
//
// Acceptance:
//   AC-S65b510-3-2 — uninstall has deleteData=false default; data survives
//                    re-install. UI surface is the uninstall confirm dialog.

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

test("[AC-S65b510-3-2] uninstall confirm dialog has delete-data unchecked by default", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui/admin/apps?tab=installed`);
  const card = page.locator('[data-testid="apps-installed-card"]').first();
  if (!(await card.isVisible().catch(() => false))) {
    test.skip(true, "No installed app to uninstall — engine test covers this AC's data invariant");
  }
  await card.locator('[data-testid="apps-uninstall-btn"]').click();
  await expect(page.locator('[data-testid="apps-uninstall-modal"]')).toBeVisible();
  const checkbox = page.locator('[data-testid="apps-uninstall-delete-data"]');
  await expect(checkbox).not.toBeChecked();
});
