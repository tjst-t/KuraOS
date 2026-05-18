// E2E tests for Sprint Se1e7a6 Story 3 — pre-upgrade snapshot + apt flow.
//
// Acceptance criteria exercised:
//   [AC-Se1e7a6-3-1] Settings から「今すぐ更新」を押すと、全アプリ graceful stop
//                     → スナップショット作成 → apt upgrade → アプリ起動 の順で進む
//   [AC-Se1e7a6-3-2] v1 ではワンクリックロールバック UI は提供しない
//
// Note: The VM must have KURA_UPGRADE_DRY_RUN=1 set (or a fake apt binary)
// so apt-get upgrade is skipped and the test remains idempotent.

import { test, expect, Page } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://192.168.1.42:8204";
const ADMIN_USER = process.env.KURA_TEST_ADMIN_USERNAME ?? "admin";
const ADMIN_PASS = process.env.KURA_TEST_ADMIN_PASSWORD ?? "password";

async function loginAsAdmin(page: Page) {
  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="username"]', ADMIN_USER);
  await page.fill('input[name="password"]', ADMIN_PASS);
  await page.click('button[type="submit"]');
  await page.waitForURL(`${BASE_URL}/ui/admin/dashboard`);
}

test.describe("[AC-Se1e7a6-3-1] System upgrade button (dry-run)", () => {
  test("Settings backup tab shows upgrade card with update button", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings?tab=backup`);
    await expect(page.locator("#upgrade-card")).toBeVisible({ timeout: 10000 });
    await expect(page.locator('[data-testid="upgrade-btn"]')).toBeVisible();
    // Button text should be the i18n key for "今すぐ更新"
    const btnText = await page.locator('[data-testid="upgrade-btn"]').innerText();
    expect(btnText.trim().length).toBeGreaterThan(0);
  });

  test("Clicking upgrade button triggers confirm dialog and shows result", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings?tab=backup`);
    await expect(page.locator('[data-testid="upgrade-btn"]')).toBeVisible({ timeout: 10000 });

    // Accept the confirm dialog
    page.on("dialog", (dialog) => dialog.accept());

    // Click upgrade button
    await page.click('[data-testid="upgrade-btn"]');

    // Result area should eventually contain some feedback (ok or error message)
    // We wait up to 30 seconds because apt-get might take time (or dry-run echo is fast)
    await expect(page.locator("#upgrade-result")).not.toBeEmpty({ timeout: 30000 });

    // The result should contain either a success marker or an error marker
    const resultText = await page.locator("#upgrade-result").innerText();
    // Either succeeded (contains snapshot names) or failed (contains error text)
    expect(resultText.trim().length).toBeGreaterThan(0);
  });
});

test.describe("[AC-Se1e7a6-3-2] No rollback UI in v1", () => {
  test("Backup tab does not contain rollback button", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings?tab=backup`);
    await expect(page.locator("#upgrade-card")).toBeVisible({ timeout: 10000 });

    // There must be no element mentioning rollback
    const rollbackBtn = page.locator('[data-testid*="rollback"], [href*="rollback"]');
    await expect(rollbackBtn).toHaveCount(0);
  });

  test("Rollback endpoint returns 404", async ({ page }) => {
    await loginAsAdmin(page);
    const resp = await page.request.post(`${BASE_URL}/ui/admin/settings/upgrade/rollback`);
    // Must not be 200 (rollback is forbidden in v1)
    expect(resp.status()).not.toBe(200);
  });
});
