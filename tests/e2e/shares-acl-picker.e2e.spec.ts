// E2E tests for Sprint Sfix001-3 — Share ACL row picker.
//
// Acceptance:
//   AC-Sfix001-3-1 — create modal renders a row picker with kind / name /
//                    mode dropdowns + "+ ユーザー / グループを追加" button
//   AC-Sfix001-3-2 — edit modal pre-fills the picker with the existing ACL

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

test.describe("[AC-Sfix001-3-1] Create-share modal: ACL picker", () => {
  test("renders empty picker + add row button", async ({ page }) => {
    await login(page);
    await page.goto(`${BASE_URL}/ui/admin/shares`);
    await page.click('[data-testid="shares-new-btn"]');
    await expect(page.locator('[data-testid="shares-new-modal"]')).toBeVisible();

    // Picker container + add button must be present. The container
    // itself is empty until the operator clicks add (no rendered
    // children), so an empty flex container has 0 height — assert it
    // exists in the DOM rather than visible.
    await expect(page.locator('[data-testid="shares-acl-rows"]')).toBeAttached();
    const addBtn = page.locator('[data-testid="shares-acl-add-btn"]');
    await expect(addBtn).toBeVisible();

    // Click "+ 追加" to fetch a row via htmx, then check the row's controls.
    await addBtn.click();
    const row = page.locator('[data-testid="shares-acl-row"]').first();
    await expect(row).toBeVisible();
    await expect(row.locator('[data-testid="shares-acl-kind"]')).toBeVisible();
    await expect(row.locator('[data-testid="shares-acl-name"]')).toBeVisible();
    await expect(row.locator('[data-testid="shares-acl-mode"]')).toBeVisible();

    // × button removes the row.
    await row.locator('[data-testid="shares-acl-remove"]').click();
    await expect(row).toBeHidden();
  });
});

test.describe("[AC-Sfix001-3-2] Edit-share modal pre-fills the picker", () => {
  test("opens edit modal with existing ACL rows visible", async ({ page }) => {
    await login(page);
    await page.goto(`${BASE_URL}/ui/admin/shares`);
    // Pick the first row so the right-hand detail card opens.
    const firstRow = page.locator('[data-testid="shares-row"]').first();
    if ((await firstRow.count()) === 0) {
      test.skip(true, "no shares present yet — bootstrap fixture missing");
      return;
    }
    await firstRow.click();
    await page.waitForURL(/selected=/);

    await page.click('[data-testid="shares-edit-btn"]');
    await expect(page.locator('[data-testid="shares-edit-modal"]')).toBeVisible();
    await expect(page.locator('[data-testid="shares-edit-acl-rows"]')).toBeVisible();
  });
});
