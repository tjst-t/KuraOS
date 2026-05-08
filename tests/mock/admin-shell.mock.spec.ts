// Mock tests for Sprint S464e47 Story 1.
// These run against a static HTML fixture (no kura server required) and cover
// error / edge-case visuals — invalid theme value, collapsed sidebar
// initial state, and absent localStorage.
//
// They are gated by `mock` so the gating-vs-e2e split documented in the sprint
// runner is honored.

import { test, expect } from "@playwright/test";
import * as path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const FIXTURE = `file://${path.resolve(__dirname, "fixtures/admin-shell.html")}`;

test.describe("admin-shell mock", () => {
  test("renders all 7 nav items from the static fixture", async ({ page }) => {
    await page.goto(FIXTURE);
    const items = page.locator('[data-testid="sidebar-nav"] .nav-item');
    await expect(items).toHaveCount(7);
  });

  test("invalid stored theme falls back to light", async ({
    page,
    context,
  }) => {
    await context.addInitScript(() => {
      window.localStorage.setItem("kura-theme", "rainbow");
    });
    await page.goto(FIXTURE);
    // The boot script in admin.tmpl reads localStorage but the value
    // "rainbow" is not "dark", so data-theme stays at the document's
    // initial "light".
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  });

  test("collapsed sidebar persists across reloads", async ({
    page,
    context,
  }) => {
    await context.addInitScript(() => {
      window.localStorage.setItem("kura-collapsed", "true");
    });
    await page.goto(FIXTURE);
    await expect(page.locator(".app")).toHaveAttribute(
      "data-collapsed",
      "true",
    );
  });

  test("missing localStorage does not throw", async ({ page, context }) => {
    await context.addInitScript(() => {
      // Simulate Safari Private Mode: localStorage throws on access.
      Object.defineProperty(window, "localStorage", {
        get() {
          throw new Error("denied");
        },
      });
    });
    await page.goto(FIXTURE);
    // Page still mounts.
    await expect(page.locator('[data-testid="sidebar-nav"]')).toBeVisible();
  });
});
