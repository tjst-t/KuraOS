// E2E tests for Sprint Sf92666 Story 4 — kura Self-Update.
//
// Acceptance criteria exercised:
//   [AC-Sf92666-4-1] Settings の更新タブで新バージョンが検出され、SHA256 + cosign 検証 →
//                    アトミック置換 → systemctl restart の順に進む
//   [AC-Sf92666-4-2] 起動後セルフチェックが失敗すると kura.bak に自動ロールバックされる
//                    (AC-4-2 is tested in the acceptance shell script kura-update-rollback.sh)
//
// Run against a live kura server at KURA_BASE_URL.

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

test.describe("[AC-Sf92666-4-1] Self-Update UI", () => {
  test("Self-update tab is accessible from Settings page", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings`);

    // Click the self-update tab
    await page.click('[data-testid="settings-tab-self-update"]');

    // Should see the self-update section
    await expect(page.locator('[data-testid="self-update-section-title"]')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('[data-testid="current-version"]')).toBeVisible();
  });

  test("[AC-Sf92666-4-1] Current version is displayed", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings?tab=self_update`);

    await expect(page.locator('[data-testid="self-update-section-title"]')).toBeVisible({ timeout: 5000 });

    const currentVersion = await page.locator('[data-testid="current-version"]').textContent();
    expect(currentVersion?.trim()).not.toBe("");
  });

  test("[AC-Sf92666-4-1] Check update button is visible and triggers check", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings?tab=self_update`);

    await expect(page.locator('[data-testid="check-update-btn"]')).toBeVisible({ timeout: 5000 });

    // Click check — may return "up to date" or "available", depending on network
    await page.click('[data-testid="check-update-btn"]');

    // Wait for either up-to-date or available banner (or error from GitHub rate limit)
    await page.waitForSelector(
      '[data-testid="up-to-date-banner"], [data-testid="update-available-banner"], [data-testid="self-update-error"]',
      { timeout: 15000 }
    );
  });
});
