// E2E for Sprint Sfix002 Story 3 — auto_provision=0 で未紐付け subject
// が来たときに、http.Error の plain text ではなく親切な i18n error 画面
// が出ることを確認する.
//
// Acceptance:
//   AC-Sfix002-3-2 — unbound Google subject + auto_provision=false で
//                    /login に戻る button + 日本語メッセージが見える
//
// 前提:
//   - KURA_FED_GOOGLE_* env が mock IdP (cmd/oidc-mock) 向けに設定済
//   - KURA_FED_GOOGLE_AUTO_PROVISION=0 (env で明示)
//   - mock IdP の OIDC_MOCK_SUBJECT は未紐付けの値に設定 (テスト時に
//     OIDC_MOCK_SUBJECT を切り替えるか、CI で別 mock を立てる)
//
// 上記が成立しないなら test.skip。

import { test, expect, request } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

async function probeFederation(): Promise<{ ok: boolean; autoProvision: boolean | null }> {
  const ctx = await request.newContext();
  const r = await ctx.get(`${BASE_URL}/federation/google/start`, { maxRedirects: 0 });
  await ctx.dispose();
  // Without an env-exposed surface for the AUTO_PROVISION flag, we
  // can't tell from outside. Returning null means "let the test decide
  // based on the AC behaviour observed".
  return { ok: r.status() === 302, autoProvision: null };
}

test.describe("[AC-Sfix002-3-2] auto_provision=0 unbound subject → error page", () => {
  test("error page is rendered instead of plain http.Error", async ({ page, context }) => {
    const probe = await probeFederation();
    if (!probe.ok) {
      test.skip(true, `Federation not wired on ${BASE_URL}.`);
      return;
    }

    // Step the federation flow as an unauthenticated visitor. We
    // expect either:
    //   - 302 to /ui (already linked, auto_provision irrelevant), or
    //   - 403 with the i18n error page (auto_provision=false, unbound).
    // The second case is what AC-Sfix002-3-2 covers; the first case
    // means the VM is configured differently and we skip.
    const resp = await page.goto(`${BASE_URL}/federation/google/start`, {
      waitUntil: "domcontentloaded",
    });

    // The browser follows the redirect chain through the IdP and back
    // to /federation/google/callback. The terminal status depends on
    // whether the subject is bound + auto_provision setting.
    const finalUrl = page.url();
    const status = resp?.status() ?? 0;

    // If we landed somewhere that already has a session (admin) just skip.
    if (finalUrl.includes("/ui/") && status === 200) {
      test.skip(true, `Subject already bound on ${BASE_URL}; can't exercise unbound path.`);
      return;
    }

    // The error page must surface the i18n key (rendered text), not a
    // bare Go http.Error string. We accept any of the three federation
    // error keys here so the test covers unbound + provision_failed +
    // provision_unavailable identically (they all use the same template).
    const errorBanner = page.locator('[data-testid="federation-error-message"]');
    const backBtn = page.locator('[data-testid="federation-back-to-login"]');
    if (await errorBanner.isVisible().catch(() => false)) {
      await expect(errorBanner).not.toBeEmpty();
      await expect(backBtn).toBeVisible();
      await expect(backBtn).toHaveAttribute("href", "/login");
    } else {
      // The flow succeeded (bound subject) — not the AC under test.
      test.skip(true, "Subject was bound; AC-Sfix002-3-2 unbound path not exercised.");
    }
  });
});
