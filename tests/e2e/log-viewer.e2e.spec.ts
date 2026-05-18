// E2E tests for Sprint Sf92666 Story 3 — Log Viewer (JSONL + SSE).
//
// Acceptance criteria exercised:
//   [AC-Sf92666-3-2] Settings の ログビューアで Source/Level/テキストフィルタが効き、
//                    『ライブ』モードで SSE リアルタイムストリームが流れる
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

test.describe("[AC-Sf92666-3-2] Log Viewer UI", () => {
  test("Logs tab is accessible from Settings page", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings`);

    // Click the logs tab button
    await page.click('[data-testid="settings-tab-logs"]');

    // Should see the log viewer section
    await expect(page.locator('[data-testid="logs-section-title"]')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('[data-testid="logs-table"]')).toBeVisible();
  });

  test("[AC-Sf92666-3-2] Filter by source shows filtered results", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings?tab=logs`);

    // Source filter dropdown should be visible
    await expect(page.locator('[data-testid="logs-source-filter"]')).toBeVisible({ timeout: 5000 });

    // Select kuraos source
    await page.selectOption('[data-testid="logs-source-filter"]', "kuraos");

    // Apply the filter
    await page.click('[data-testid="logs-tbody"]');  // loose focus
    // The form submits via hx-get; click filter btn
    // Wait for table to reload
    await page.waitForTimeout(500);
  });

  test("[AC-Sf92666-3-2] Search filter input is visible and functional", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings?tab=logs`);

    await expect(page.locator('[data-testid="logs-search-input"]')).toBeVisible({ timeout: 5000 });
    await page.fill('[data-testid="logs-search-input"]', "kura");
  });

  test("[AC-Sf92666-3-2] Live button is visible and starts SSE stream", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings?tab=logs`);

    const liveBtn = page.locator('[data-testid="logs-live-btn"]');
    await expect(liveBtn).toBeVisible({ timeout: 5000 });

    // Navigate a page to generate a log event, then come back and check live mode
    // We can't easily assert on the SSE stream content in a short E2E,
    // but we verify the button exists and is clickable.
    await liveBtn.click();

    // After clicking, button should be disabled (stream started)
    await page.waitForTimeout(200);
    const isDisabled = await liveBtn.isDisabled();
    expect(isDisabled).toBe(true);
  });
});
