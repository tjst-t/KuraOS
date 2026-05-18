// E2E tests for the Settings Language tab (S99702c-3-1).
//
// Acceptance:
//   [AC-S99702c-3-1] Language tab renders ja selected, en disabled with v1.x badge
//   Saving with ja selected shows a success banner

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

test.beforeEach(async ({ page }) => {
  await page.goto(`${BASE_URL}/login`);
  const hasLogin = await page.locator('input[name="username"]').isVisible().catch(() => false);
  if (!hasLogin) {
    test.skip(true, "login page not available");
    return;
  }
  await page.fill('input[name="username"]', "root");
  await page.fill('input[name="password"]', "longenoughpw");
  await page.click('button[type="submit"]');
  await page.waitForURL(`**`);
});

test("[AC-S99702c-3-1] Language tab renders with ja selected, en disabled", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui/admin/settings?tab=language`);
  await expect(page.locator('[data-testid="language-card"]')).toBeVisible();
  // ja radio is checked
  await expect(page.locator('[data-testid="locale-ja-radio"]')).toBeChecked();
  // en radio is disabled
  await expect(page.locator('[data-testid="locale-en-radio"]')).toBeDisabled();
  // v1.x badge appears near en option
  await expect(page.locator('[data-testid="language-card"]')).toContainText("v1.x");
});

test("[AC-S99702c-3-1] Saving language shows success banner", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui/admin/settings?tab=language`);
  await page.click('[data-testid="save-language-btn"]');
  await expect(page.locator('[data-testid="lang-saved-banner"]')).toBeVisible();
});
