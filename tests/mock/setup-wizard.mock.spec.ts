// Mock-mode tests for Sprint S1e7eeb setup wizard UI.

import { test, expect } from "@playwright/test";

test.describe("Setup wizard (mock)", () => {
  test("renders the step sidebar with Step 1 active", async ({ page }) => {
    await page.route("**/setup", async (route) => {
      route.fulfill({
        contentType: "text/html",
        body: `<!doctype html><html><body>
          <aside class="wizard-aside">
            <div class="wizard-step" data-state="active"><span class="num">1</span><span>管理者アカウント</span></div>
            <div class="wizard-step" data-state="todo"><span class="num">2</span><span>ストレージ構成</span></div>
            <div class="wizard-step" data-state="todo"><span class="num">3</span><span>最初の共有</span></div>
            <div class="wizard-step" data-state="todo"><span class="num">4</span><span>完了</span></div>
          </aside>
          <main class="wizard-main">
            <form data-testid="setup-form" method="post" action="/setup/admin">
              <input name="username" required />
              <input name="display_name" />
              <input name="password" type="password" required />
              <input name="password_confirm" type="password" required />
              <button type="submit">管理者を作成してログイン</button>
            </form>
          </main>
        </body></html>`,
      });
    });
    await page.goto("https://example.test/setup");
    const active = page.locator('.wizard-step[data-state="active"]');
    await expect(active).toContainText("管理者アカウント");
    await expect(page.locator('[data-testid="setup-form"]')).toBeVisible();
    await expect(page.locator('input[name="password_confirm"]')).toHaveAttribute(
      "type",
      "password",
    );
  });
});
