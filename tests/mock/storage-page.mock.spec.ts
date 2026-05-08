// Mock tests for Sprint Se3b190 — Storage page error / edge cases.
//
// These run against a static HTML fixture (no kura server required) and
// cover the visual states that the e2e tests can't reliably hit on a dev
// box without ZFS: empty state, banner copy, disabled buttons.

import { test, expect } from "@playwright/test";
import * as path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const FIXTURE = `file://${path.resolve(
  __dirname,
  "fixtures/storage-page.html",
)}`;

test.describe("storage-page mock", () => {
  test("import banner disabled state copy is present", async ({ page }) => {
    await page.goto(FIXTURE);
    const banner = page.locator('[data-testid="storage-import-banner"]');
    await expect(banner).toBeVisible();
    await expect(banner).toContainText(
      "実際のインポートは次のスプリントで実装します",
    );
    const button = banner.locator("button");
    await expect(button).toBeDisabled();
  });

  test("new-pool button is disabled (deferred to next sprint)", async ({
    page,
  }) => {
    await page.goto(FIXTURE);
    const newPool = page
      .locator(".page-head button")
      .filter({ hasText: "新しいプールを作成" });
    await expect(newPool).toBeDisabled();
  });

  test("no raw CLI tokens leak into rendered text", async ({ page }) => {
    await page.goto(FIXTURE);
    const body = await page.locator("body").innerText();
    for (const banned of [
      "smart_status",
      "ata_smart_attributes",
      "zpool list -H",
      "no pools available for import",
    ]) {
      expect(body).not.toContain(banned);
    }
  });

  test("disks table header is translated", async ({ page }) => {
    await page.goto(FIXTURE);
    const ths = await page
      .locator('[data-testid="storage-disks"] th')
      .allInnerTexts();
    expect(ths).toEqual(
      expect.arrayContaining([
        "デバイス",
        "モデル",
        "プール",
        "サイズ",
        "温度",
        "SMART",
        "用途",
      ]),
    );
  });
});
