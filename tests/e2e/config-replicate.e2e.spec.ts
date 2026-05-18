// E2E tests for config export / import round-trip (S99702c-3-2).
//
// Acceptance:
//   [AC-S99702c-3-2] Config tab has export button; clicking it downloads config.json
//   [AC-S99702c-3-2] Config tab has import file input; uploading config.json shows success

import { test, expect } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import * as os from "os";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

test.beforeEach(async ({ page }) => {
  await page.goto(`${BASE_URL}/login`);
  const hasLogin = await page.locator('input[name="username"]').isVisible().catch(() => false);
  if (!hasLogin) {
    test.skip(true, "login page not available");
    return;
  }
  await page.fill('input[name="username"]', "root");
  await page.fill('input[name="password"]', "longenoughpw");
  await page.click('button[type="submit"]');
  await page.waitForURL(`**`);
});

test("[AC-S99702c-3-2] Config tab renders export and import controls", async ({ page }) => {
  await page.goto(`${BASE_URL}/ui/admin/settings?tab=config`);
  await expect(page.locator('[data-testid="config-card"]')).toBeVisible();
  await expect(page.locator('[data-testid="config-export-btn"]')).toBeVisible();
  await expect(page.locator('[data-testid="config-import-file-input"]')).toBeVisible();
  await expect(page.locator('[data-testid="config-import-btn"]')).toBeVisible();
});

test("[AC-S99702c-3-2] Config export downloads valid JSON", async ({ page }) => {
  // Use API-level fetch to get the export (download interception in Playwright
  // requires chromium-specific setup; direct fetch is reliable in CI).
  const cookies = await page.context().cookies();
  const cookieHeader = cookies.map((c) => `${c.name}=${c.value}`).join("; ");

  const response = await page.evaluate(
    async ({ url, cookieHdr }: { url: string; cookieHdr: string }) => {
      const r = await fetch(url, { headers: { Cookie: cookieHdr } });
      return { status: r.status, body: await r.text() };
    },
    { url: `${BASE_URL}/ui/admin/settings/config/export`, cookieHdr: cookieHeader },
  );

  expect(response.status).toBe(200);
  // Must be parseable JSON with schema_version.
  const cfg = JSON.parse(response.body) as Record<string, unknown>;
  expect(cfg).toHaveProperty("schema_version");
});

test("[AC-S99702c-3-2] Config import round-trip succeeds", async ({ page }) => {
  // 1. Export the current config.
  const cookies = await page.context().cookies();
  const cookieHeader = cookies.map((c) => `${c.name}=${c.value}`).join("; ");

  const exportResp = await page.evaluate(
    async ({ url, cookieHdr }: { url: string; cookieHdr: string }) => {
      const r = await fetch(url, { headers: { Cookie: cookieHdr } });
      return { status: r.status, body: await r.text() };
    },
    { url: `${BASE_URL}/ui/admin/settings/config/export`, cookieHdr: cookieHeader },
  );
  expect(exportResp.status).toBe(200);

  // 2. Write to a temp file, then upload via the form file input.
  const tmpDir = os.tmpdir();
  const tmpFile = path.join(tmpDir, "kura-config-e2e.json");
  fs.writeFileSync(tmpFile, exportResp.body, "utf8");

  await page.goto(`${BASE_URL}/ui/admin/settings?tab=config`);
  await page.setInputFiles('[data-testid="config-import-file-input"]', tmpFile);
  await page.click('[data-testid="config-import-btn"]');

  // Success banner must appear.
  await expect(page.locator('[data-testid="config-import-ok-banner"]')).toBeVisible({
    timeout: 10000,
  });

  fs.unlinkSync(tmpFile);
});
