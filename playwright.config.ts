// Playwright config for KuraOS GUI E2E tests.
//
// Tests live in tests/e2e/*.e2e.spec.ts. They run against a real
// kura server — by default the VM at 192.168.1.42:8204 — because
// the recurring Class A bug ("server-side OK / browser broken") is
// only catchable when a real browser executes the JS / htmx swaps.
//
// Override target with KURA_BASE_URL when running locally (e.g.
// against `make serve` on the dev box).
//
// Quickstart:
//   npm install
//   npx playwright install --with-deps chromium
//   npm run e2e             # or: make e2e
import { defineConfig, devices } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://192.168.1.42:8204";

export default defineConfig({
  testDir: "./tests/e2e",
  testMatch: "*.e2e.spec.ts",
  // Tests share an admin session; serial keeps logout / user CRUD
  // tests from racing each other on the single VM target.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: BASE_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    // Self-signed cert + http on the dev VM — accept anything.
    ignoreHTTPSErrors: true,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
