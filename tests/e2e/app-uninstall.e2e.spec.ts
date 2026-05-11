// E2E test for Sprint S65b510 Story 3 part 2 — Uninstall keeps data.
//
// Acceptance:
//   AC-S65b510-3-2 — uninstall has deleteData=false default; data survives
//                    re-install. UI surface is the uninstall confirm dialog.

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";
const ADMIN_USER = process.env.KURA_E2E_ADMIN_USER ?? "admin";
const ADMIN_PW = process.env.KURA_E2E_ADMIN_PW ?? "password";

test.beforeEach(async ({ page }) => {
  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="username"]', ADMIN_USER);
  await page.fill('input[name="password"]', ADMIN_PW);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/ui/admin/"));
});

test("[AC-S65b510-3-2] uninstall confirm dialog has delete-data unchecked by default", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui/admin/apps?tab=installed`);
  const card = page.locator('[data-testid="apps-installed-card"]').first();
  if (!(await card.isVisible().catch(() => false))) {
    test.skip(true, "No installed app on target — install one (whoami / filebrowser via dev fixture) to enable this test");
    return;
  }
  // Modal must open in-place (htmx swap), NOT a full-page navigation.
  // The earlier <a href> bug regressed the install pattern; this URL
  // assertion catches it. 2026-05-11 regression.
  const urlBefore = page.url();
  await card.locator('[data-testid="apps-uninstall-btn"]').click();
  await expect(page.locator('[data-testid="apps-uninstall-modal"]')).toBeVisible();
  expect(page.url()).toBe(urlBefore);
  const checkbox = page.locator('[data-testid="apps-uninstall-delete-data"]');
  await expect(checkbox).not.toBeChecked();
  // Sidebar must still be present — proves we're on the original page
  // with the modal overlaid, not on a bare fragment response.
  await expect(page.locator('[data-testid="sidebar-nav"]')).toBeVisible();
});
