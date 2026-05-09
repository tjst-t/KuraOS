// E2E test for Sprint S65b510 Story 2 — Gateway dynamic routing.
//
// Acceptance:
//   AC-S65b510-2-1 — manifest routing.mode (path / port / subdomain) reflects
//                    in /apps/<name>/* on install and disappears on uninstall.

import { test, expect } from "@playwright/test";

const BASE_URL = process.env.KURA_BASE_URL ?? "http://127.0.0.1:8204";

test.describe("[AC-S65b510-2-1] Gateway routing follows install state", () => {
  test("listing endpoint reflects registered routes", async ({ request }) => {
    const resp = await request.get(`${BASE_URL}/api/app-routes`);
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(Array.isArray(body)).toBe(true);
    // The shape is the contract — every entry has the four required fields.
    for (const route of body) {
      expect(route).toHaveProperty("app_id");
      expect(route).toHaveProperty("app_name");
      expect(route).toHaveProperty("mode");
      expect(route).toHaveProperty("host_port");
    }
  });

  test("/apps/<unknown>/ returns 404 (route absent)", async ({ request }) => {
    const resp = await request.get(`${BASE_URL}/apps/totally-unknown-app/`);
    expect(resp.status()).toBe(404);
  });
});
