import { defineConfig, devices } from "@playwright/test";

// Playwright scaffold for the dashboard (#818). The dashboard talks
// to `/api/*` at runtime, so the smoke suite stubs those endpoints
// via `page.route()` inside each test rather than requiring a live
// cluster. This keeps E2E coverage runnable in CI containers without
// having to port-forward every agent's harness.
//
// Invocation:
//   cd dashboard
//   npx playwright install chromium   # first run only — downloads browser
//   npm run test:e2e
//
// CI gating is deliberately follow-up — the scaffold proves the
// pattern; sweep-in of broader coverage (conversations drawer, chat
// send/cancel, degraded banner, legacy-route redirects) lands on
// top of this configuration.

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // Low retry count — flaky specs should fail loudly, not hide.
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:4173",
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  // Auto-start the production build's preview server for smoke tests.
  // `npm run build && npm run preview` gives us a deterministic bundle
  // without depending on vite's HMR pipeline. Swap to `npm run dev`
  // locally with PLAYWRIGHT_BASE_URL if faster iteration is wanted.
  webServer: {
    command: "npm run build && npm run preview -- --port 4173 --strictPort",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
