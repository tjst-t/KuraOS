// E2E for Sprint Sfix002 Story 2 — /login の「Google でログイン」ボタン.
//
// Acceptance:
//   AC-Sfix002-2-1 — federation provider が enabled なら button が表示
//                   (disabled なら非表示)
//   AC-Sfix002-2-2 — クリック → /federation/google/start に遷移
//                   (mock IdP 経由で session 発行 + role-safe redirect)
//   AC-Sfix002-2-3 — divider「または」と Google ロゴ alt-text が見える
//
// Skips when /federation/google/start returns 404 (no provider env wired).
// The VM verification config sets KURA_FED_GOOGLE_* against the standalone
// mock IdP at :9998 so this passes on the dev VM end-to-end.

import { test, expect, request } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

async function federationConfigured(): Promise<boolean> {
  const ctx = await request.newContext();
  const r = await ctx.get(`${BASE_URL}/federation/google/start`, { maxRedirects: 0 });
  await ctx.dispose();
  return r.status() === 302;
}

test.describe("[AC-Sfix002-2-1] Google button visibility", () => {
  test("button renders on /login when google provider is enabled", async ({ page }) => {
    if (!(await federationConfigured())) {
      test.skip(true, `No federation provider on ${BASE_URL}; set KURA_FED_GOOGLE_*.`);
      return;
    }
    await page.goto(`${BASE_URL}/login`);
    await expect(page.locator('[data-testid="login-google-btn"]')).toBeVisible();
    await expect(page.locator('[data-testid="login-fed-divider"]')).toContainText("または");
    // [AC-Sfix002-2-3] visual spec: Google ロゴが alt-text/title 付きで render される.
    const logo = page.locator('[data-testid="login-google-logo"]');
    await expect(logo).toBeVisible();
    await expect(logo.locator("title")).toHaveText(/Google/);
    await expect(page.locator('[data-testid="login-google-btn"]')).toContainText(
      "Google でログイン",
    );
  });
});

test.describe("[AC-Sfix002-2-2] Google button click → federation start", () => {
  test("clicking the button initiates the federation flow", async ({ page }) => {
    if (!(await federationConfigured())) {
      test.skip(true, `No federation provider on ${BASE_URL}; set KURA_FED_GOOGLE_*.`);
      return;
    }
    await page.goto(`${BASE_URL}/login`);
    // Don't auto-follow into the live IdP — assert the *first* navigation
    // target is /federation/google/start (then the IdP redirect itself
    // is exercised by google-federation-flow.e2e.spec.ts).
    const [resp] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/federation/google/start")),
      page.click('[data-testid="login-google-btn"]'),
    ]);
    expect([302, 200, 503]).toContain(resp.status());
  });
});
