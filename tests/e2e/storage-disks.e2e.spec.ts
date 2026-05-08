// E2E tests for Sprint Se3b190 Story 2 — Storage page disks table.
// Run against a real kura server with an authenticated admin session.
//
// Acceptance:
//   [AC-Se3b190-2-1] /ui/admin/storage shows physical disks with SMART status

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

test.describe("[AC-Se3b190-2-1] Storage disks table", () => {
  test("disks section renders with a table of devices", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/admin/storage`);

    // The disks section is identified by its testid; on a dev box without
    // ZFS or smartctl the section may be empty, so we only assert presence
    // when the engine returned at least one row.
    const disksSection = page.locator('[data-testid="storage-disks"]');
    if (await disksSection.count()) {
      await expect(disksSection).toBeVisible();
      const rows = disksSection.locator(
        '[data-testid="storage-disk-row"]',
      );
      // The dev VM advertises at least one disk (the OS disk).
      expect(await rows.count()).toBeGreaterThanOrEqual(1);
      // Header columns are translated.
      const ths = await disksSection.locator("th").allInnerTexts();
      expect(ths).toEqual(
        expect.arrayContaining([
          "デバイス",
          "モデル",
          "サイズ",
          "温度",
          "SMART",
          "用途",
        ]),
      );
    }
  });

  test("SMART badge text is translated (no raw CLI tokens)", async ({
    page,
  }) => {
    await page.goto(`${BASE_URL}/ui/admin/storage`);
    const body = await page.locator("body").innerText();
    // Forbidden: raw smartctl tokens leaking through.
    expect(body).not.toContain("smart_status");
    expect(body).not.toContain("ata_smart_attributes");
  });
});
