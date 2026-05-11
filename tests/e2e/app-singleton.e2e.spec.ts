// E2E for the singleton-per-app invariant (2026-05-11).
//
// design.md uses dataset paths tank/apps/<name>/<dataset> with no
// install-instance suffix → only one install per manifest.Name is
// supported. The Store grid surfaces this by rendering an installed
// card with a disabled "インストール済" badge instead of the Install
// button. Attempting the install endpoint anyway must be rejected by
// the server-side guard.

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";
const ADMIN_USER = process.env.KURA_E2E_ADMIN_USER ?? "admin";
const ADMIN_PW = process.env.KURA_E2E_ADMIN_PW ?? "password";

test.beforeEach(async ({ page }) => {
  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="username"]', ADMIN_USER);
  await page.fill('input[name="password"]', ADMIN_PW);
  await page.click('button[type="submit"]');
  await page.waitForURL((u) => u.pathname.startsWith("/ui/admin/"));
});

test("Store card of an already-installed app shows installed badge, not install button", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui/admin/apps?tab=installed`);
  const firstInstalled = page.locator('[data-testid="apps-installed-card"]').first();
  if (!(await firstInstalled.isVisible().catch(() => false))) {
    test.skip(true, "No installed app on target — fixture registry install needed");
    return;
  }
  const installedName = await firstInstalled.getAttribute("data-app-name");
  expect(installedName).not.toBeNull();

  await page.goto(`${BASE_URL}/ui/admin/apps?tab=store`);
  const storeCard = page.locator(`[data-testid="apps-store-card"][data-app-name="${installedName}"]`);
  await expect(storeCard).toBeVisible();
  // Disabled "installed" badge present, install button absent on this card.
  await expect(storeCard.locator('[data-testid="apps-store-installed"]')).toBeVisible();
  await expect(storeCard.locator('[data-testid="apps-store-installed"]')).toBeDisabled();
  await expect(storeCard.locator('[data-testid="apps-install-btn"]')).toHaveCount(0);
});
