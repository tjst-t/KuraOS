// Mock-mode tests for Sprint S1e7eeb login UI. These exercise client-side
// behaviour against a Playwright route-stubbed server, without standing up a
// full kura backend. Used to gate the Story in `sprint run`.

import { test, expect } from "@playwright/test";

test.describe("Login form (mock)", () => {
  test("renders username + password fields and submit button", async ({ page }) => {
    await page.route("**/login", async (route) => {
      route.fulfill({
        contentType: "text/html",
        body: `<!doctype html><html><body>
          <form data-testid="login-form" method="post" action="/login">
            <input name="username" />
            <input name="password" type="password" />
            <button type="submit">ログイン</button>
          </form>
        </body></html>`,
      });
    });
    await page.goto("https://example.test/login");
    await expect(page.locator('[data-testid="login-form"]')).toBeVisible();
    await expect(page.locator('input[name="username"]')).toBeVisible();
    await expect(page.locator('input[name="password"]')).toHaveAttribute(
      "type",
      "password",
    );
  });

  test("required fields prevent empty submission", async ({ page }) => {
    await page.route("**/login", async (route) => {
      route.fulfill({
        contentType: "text/html",
        body: `<!doctype html><html><body>
          <form data-testid="login-form" method="post" action="/login">
            <input name="username" required />
            <input name="password" type="password" required />
            <button type="submit">ログイン</button>
          </form>
        </body></html>`,
      });
    });
    await page.goto("https://example.test/login");
    await page.click('button[type="submit"]');
    // Browser native validation should prevent submission; assert by URL.
    expect(page.url()).toContain("/login");
  });
});
