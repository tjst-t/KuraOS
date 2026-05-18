// E2E tests for Sprint S0eedaa Story 2 — Built-in filebrowser (/ui/files).
//
// Acceptance criteria:
//   [AC-S0eedaa-2-1] list / download / upload-with-progress / delete / rename
//                    all work from the browser UI
//   [AC-S0eedaa-2-2] Only shares where the current user has ACL access are shown
//
// Run against a live kura server at KURA_BASE_URL (default: http://192.168.1.42:8204).
// Admin: admin / password (VM test fixture).
//
// Test isolation: all tests in [AC-S0eedaa-2-1] share ONE pre-created share
// "e2e-ui-suite" pointing to /tmp (avoids path conflict on repeated create).
// Within each test, unique file/dir names (timestamp-based) are used.

import { test, expect, Page, APIRequestContext } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://192.168.1.42:8204";
const ADMIN_USER = process.env.KURA_TEST_ADMIN_USERNAME ?? "admin";
const ADMIN_PASS = process.env.KURA_TEST_ADMIN_PASSWORD ?? "password";

// Shared share name for the entire suite. Must match file-api-range.e2e.spec.ts.
// Both test files use the same share pointing to /tmp — one creation succeeds
// and the second is a no-op (share already exists).
const SUITE_SHARE = "e2e-suite";

// sessionCookie for API calls inside tests.
let adminSession = "";

async function loginAsAdmin(page: Page): Promise<void> {
  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="username"]', ADMIN_USER);
  await page.fill('input[name="password"]', ADMIN_PASS);
  await page.click('button[type="submit"]');
  await page.waitForURL(`${BASE_URL}/ui/admin/dashboard`, { timeout: 15000 });
}

async function loginAndGetCookie(request: APIRequestContext): Promise<string> {
  const resp = await request.post(`${BASE_URL}/login`, {
    form: { username: ADMIN_USER, password: ADMIN_PASS },
    maxRedirects: 0,
  });
  const setCookie = resp.headers()["set-cookie"] ?? "";
  const match = setCookie.match(/kura_session=([^;]+)/);
  if (!match) throw new Error(`login failed: ${setCookie}`);
  return match[1];
}

async function ensureSuiteShare(
  request: APIRequestContext,
  cookie: string
): Promise<void> {
  // Create share pointing to /var/tmp (world-writable, always exists, separate from
  // /tmp so it avoids path-conflict with any pre-existing /tmp-backed test shares).
  // If already exists (200 with form error) that's fine — share is still accessible.
  await request.post(`${BASE_URL}/ui/admin/shares`, {
    form: {
      name: SUITE_SHARE,
      path: "/var/tmp",
      protocol: "smb",
      preset: "general",
      access_mode: "read_write",
    },
    headers: { Cookie: `kura_session=${cookie}` },
  });
}

// ── Suite setup ───────────────────────────────────────────────────────────────
test.beforeAll(async ({ request }) => {
  adminSession = await loginAndGetCookie(request);
  await ensureSuiteShare(request, adminSession);
});

// ── [AC-S0eedaa-2-1] Filebrowser UI: list / download / upload / delete / rename ──
test.describe("[AC-S0eedaa-2-1] Files UI: list / download / upload / delete / rename", () => {
  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page);
  });

  test("navigates to /ui/files and shows the files page title", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/files`);
    await expect(page.locator('[data-testid="files-title"]')).toBeVisible({ timeout: 10000 });
  });

  test("shows share tab for suite share when browsing /ui/files", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/files`);
    await expect(page.locator('[data-testid="files-title"]')).toBeVisible({ timeout: 10000 });
    // The suite share tab must appear for the admin.
    await expect(page.locator(`[data-testid="share-tab-${SUITE_SHARE}"]`)).toBeVisible({ timeout: 5000 });
  });

  test("selecting a share shows the file table or empty state", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);
    await expect(page.locator('[data-testid="files-title"]')).toBeVisible({ timeout: 10000 });

    // Either files-table (non-empty dir) or files-empty (empty dir) must be visible.
    const tableOrEmpty = page.locator('[data-testid="files-table"], [data-testid="files-empty"]');
    await expect(tableOrEmpty.first()).toBeVisible({ timeout: 5000 });
  });

  test("toolbar shows upload and new-folder buttons when share is selected", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);
    await expect(page.locator('[data-testid="btn-upload"]')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('[data-testid="btn-new-folder"]')).toBeVisible();
  });

  test("upload a file and verify it appears in the file list", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);
    await expect(page.locator('[data-testid="btn-upload"]')).toBeVisible({ timeout: 10000 });

    const fileChooserPromise = page.waitForEvent("filechooser");
    await page.locator('[data-testid="btn-upload"]').click();
    const fileChooser = await fileChooserPromise;

    const testFileName = `e2e-upl-${Date.now()}.txt`;
    await fileChooser.setFiles({
      name: testFileName,
      mimeType: "text/plain",
      buffer: Buffer.from("e2e upload test content"),
    });

    // Wait for XHR upload to complete and page to reload (~800ms delay in template JS).
    await page.waitForTimeout(2500);
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);

    const fileRow = page.locator('[data-testid="file-row"]').filter({ hasText: testFileName });
    await expect(fileRow).toBeVisible({ timeout: 10000 });
  });

  test("delete a file via the delete button", async ({ page }) => {
    // First upload a file to delete.
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);
    await expect(page.locator('[data-testid="btn-upload"]')).toBeVisible({ timeout: 10000 });

    const fileChooserPromise = page.waitForEvent("filechooser");
    await page.locator('[data-testid="btn-upload"]').click();
    const fileChooser = await fileChooserPromise;
    const testFileName = `e2e-del-${Date.now()}.txt`;
    await fileChooser.setFiles({
      name: testFileName,
      mimeType: "text/plain",
      buffer: Buffer.from("to be deleted"),
    });

    await page.waitForTimeout(2500);
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);

    const fileRow = page.locator('[data-testid="file-row"]').filter({ hasText: testFileName });
    await expect(fileRow).toBeVisible({ timeout: 10000 });

    // Click delete and accept the confirm dialog.
    page.on("dialog", (dialog) => dialog.accept());
    await fileRow.locator(`[data-testid="btn-delete-${testFileName}"]`).click();

    // Wait for reload and verify file is gone.
    await page.waitForTimeout(1000);
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);
    await expect(
      page.locator('[data-testid="file-row"]').filter({ hasText: testFileName })
    ).not.toBeVisible({ timeout: 5000 });
  });

  test("rename a file via the rename modal", async ({ page }) => {
    // Upload a file first.
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);
    await expect(page.locator('[data-testid="btn-upload"]')).toBeVisible({ timeout: 10000 });

    const fileChooserPromise = page.waitForEvent("filechooser");
    await page.locator('[data-testid="btn-upload"]').click();
    const fileChooser = await fileChooserPromise;
    const originalName = `e2e-ren-${Date.now()}.txt`;
    await fileChooser.setFiles({
      name: originalName,
      mimeType: "text/plain",
      buffer: Buffer.from("rename me"),
    });

    await page.waitForTimeout(2500);
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);

    const fileRow = page.locator('[data-testid="file-row"]').filter({ hasText: originalName });
    await expect(fileRow).toBeVisible({ timeout: 10000 });

    // Open rename modal.
    await fileRow.locator(`[data-testid="btn-rename-${originalName}"]`).click();
    await expect(page.locator('[data-testid="rename-modal"]')).toBeVisible({ timeout: 5000 });

    const newName = `e2e-renamed-${Date.now()}.txt`;
    await page.locator('[data-testid="rename-input"]').fill(newName);
    await page.locator('[data-testid="rename-submit"]').click();

    await page.waitForTimeout(1000);
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);

    // New name visible, old name gone.
    await expect(
      page.locator('[data-testid="file-row"]').filter({ hasText: newName })
    ).toBeVisible({ timeout: 10000 });
    await expect(
      page.locator('[data-testid="file-row"]').filter({ hasText: originalName })
    ).not.toBeVisible();
  });

  test("create new folder via mkdir modal", async ({ page }) => {
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);
    await expect(page.locator('[data-testid="btn-new-folder"]')).toBeVisible({ timeout: 10000 });

    await page.locator('[data-testid="btn-new-folder"]').click();
    await expect(page.locator('[data-testid="mkdir-modal"]')).toBeVisible({ timeout: 5000 });

    const dirName = `e2e-dir-${Date.now()}`;
    await page.locator('[data-testid="mkdir-input"]').fill(dirName);
    await page.locator('[data-testid="mkdir-submit"]').click();

    await page.waitForTimeout(1000);
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);

    const dirRow = page.locator('[data-testid="file-row"]').filter({ hasText: dirName });
    await expect(dirRow).toBeVisible({ timeout: 10000 });
  });

  test("download link triggers a file download", async ({ page }) => {
    // Upload a file to download.
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);
    await expect(page.locator('[data-testid="btn-upload"]')).toBeVisible({ timeout: 10000 });

    const fileChooserPromise = page.waitForEvent("filechooser");
    await page.locator('[data-testid="btn-upload"]').click();
    const fileChooser = await fileChooserPromise;
    const testFileName = `e2e-dl-${Date.now()}.txt`;
    await fileChooser.setFiles({
      name: testFileName,
      mimeType: "text/plain",
      buffer: Buffer.from("download test content"),
    });

    await page.waitForTimeout(2500);
    await page.goto(`${BASE_URL}/ui/files?share=${SUITE_SHARE}&path=/`);

    const fileRow = page.locator('[data-testid="file-row"]').filter({ hasText: testFileName });
    await expect(fileRow).toBeVisible({ timeout: 10000 });

    // Download button must be present.
    await expect(fileRow.locator(`[data-testid="btn-download-${testFileName}"]`)).toBeVisible();

    // Trigger download and verify suggested filename.
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      fileRow.locator(`[data-testid="btn-download-${testFileName}"]`).click(),
    ]);
    expect(download.suggestedFilename()).toBe(testFileName);
  });
});

// ── [AC-S0eedaa-2-2] ACL-filtered share visibility ───────────────────────────
test.describe("[AC-S0eedaa-2-2] Share ACL filtering — user only sees permitted shares", () => {
  test("admin user sees the suite share tab in /ui/files", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(`${BASE_URL}/ui/files`);
    await expect(page.locator('[data-testid="files-title"]')).toBeVisible({ timeout: 10000 });
    await expect(page.locator(`[data-testid="share-tab-${SUITE_SHARE}"]`)).toBeVisible({ timeout: 5000 });
  });

  test("regular user without ACL does not see admin-only share", async ({ page, request }) => {
    // Create an admin-only share (ACL: only admin user, access_mode=none for others).
    const adminOnlyShare = `e2e-adminonly-${Date.now()}`;
    await request.post(`${BASE_URL}/ui/admin/shares`, {
      form: {
        name: adminOnlyShare,
        path: "/tmp",
        protocol: "smb",
        preset: "general",
        access_mode: "none",
        acl: `user:${ADMIN_USER}:rw`,
      },
      headers: { Cookie: `kura_session=${adminSession}` },
    });

    // Create a test user with role=user.
    const testUser = `e2e-user-${Date.now()}`;
    const testPw = "TestPass123!";
    await request.post(`${BASE_URL}/ui/admin/users`, {
      form: {
        username: testUser,
        display_name: "E2E File User",
        password: testPw,
        role: "user",
      },
      headers: { Cookie: `kura_session=${adminSession}` },
    });

    // Log in as the regular user.
    await page.goto(`${BASE_URL}/login`);
    await page.fill('input[name="username"]', testUser);
    await page.fill('input[name="password"]', testPw);
    await page.click('button[type="submit"]');
    // User-role users land on /ui/admin/dashboard or /ui depending on the gateway.
    await page.waitForTimeout(2000);

    await page.goto(`${BASE_URL}/ui/files`);
    await expect(page.locator('[data-testid="files-title"]')).toBeVisible({ timeout: 10000 });

    // The admin-only share must NOT appear in the regular user's tab list.
    await expect(
      page.locator(`[data-testid="share-tab-${adminOnlyShare}"]`)
    ).not.toBeVisible({ timeout: 3000 });

    // Cleanup: delete test user (best-effort via admin API).
    await request.post(`${BASE_URL}/ui/admin/users/${testUser}/delete`, {
      headers: { Cookie: `kura_session=${adminSession}` },
    }).catch(() => {});
  });
});
