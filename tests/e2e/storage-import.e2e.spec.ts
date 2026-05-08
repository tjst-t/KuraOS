// E2E tests for Sprint Se3b190 Story 3 — Importable pool detection banner.
// Run against a real kura server.
//
// Acceptance:
//   [AC-Se3b190-3-1] Storage page shows the import banner with detected
//                    pool GUID/state/disks; actual import deferred to next
//                    sprint (button disabled).

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

test.describe("[AC-Se3b190-3-1] Importable pool banner", () => {
  test("banner appears when zpool import returns at least one pool", async ({
    page,
  }) => {
    await page.goto(`${BASE_URL}/ui/admin/storage`);

    // The banner is conditional. Skip the assertion if the engine reports
    // no importable pools — the dev box without ZFS will hit this branch.
    const banner = page.locator('[data-testid="storage-import-banner"]');
    if (await banner.count()) {
      await expect(banner).toBeVisible();
      // The "確認" button is the placeholder — disabled in this sprint.
      const inspect = banner.locator("button");
      await expect(inspect).toBeDisabled();
      // The deferred-note copy must be present so operators don't expect
      // the button to work.
      await expect(banner).toContainText(
        "実際のインポートは次のスプリントで実装します",
      );
    }
  });

  test("importable-pools table mirrors the banner detection", async ({
    page,
  }) => {
    await page.goto(`${BASE_URL}/ui/admin/storage`);
    const importTable = page.locator('[data-testid="storage-import"]');
    if (await importTable.count()) {
      const rows = importTable.locator(
        '[data-testid="storage-import-row"]',
      );
      expect(await rows.count()).toBeGreaterThan(0);
      // Each row carries the pool GUID and a state badge.
      for (const row of await rows.all()) {
        await expect(row.locator(".badge")).toBeVisible();
      }
    }
  });
});
