// E2E for the singleton "Google を紐付け" button — it must appear only
// on the current user's row. Prior to the 2026-05-12 fix, every row
// rendered the same button, but /federation/google/link binds to the
// session user, so clicking takumi's row silently linked the admin.

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

test("Google link button renders on the current user's row only", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui/admin/users?tab=users`);

  // Find the admin row (the logged-in user).
  const adminRow = page.locator('[data-testid="users-row"]').filter({ hasText: ADMIN_USER });
  await expect(adminRow.locator('[data-testid="users-link-google-btn"]')).toBeVisible();

  // Every other row must NOT have a link button. The placeholder
  // "—" with users-link-google-na takes its place.
  const otherRows = page.locator('[data-testid="users-row"]').filter({ hasNotText: ADMIN_USER });
  const otherCount = await otherRows.count();
  for (let i = 0; i < otherCount; i++) {
    const row = otherRows.nth(i);
    await expect(row.locator('[data-testid="users-link-google-btn"]')).toHaveCount(0);
  }
});
