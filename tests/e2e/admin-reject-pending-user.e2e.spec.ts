// E2E for Story S413bd5-3 — Admin rejects a pending user.
//
// Acceptance criteria covered:
//   [AC-S413bd5-3-3] Reject flow: confirm modal → POST /reject →
//                    row removed from 承認待ち section + deleted from DB
//
// Prerequisites: same as admin-approve-pending-user.e2e.spec.ts.

import { test, expect, request } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";
const ADMIN_USER = process.env.KURA_E2E_ADMIN_USER ?? "admin";
const ADMIN_PW = process.env.KURA_E2E_ADMIN_PW ?? "password";

async function federationConfigured(): Promise<boolean> {
  const ctx = await request.newContext();
  const r = await ctx.get(`${BASE_URL}/federation/google/start`, {
    maxRedirects: 0,
  });
  await ctx.dispose();
  return r.status() === 302;
}

test.describe(
  "[AC-S413bd5-3-3] Admin reject pending user",
  () => {
    test("reject flow: confirm modal → row removed from 承認待ち section", async ({
      browser,
    }) => {
      if (!(await federationConfigured())) {
        test.skip(
          true,
          `Federation not wired on ${BASE_URL}; set KURA_FED_GOOGLE_* env vars.`,
        );
        return;
      }

      // --- Step 1: provision a pending user via the Google federation flow ---
      const userCtx = await browser.newContext();
      const userPage = await userCtx.newPage();

      await userPage.goto(`${BASE_URL}/login`);
      const loginBtn = userPage.locator('[data-testid="login-google-btn"]');
      const loginBtnVisible = await loginBtn.isVisible().catch(() => false);
      if (!loginBtnVisible) {
        await userCtx.close();
        test.skip(true, "Google login button not present — skipping.");
        return;
      }

      await Promise.all([
        userPage.waitForURL(
          (u) =>
            u.pathname === "/ui/pending-approval" ||
            u.pathname.startsWith("/ui/"),
          { timeout: 20_000 },
        ),
        loginBtn.click(),
      ]);

      const finalPath = new URL(userPage.url()).pathname;
      if (finalPath !== "/ui/pending-approval") {
        await userCtx.close();
        test.skip(
          true,
          `Landed on ${finalPath} instead of /ui/pending-approval. ` +
            "Pending subject already promoted or rejected from a prior run.",
        );
        return;
      }
      await userCtx.close();

      // --- Step 2: admin logs in and navigates to the Users page ---
      const adminCtx = await browser.newContext();
      const adminPage = await adminCtx.newPage();

      await adminPage.goto(`${BASE_URL}/login`);
      await adminPage.fill('input[name="username"]', ADMIN_USER);
      await adminPage.fill('input[name="password"]', ADMIN_PW);
      await adminPage.click('button[type="submit"]');
      await adminPage.waitForURL(
        (u) => u.pathname.startsWith("/ui/admin/"),
        { timeout: 10_000 },
      );

      await adminPage.goto(`${BASE_URL}/ui/admin/users`);
      await adminPage.waitForURL(/\/ui\/admin\/users/, { timeout: 5_000 });

      // [AC-S413bd5-3-3] 承認待ちセクションが表示されること
      const pendingSection = adminPage.locator(
        '[data-testid="pending-users-section"]',
      );
      await expect(pendingSection).toBeVisible({ timeout: 5_000 });

      // Count pending users before reject.
      const pendingRows = adminPage.locator('[data-testid="pending-users-row"]');
      const countBefore = await pendingRows.count();
      expect(countBefore).toBeGreaterThan(0);

      // [AC-S413bd5-3-3] 却下ボタンをクリック
      const rejectBtn = adminPage
        .locator('[data-testid="pending-reject-btn"]')
        .first();
      await expect(rejectBtn).toBeVisible();
      await rejectBtn.click();

      // 却下モーダルが開くこと
      const rejectModal = adminPage
        .locator('[data-testid^="pending-reject-modal-"]')
        .first();
      await expect(rejectModal).toBeVisible({ timeout: 3_000 });

      // [AC-S413bd5-3-3] 却下するボタンを押してリダイレクトされること
      await Promise.all([
        adminPage.waitForURL(/\/ui\/admin\/users/, { timeout: 10_000 }),
        adminPage
          .locator('[data-testid="pending-reject-submit"]')
          .first()
          .click(),
      ]);

      // [AC-S413bd5-3-3] リダイレクト後、却下されたユーザーが消えていること
      // If countBefore == 1, the section should be hidden now.
      if (countBefore === 1) {
        const pendingSectionAfter = adminPage.locator(
          '[data-testid="pending-users-section"]',
        );
        // Section should be absent (not rendered) since count is now 0.
        const sectionVisible = await pendingSectionAfter
          .isVisible()
          .catch(() => false);
        expect(sectionVisible).toBe(false);
      } else {
        // Multiple pending users: just verify one less row.
        const pendingRowsAfter = adminPage.locator(
          '[data-testid="pending-users-row"]',
        );
        const countAfter = await pendingRowsAfter.count();
        expect(countAfter).toBe(countBefore - 1);
      }

      // [AC-S413bd5-3-3] Active users table is still reachable.
      const activeTable = adminPage.locator('[data-testid="users-table"]');
      await expect(activeTable).toBeVisible({ timeout: 5_000 });

      await adminCtx.close();
    });
  },
);
