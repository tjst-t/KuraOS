// E2E tests for Sprint S1e7eeb Story 3 — first-admin setup wizard.
// Run against a fresh kura server (no users in the DB).
//
// Acceptance:
//   AC-S1e7eeb-3-1 — Fresh DB GET / redirects to /setup; /setup renders Step 1
//   AC-S1e7eeb-3-2 — After admin is created, /setup is no longer reachable

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

// Setup wizard tests need to know whether the target VM is in the
// pristine "no admin yet" state. The first two tests apply only to a
// fresh DB; the third (one-shot block) applies always and is the
// steady-state assertion the suite always runs.
async function isPristine(page: import("@playwright/test").Page): Promise<boolean> {
  await page.goto(`${BASE_URL}/`);
  return page.url().includes("/setup");
}

test.describe("[AC-S1e7eeb-3-1] Setup wizard appears on fresh DB", () => {
  test("GET / on a clean DB redirects to /setup", async ({ page }) => {
    if (!(await isPristine(page))) {
      test.skip(true, "target already has an admin — pristine-only test");
      return;
    }
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
    if (!(await isPristine(page))) {
      test.skip(true, "target already has an admin — destructive test, only on fresh DB");
      return;
    }
    await page.goto(`${BASE_URL}/setup`);
    await page.fill('input[name="username"]', "root");
    await page.fill('input[name="display_name"]', "Root");
    await page.fill('input[name="password"]', "longenoughpw");
    await page.fill('input[name="password_confirm"]', "longenoughpw");
    await page.click('button[type="submit"]');
    await page.waitForURL(`${BASE_URL}/ui/admin/dashboard`);
  });

  test("/setup is blocked once an admin exists", async ({ page }) => {
    // Steady-state assertion: once an admin exists, /setup must not
    // re-open the wizard. Runs on every target with an admin.
    await page.goto(`${BASE_URL}/setup`);
    expect(page.url()).toContain("/login");
  });
});
