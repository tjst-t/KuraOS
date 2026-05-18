// E2E tests for Sprint Se1e7a6 Story 2 — BackupBackend CRUD (Settings tab).
//
// Acceptance criteria exercised:
//   [AC-Se1e7a6-2-1] zfs_send / restic / rclone の 3 種類の backend 設定が
//                     config.json に書け、Settings 画面から編集できる
//
// Run against a live kura server at KURA_BASE_URL.
// Admin: admin / password (VM test fixture).

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

async function gotoBackupTab(page: Page) {
  await page.goto(`${BASE_URL}/ui/admin/settings?tab=backup`);
  // Wait for the backup section to load
  await expect(page.locator('[data-testid="backup-section-title"]')).toBeVisible({ timeout: 10000 });
}

test.describe("[AC-Se1e7a6-2-1] Backup backend CRUD via Settings UI", () => {
  test("Backup tab is reachable and shows schedules + backends sections", async ({ page }) => {
    await loginAsAdmin(page);
    await gotoBackupTab(page);

    // Schedules card
    await expect(page.locator("#schedules-card")).toBeVisible();
    // Backends card
    await expect(page.locator("#backends-card")).toBeVisible();
    // Upgrade card
    await expect(page.locator("#upgrade-card")).toBeVisible();
  });

  test("Settings tab navigation includes Backup tab", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/admin/settings`);
    // Backup tab button should be present
    await expect(page.locator('[data-testid="settings-tab-backup"]')).toBeVisible();
  });

  test("Can add a restic backend and see it in the table", async ({ page }) => {
    await loginAsAdmin(page);
    await gotoBackupTab(page);

    // Click the add backend button
    await page.click('[data-testid="add-backend-btn"]');

    // Form should appear
    await expect(page.locator('[data-testid="backend-form"]')).toBeVisible({ timeout: 5000 });

    // Fill in the form
    const uniqueName = `e2e-restic-${Date.now()}`;
    await page.fill('[data-testid="backend-name-input"]', uniqueName);
    await page.selectOption('[data-testid="backend-kind-select"]', "restic");
    await page.fill('[data-testid="backend-repo-input"]', "s3:test-bucket/kura");

    // Submit
    await page.click('[data-testid="backend-form-save"]');

    // Backends table should now include the new entry
    await expect(page.locator('[data-testid="backends-table"]')).toBeVisible({ timeout: 5000 });
    await expect(page.locator(`td.td-mono`).filter({ hasText: uniqueName })).toBeVisible();
  });

  test("Can add a zfs_send backend", async ({ page }) => {
    await loginAsAdmin(page);
    await gotoBackupTab(page);

    await page.click('[data-testid="add-backend-btn"]');
    await expect(page.locator('[data-testid="backend-form"]')).toBeVisible({ timeout: 5000 });

    const uniqueName = `e2e-zfssend-${Date.now()}`;
    await page.fill('[data-testid="backend-name-input"]', uniqueName);
    await page.selectOption('[data-testid="backend-kind-select"]', "zfs_send");
    await page.fill('[data-testid="backend-host-input"]', "user@192.168.1.50");

    await page.click('[data-testid="backend-form-save"]');
    await expect(page.locator(`td.td-mono`).filter({ hasText: uniqueName })).toBeVisible({ timeout: 5000 });
  });

  test("Can add an rclone backend", async ({ page }) => {
    await loginAsAdmin(page);
    await gotoBackupTab(page);

    await page.click('[data-testid="add-backend-btn"]');
    await expect(page.locator('[data-testid="backend-form"]')).toBeVisible({ timeout: 5000 });

    const uniqueName = `e2e-rclone-${Date.now()}`;
    await page.fill('[data-testid="backend-name-input"]', uniqueName);
    await page.selectOption('[data-testid="backend-kind-select"]', "rclone");
    await page.fill('[data-testid="backend-remote-input"]', "gdrive:backups/kura");

    await page.click('[data-testid="backend-form-save"]');
    await expect(page.locator(`td.td-mono`).filter({ hasText: uniqueName })).toBeVisible({ timeout: 5000 });
  });

  test("Can delete a backend", async ({ page }) => {
    await loginAsAdmin(page);
    await gotoBackupTab(page);

    // Create one first
    await page.click('[data-testid="add-backend-btn"]');
    await expect(page.locator('[data-testid="backend-form"]')).toBeVisible({ timeout: 5000 });
    const uniqueName = `e2e-delete-be-${Date.now()}`;
    await page.fill('[data-testid="backend-name-input"]', uniqueName);
    await page.selectOption('[data-testid="backend-kind-select"]', "restic");
    await page.fill('[data-testid="backend-repo-input"]', "s3:test/kura");
    await page.click('[data-testid="backend-form-save"]');
    await expect(page.locator(`td.td-mono`).filter({ hasText: uniqueName })).toBeVisible({ timeout: 5000 });

    // Find the delete button for our new row and click it
    const row = page.locator('tr').filter({ hasText: uniqueName });
    page.on('dialog', dialog => dialog.accept());
    await row.locator('[data-testid^="delete-backend-"]').click();

    // Row should disappear
    await expect(page.locator(`td.td-mono`).filter({ hasText: uniqueName })).not.toBeVisible({ timeout: 5000 });
  });
});
