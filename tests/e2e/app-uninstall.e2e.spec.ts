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
  // Uninstall btn is an <a href> — clicking navigates to the GET endpoint
  // that renders the confirm modal as a full-page fragment.
  await card.locator('[data-testid="apps-uninstall-btn"]').click();
  await expect(page.locator('[data-testid="apps-uninstall-modal"]')).toBeVisible();
  const checkbox = page.locator('[data-testid="apps-uninstall-delete-data"]');
  await expect(checkbox).not.toBeChecked();
});
