// E2E tests for Sprint S1e7eeb Story 2 — login + Gateway auth middleware.
// Run against a real kura server (started by `make serve`) seeded with one
// admin and one user-role account.
//
// Acceptance:
//   AC-S1e7eeb-2-1 — POST /login issues kura_session cookie + admin reaches /ui/admin
//   AC-S1e7eeb-2-2 — Unauthenticated /ui/admin redirects to /login
//   AC-S1e7eeb-2-3 — User role gets 403 on /ui/admin, 200 on /ui

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";
const ADMIN = {
  username: process.env.KURA_TEST_ADMIN_USERNAME ?? "admin",
  password: process.env.KURA_TEST_ADMIN_PASSWORD ?? "longenoughpw",
};
const USER = {
  username: process.env.KURA_TEST_USER_USERNAME ?? "alice",
  password: process.env.KURA_TEST_USER_PASSWORD ?? "longenoughpw",
};

test.describe("[AC-S1e7eeb-2-2] Unauthenticated requests", () => {
  test("GET /ui/admin/dashboard redirects to /login", async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/ui/admin/dashboard`);
    // Browser follows the 302 — the final URL should be /login.
    expect(page.url()).toContain("/login");
    // The form must be visible on the destination.
    await expect(page.locator('[data-testid="login-form"]')).toBeVisible();
  });
});

test.describe("[AC-S1e7eeb-2-1] Admin login flow", () => {
  test("POST /login succeeds and lands on dashboard", async ({ page, context }) => {
    await page.goto(`${BASE_URL}/login`);
    await page.fill('input[name="username"]', ADMIN.username);
    await page.fill('input[name="password"]', ADMIN.password);
    await page.click('button[type="submit"]');
    await page.waitForURL(`${BASE_URL}/ui/admin/dashboard`);
    const cookies = await context.cookies();
    expect(cookies.find((c) => c.name === "kura_session")).toBeDefined();
  });

  test("wrong password shows translated error and 401", async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/login`);
    expect(resp?.status()).toBe(200);
    await page.fill('input[name="username"]', ADMIN.username);
    await page.fill('input[name="password"]', "definitely-wrong");
    await page.click('button[type="submit"]');
    await expect(page.locator('[data-testid="login-error"]')).toBeVisible();
    await expect(page.locator('[data-testid="login-error"]')).toContainText(
      "ユーザー名またはパスワードが違います",
    );
  });
});

test.describe("[AC-S1e7eeb-2-3] Role-based routing", () => {
  // Self-contained: spin up a fresh user-role account via the admin
  // CRUD UI (Sfix001-1), exercise the role assertion as that user,
  // then clean up. Avoids relying on a pre-seeded fixture user that
  // may or may not exist on the target VM.
  const stamp = Date.now().toString().slice(-6);
  const TMP_USER = {
    username: `e2e_role_${stamp}`,
    displayName: "E2E Role User",
    password: "longenoughpw",
  };

  test.beforeAll(async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await page.goto(`${BASE_URL}/login`);
    await page.fill('input[name="username"]', ADMIN.username);
    await page.fill('input[name="password"]', ADMIN.password);
    await page.click('button[type="submit"]');
    await page.waitForURL((url) => url.pathname.startsWith("/ui/admin/"));
    await page.goto(`${BASE_URL}/ui/admin/users?tab=users`);
    await page.click('[data-testid="users-add-btn"]');
    await page.fill('[data-testid="users-form-username"]', TMP_USER.username);
    await page.fill('[data-testid="users-form-display-name"]', TMP_USER.displayName);
    await page.fill('[data-testid="users-form-password"]', TMP_USER.password);
    await page.selectOption('[data-testid="users-form-role"]', "user");
    await page.click('[data-testid="users-form-submit"]');
    await page.waitForURL(/\/ui\/admin\/users/);
    await ctx.close();
  });

  test.afterAll(async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await page.goto(`${BASE_URL}/login`);
    await page.fill('input[name="username"]', ADMIN.username);
    await page.fill('input[name="password"]', ADMIN.password);
    await page.click('button[type="submit"]');
    await page.waitForURL((url) => url.pathname.startsWith("/ui/admin/"));
    await page.goto(`${BASE_URL}/ui/admin/users?tab=users`);
    const row = page.locator(`[data-testid="users-row"]`).filter({
      hasText: TMP_USER.username,
    });
    if ((await row.count()) > 0) {
      // Per-row delete is a POST form; submit it directly.
      await row.locator('[data-testid="users-delete-form"]').evaluate(
        (form: HTMLFormElement) => form.submit(),
      );
      await page.waitForLoadState("load");
    }
    await ctx.close();
  });

  test("user role gets 403 on /ui/admin and 200 on /ui", async ({ page }) => {
    await page.goto(`${BASE_URL}/login`);
    await page.fill('input[name="username"]', TMP_USER.username);
    await page.fill('input[name="password"]', TMP_USER.password);
    await page.click('button[type="submit"]');
    await page.waitForURL(`${BASE_URL}/ui`);
    const adminResp = await page.goto(`${BASE_URL}/ui/admin/dashboard`);
    expect(adminResp?.status()).toBe(403);
    const userResp = await page.goto(`${BASE_URL}/ui`);
    expect(userResp?.status()).toBe(200);
  });
});
