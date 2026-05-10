// E2E tests for Sprint S464e47 Story 1 — UI shell + sidebar.
// Run against a real kura server (started by `make serve`).
// Acceptance: AC-S464e47-1-1 (clickable 7-item sidebar at /ui/admin)
//             AC-S464e47-1-2 (all rendered text via i18n)

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";
const ADMIN_USER = process.env.KURA_E2E_ADMIN_USER ?? "admin";
const ADMIN_PW = process.env.KURA_E2E_ADMIN_PW ?? "password";

// Every /ui/admin/* page redirects to /login when unauthenticated.
// Logging in once per test keeps each spec independent.
test.beforeEach(async ({ page }) => {
  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="username"]', ADMIN_USER);
  await page.fill('input[name="password"]', ADMIN_PW);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname.startsWith("/ui/admin/"));
});

const NAV_ITEMS = [
  { id: "dashboard", label: "ダッシュボード", path: "/ui/admin/dashboard" },
  { id: "storage", label: "ストレージ", path: "/ui/admin/storage" },
  { id: "shares", label: "共有", path: "/ui/admin/shares" },
  { id: "users", label: "ユーザー", path: "/ui/admin/users" },
  { id: "network", label: "ネットワーク", path: "/ui/admin/network" },
  { id: "apps", label: "アプリ", path: "/ui/admin/apps" },
  { id: "settings", label: "設定", path: "/ui/admin/settings" },
];

test.describe("[AC-S464e47-1-1] Admin shell sidebar", () => {
  test("renders 7 nav items at /ui/admin/dashboard", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/admin/dashboard`);
    const nav = page.locator('[data-testid="sidebar-nav"]');
    await expect(nav).toBeVisible();
    const items = nav.locator(".nav-item");
    await expect(items).toHaveCount(NAV_ITEMS.length);
  });

  for (const item of NAV_ITEMS) {
    test(`sidebar item "${item.id}" is clickable and routes to ${item.path}`, async ({
      page,
    }) => {
      await page.goto(`${BASE_URL}/ui/admin/dashboard`);
      const link = page.locator(
        `[data-testid="sidebar-nav"] [data-nav-id="${item.id}"]`,
      );
      await expect(link).toBeVisible();
      await expect(link).toHaveAttribute("href", item.path);
      await link.click();
      await page.waitForURL(`${BASE_URL}${item.path}`);
      // Sidebar still visible after navigation.
      await expect(page.locator('[data-testid="sidebar-nav"]')).toBeVisible();
      // Active item is the destination.
      const active = page.locator(
        `[data-testid="sidebar-nav"] [data-nav-id="${item.id}"]`,
      );
      await expect(active).toHaveAttribute("data-active", "true");
    });
  }
});

test.describe("[AC-S464e47-1-2] i18n coverage", () => {
  test("dashboard renders translated Japanese strings", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/admin/dashboard`);
    for (const item of NAV_ITEMS) {
      await expect(
        page.locator(`[data-nav-id="${item.id}"] .nav-label`),
      ).toHaveText(item.label);
    }
    // Section heading and page title are also translated.
    await expect(page.locator(".nav-section-label").first()).toHaveText(
      "システム管理",
    );
    await expect(page.locator(".page-title").first()).toHaveText(
      "ダッシュボード",
    );
  });

  test("no untranslated MessageID appears in body", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/admin/dashboard`);
    const body = await page.locator("body").innerText();
    // The Translator returns the MessageID itself when a key is missing,
    // so finding "nav.dashboard" in rendered text indicates a missing
    // translation and breaks AC-S464e47-1-2.
    const probe = ["nav.dashboard", "page.dashboard.title", "brand.name"];
    for (const id of probe) {
      expect(body).not.toContain(id);
    }
  });
});

test.describe("[AC-S464e47-1-1] Theme toggle (Fog palette light/dark)", () => {
  test("toggle button switches data-theme on <html>", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/admin/dashboard`);
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await page.locator("#btn-toggle-theme").click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await page.locator("#btn-toggle-theme").click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  });
});
