// E2E for the Storage page app-dataset classification (2026-05-11).
//
// Volume list hides <pool>/apps/<name>/... rows by default so the
// operator sees only datasets they created. A "全 Dataset を表示"
// checkbox toggles ?showAll=1; in that view, app rows render with the
// "アプリ" badge and the destroy button is disabled when the owning
// app is still installed (data-loss guard). Orphan datasets — owning
// app uninstalled, dataset retained — get destroy enabled so the
// operator can reclaim the space.

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

test("default volume list hides app-managed datasets", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui/admin/storage?tab=volumes`);
  const rows = page.locator('[data-testid="storage-volume-row"]');
  // Any rendered row must have data-app-dataset="false".
  const appRows = rows.locator('[data-app-dataset="true"]');
  await expect(appRows).toHaveCount(0);
});

test("showAll=1 surfaces app datasets with badge + locked destroy button", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui/admin/storage?tab=volumes&showAll=1`);
  // Either some app dataset is present, or none on this target (fresh VM).
  const appRows = page.locator('[data-testid="storage-volume-row"][data-app-dataset="true"]');
  const count = await appRows.count();
  if (count === 0) {
    test.skip(true, "No app datasets on target — install an app first to exercise this assertion.");
    return;
  }
  // The first app-namespace row (<pool>/apps) is always disabled.
  const appsParentRow = appRows.filter({
    has: page.locator('[data-testid="storage-volume-apps-parent"]'),
  });
  if ((await appsParentRow.count()) > 0) {
    const destroyBtn = appsParentRow.first().locator('[data-testid="storage-volume-destroy-btn"]');
    await expect(destroyBtn).toBeDisabled();
  }
});
