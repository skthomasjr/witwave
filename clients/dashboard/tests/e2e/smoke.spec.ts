import { test, expect, type Route } from "@playwright/test";

// End-to-end smoke coverage against a mocked `/api/*` surface (#818).
// Every test installs a `page.route()` handler before the initial
// navigation so the dashboard never has to reach a live harness.

const TEAM_FIXTURE = [
  { name: "iris", url: "http://iris:8000" },
  { name: "nova", url: "http://nova:8001" },
];

const AGENTS_FIXTURE = [
  {
    id: "iris-witwave",
    role: "witwave",
    url: "http://iris:8000",
    card: { name: "iris", description: "Primary agent" },
  },
  {
    id: "iris-claude",
    role: "backend",
    url: "http://iris-claude:8000",
    card: { name: "iris-claude" },
  },
];

function json(route: Route, body: unknown): Promise<void> {
  return route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

async function installApiMocks(page: import("@playwright/test").Page) {
  await page.route("**/api/team", (route) => json(route, TEAM_FIXTURE));
  await page.route("**/api/agents/*/agents", (route) =>
    json(route, AGENTS_FIXTURE),
  );
  await page.route("**/api/agents/*/conversations*", (route) =>
    json(route, []),
  );
  // Generic catch-all for any other /api/* endpoint a view might try.
  await page.route("**/api/**", (route) => json(route, []));
}

test.describe("dashboard smoke", () => {
  test("shell renders with nav links", async ({ page }) => {
    await installApiMocks(page);
    await page.goto("/");
    await expect(page.getByRole("link", { name: "Team" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Automation" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Metrics" })).toBeVisible();
  });

  test("team view lists agents from the mocked /api/team", async ({ page }) => {
    await installApiMocks(page);
    await page.goto("/");
    // Default route is Team.
    await expect(page.locator("[data-testid='agent-card']").first()).toBeVisible();
    await expect(page.getByText("iris").first()).toBeVisible();
    await expect(page.getByText("nova").first()).toBeVisible();
  });

  test("legacy /jobs path redirects to /automation", async ({ page }) => {
    await installApiMocks(page);
    await page.goto("/jobs");
    await expect(page).toHaveURL(/\/automation$/);
  });
});
