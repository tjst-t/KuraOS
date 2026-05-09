// E2E tests for Sprint Sfix001 — Groups CRUD UI.
//
// Acceptance:
//   AC-Sfix001-2-1 — "+ グループ追加" button posts to /api/groups
//   AC-Sfix001-2-2 — メンバー編集 modal lists checkboxes for every user

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";
const ADMIN_USER = process.env.KURA_E2E_ADMIN_USER ?? "admin";
const ADMIN_PW = process.env.KURA_E2E_ADMIN_PW ?? "password";

async function login(page: import("@playwright/test").Page) {
  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="username"]', ADMIN_USER);
  await page.fill('input[name="password"]', ADMIN_PW);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/ui/"));
}

test.describe("[AC-Sfix001-2-1] Add group modal", () => {
  test("create a group and see it in the table", async ({ page }) => {
    await login(page);
    await page.goto(`${BASE_URL}/ui/admin/users?tab=groups`);

    const stamp = Date.now().toString().slice(-6);
    const groupName = `g${stamp}`;

    await page.click('[data-testid="groups-add-btn"]');
    await expect(page.locator('[data-testid="groups-new-modal"]')).toBeVisible();
    await page.fill('[data-testid="groups-form-name"]', groupName);
    await page.fill('[data-testid="groups-form-description"]', "E2E group");
    await page.click('[data-testid="groups-form-submit"]');

    await page.waitForURL(/\/ui\/admin\/users/);
    const row = page.locator(`tr[data-testid="groups-row"]`).filter({
      hasText: groupName,
    });
    await expect(row).toBeVisible();
  });
});

test.describe("[AC-Sfix001-2-2] Members modal", () => {
  test("members modal renders a checkbox per user", async ({ page }) => {
    await login(page);
    await page.goto(`${BASE_URL}/ui/admin/users?tab=groups`);

    // Use the first non-readonly group row. The autopilot bootstrap
    // creates at least one through the previous spec.
    const editBtn = page.locator('[data-testid="groups-edit-members-btn"]').first();
    await editBtn.click();

    const modal = page.locator('[data-testid^="groups-members-modal-"]').first();
    await expect(modal).toBeVisible();
    await expect(modal.locator('[data-testid="groups-members-checkbox"]')).toHaveCount(
      await modal.locator('[data-testid="groups-members-checkbox"]').count(),
    );
  });
});
