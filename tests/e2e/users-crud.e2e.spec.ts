// E2E tests for Sprint Sfix001 — Users CRUD UI.
//
// Run against a kura server with an admin already created (the autopilot
// VM bootstrap covers this). Tests are idempotent: each one creates a
// uniquely-named user / cleans up via the same UI.
//
// Acceptance:
//   AC-Sfix001-1-1 — "+ ユーザー追加" button -> modal -> POST /api/users
//   AC-Sfix001-1-2 — per-row 編集 / 削除 + admin self-delete blocked

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

test.describe("[AC-Sfix001-1-1] Users tab + add user modal", () => {
  test("add a user via the modal and see it in the table", async ({ page }) => {
    await login(page);
    await page.goto(`${BASE_URL}/ui/admin/users?tab=users`);

    const stamp = Date.now().toString().slice(-6);
    const username = `e2e${stamp}`;

    await page.click('[data-testid="users-add-btn"]');
    await expect(page.locator('[data-testid="users-new-modal"]')).toBeVisible();
    await page.fill('[data-testid="users-form-username"]', username);
    await page.fill('[data-testid="users-form-display-name"]', "E2E User");
    await page.fill('[data-testid="users-form-password"]', "longenoughpw");
    await page.selectOption('[data-testid="users-form-role"]', "user");
    await page.click('[data-testid="users-form-submit"]');

    await page.waitForURL(/\/ui\/admin\/users/);
    const row = page.locator(`tr[data-testid="users-row"]`).filter({
      hasText: username,
    });
    await expect(row).toBeVisible();
  });
});

test.describe("[AC-Sfix001-1-2] Row actions + self-delete guard", () => {
  test("admin row delete is refused with a banner", async ({ page }) => {
    await login(page);
    await page.goto(`${BASE_URL}/ui/admin/users?tab=users`);

    page.on("dialog", (d) => d.accept()); // ack the confirm()
    const adminRow = page
      .locator(`tr[data-testid="users-row"]`)
      .filter({ hasText: ADMIN_USER });
    await adminRow.locator('[data-testid="users-delete-btn"]').click();

    await page.waitForURL(/\/ui\/admin\/users/);
    await expect(page.locator('[data-testid="users-error"]')).toBeVisible();
    // The admin row must still be present.
    await expect(adminRow).toBeVisible();
  });
});
