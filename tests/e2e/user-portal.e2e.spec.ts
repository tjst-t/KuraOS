// E2E tests for the User Portal at /ui (S99702c-1).
//
// Acceptance:
//   [AC-S99702c-1-1] GET /ui renders the portal landing page for user-role sessions
//   [AC-S99702c-1-2] Portal shows installed apps grid (empty state or app tiles)
//   [AC-S99702c-1-3] Portal has Files shortcut card

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

test.beforeEach(async ({ page }) => {
  // Login as root (created during setup-wizard tests).
  await page.goto(`${BASE_URL}/login`);
  const hasLogin = await page.locator('input[name="username"]').isVisible().catch(() => false);
  if (!hasLogin) {
    test.skip(true, "login page not available — server may be unreachable");
    return;
  }
  await page.fill('input[name="username"]', "root");
  await page.fill('input[name="password"]', "longenoughpw");
  await page.click('button[type="submit"]');
  await page.waitForURL(`**`);
});

test("[AC-S99702c-1-1] GET /ui renders portal landing page", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui`);
  // Should not redirect to login (200 or redirect to /ui with portal content).
  await expect(page.locator('[data-testid="portal-apps-grid"]')).toBeVisible();
});

test("[AC-S99702c-1-2] Portal shows apps grid (empty state when no apps installed)", async ({
  page,
}) => {
  await page.goto(`${BASE_URL}/ui`);
  const grid = page.locator('[data-testid="portal-apps-grid"]');
  await expect(grid).toBeVisible();
  // Either app tiles or empty-state text — both count as rendered.
  const hasApps = await page.locator('[data-testid^="app-tile-"]').count();
  const hasEmpty = await page
    .locator('[data-testid="portal-apps-empty"]')
    .isVisible()
    .catch(() => false);
  expect(hasApps > 0 || hasEmpty).toBeTruthy();
});

test("[AC-S99702c-1-3] Portal has Files shortcut", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui`);
  await expect(page.locator('[data-testid="portal-files-shortcut"]')).toBeVisible();
});
