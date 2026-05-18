// E2E tests for setup wizard — Step 1 (S1e7eeb-3) + Steps 2-5 (S99702c-2).
//
// Acceptance:
//   [AC-S1e7eeb-3-1] Fresh DB GET / redirects to /setup; step 1 renders
//   [AC-S1e7eeb-3-2] After admin creation /setup is no longer reachable
//   [AC-S99702c-2-1] Steps 2-5 are accessible after admin creation
//   [AC-S99702c-2-3] /setup/done shows completion checklist + dashboard link

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

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
  test("[AC-S99702c-2-1] posting valid admin form starts wizard step 2", async ({ page }) => {
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
    // After admin creation, wizard proceeds to /setup/welcome (S99702c-2)
    await page.waitForURL(`${BASE_URL}/setup/welcome`);
    await expect(page.locator('[data-testid="setup-welcome-heading"]')).toBeVisible();
  });

  test("/setup is blocked once an admin exists", async ({ page }) => {
    // Steady-state: /setup redirects to /login once an admin exists.
    await page.goto(`${BASE_URL}/setup`);
    expect(page.url()).toContain("/login");
  });
});

test.describe("[AC-S99702c-2-3] Wizard done screen", () => {
  test("GET /setup/done shows completion checklist (requires active session)", async ({
    page,
  }) => {
    // Authenticate first so /setup/done (requireAnySession) is accessible.
    await page.goto(`${BASE_URL}/login`);
    const hasLogin = await page.locator('input[name="username"]').isVisible().catch(() => false);
    if (!hasLogin) {
      test.skip(true, "login page not available — server may be unreachable");
      return;
    }
    await page.fill('input[name="username"]', "root");
    await page.fill('input[name="password"]', "longenoughpw");
    await page.click('button[type="submit"]');
    await page.waitForURL(`**`);

    await page.goto(`${BASE_URL}/setup/done`);
    await expect(page.locator('[data-testid="setup-done-heading"]')).toBeVisible();
    await expect(page.locator('[data-testid="setup-done-dashboard-link"]')).toBeVisible();
  });
});
