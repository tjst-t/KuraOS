// E2E tests for Sprint S1e7eeb Story 3 — first-admin setup wizard.
// Run against a fresh kura server (no users in the DB).
//
// Acceptance:
//   AC-S1e7eeb-3-1 — Fresh DB GET / redirects to /setup; /setup renders Step 1
//   AC-S1e7eeb-3-2 — After admin is created, /setup is no longer reachable

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

test.describe("[AC-S1e7eeb-3-1] Setup wizard appears on fresh DB", () => {
  test("GET / on a clean DB redirects to /setup", async ({ page }) => {
    await page.goto(`${BASE_URL}/`);
    expect(page.url()).toContain("/setup");
    await expect(page.locator('[data-testid="setup-form"]')).toBeVisible();
    // Step indicator: only "管理者アカウント" is active.
    await expect(page.locator('.wizard-step[data-state="active"]')).toContainText(
      "管理者アカウント",
    );
  });
});

test.describe("[AC-S1e7eeb-3-2] Setup is one-shot", () => {
  test("posting valid admin form lands on /ui/admin/dashboard", async ({ page }) => {
    await page.goto(`${BASE_URL}/setup`);
    await page.fill('input[name="username"]', "root");
    await page.fill('input[name="display_name"]', "Root");
    await page.fill('input[name="password"]', "longenoughpw");
    await page.fill('input[name="password_confirm"]', "longenoughpw");
    await page.click('button[type="submit"]');
    await page.waitForURL(`${BASE_URL}/ui/admin/dashboard`);
  });

  test("/setup is blocked once an admin exists", async ({ page }) => {
    // Assumes the previous test (or a fixture) created the admin. After
    // creation, GET /setup is gated by requireNoAdmin and redirects to /login.
    await page.goto(`${BASE_URL}/setup`);
    expect(page.url()).toContain("/login");
  });
});
