import { expect, test, type Page, type Route } from "./site";

// The Overlord, 2026-10-13, with this task's own Connections and Claude's Top
// connectors side by side: "fix conenctors so they work fluidly and llok like
// caldu code conenctors... pelase use the actual logo", "more like this where
// plus icon either has a plus or check". Each connector is a card with its
// logo, its name, one line on what it does and one square button. The
// supervisor is played here: its connection check and its grant are routes.
const withheld = (name: string) => ({ id: "service:" + name, name, kind: "service", status: "withheld", source: "Repository", detail: "This task does not carry it. The CFO gives it with cfo auth grant connection-proof " + name + ".", checked_at: "2026-10-13T12:07:00Z", grant: "grant" });
const ENTRIES = [
  withheld("github"), withheld("context7"), withheld("openrouter"), withheld("google-report"),
  { id: "service:acme-internal", name: "acme-internal", kind: "service", status: "unauthorized", detail: "Sign-in required.", actions: ["login"], checked_at: "2026-10-13T12:07:00Z", grant: "revoke" },
  { id: "mcp:personal", name: "personal", kind: "mcp", status: "connected", checked_at: "2026-10-13T12:07:00Z" },
  { id: "credential:GITHUB_TOKEN", name: "GITHUB_TOKEN", kind: "credential", status: "withheld", detail: "This task does not carry it.", checked_at: "2026-10-13T12:07:00Z" },
  { id: "credential:OPENROUTER_API_KEY", name: "OPENROUTER_API_KEY", kind: "credential", status: "provided", checked_at: "2026-10-13T12:07:00Z" },
];

interface Supervisor { grants: unknown[]; tokens: string[]; entries: object[]; checking: boolean; held?: Promise<void>; refuses?: string }

async function open(page: Page, supervisor: Supervisor) {
  await page.route("**/api/workspace**", (route) => route.fulfill({ json: { repository: "code-goblins", root: "C:/scratch/work", harness: "codex", model: "Reported: gpt-6-astra", notes: [] } }));
  await page.route("**/api/engines", (route) => route.fulfill({ json: { harnesses: [] } }));
  await page.route("**/api/connections**", async (route: Route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/connections/grant") {
      supervisor.grants.push(route.request().postDataJSON());
      supervisor.tokens.push(route.request().headers()["x-cfo-token"]);
      await supervisor.held;
      if (supervisor.refuses) return route.fulfill({ status: 409, json: { error: supervisor.refuses } });
      return route.fulfill({ json: { instance: "connections-proof", checking: true, entries: supervisor.entries } });
    }
    return route.fulfill({ json: { instance: "connections-proof", checking: supervisor.checking, entries: supervisor.entries } });
  });
  await page.goto("/tests/fixtures/connections.html");
  await page.getByText("Connections", { exact: true }).click();
  const panel = page.getByRole("region", { name: "Connections" });
  await expect(panel.locator(".connector-card").first()).toBeVisible();
  return panel;
}

const card = (panel: ReturnType<Page["getByRole"]>, name: string) => panel.locator(".connector-card").filter({ has: panel.page().locator("strong", { hasText: new RegExp("^" + name + "$") }) });

for (const viewport of [{ width: 1280, height: 1400 }, { width: 390, height: 844 }]) {
  test(`each connector is a card with its own logo, its name, one line and + or a check, and nothing else, at ${viewport.width}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize(viewport);
    const supervisor: Supervisor = { grants: [], tokens: [], entries: ENTRIES, checking: false };

    // Act
    const panel = await open(page, supervisor);

    // Assert
    for (const [name, about] of [["github", "Repositories, pull requests and Actions"], ["context7", "Current library documentation"], ["openrouter", "Models from many providers"], ["google-report", "Google APIs"]]) {
      const each = card(panel, name);
      await expect(each.locator(".connector-logo svg")).toHaveCount(1);
      await expect(each.locator(".connector-about")).toHaveText(about);
      await expect(each.getByRole("button", { name: "Give " + name })).toBeVisible();
    }
    await expect(card(panel, "acme-internal").locator(".connector-initial")).toHaveText("A");
    await expect(card(panel, "acme-internal").getByRole("button", { name: "Take acme-internal back" })).toBeVisible();
    await expect(card(panel, "personal").getByRole("button", { name: /^(Give|Take)/ })).toHaveCount(0);
    await expect(panel).not.toContainText("Withheld");
    await expect(panel).not.toContainText("does not carry it");
    await expect(panel).not.toContainText(/Checked \d/);
    await expect(panel.getByText("GITHUB_TOKEN", { exact: true })).toHaveCount(0);
    await expect(card(panel, "OPENROUTER_API_KEY").locator(".connector-logo svg")).toHaveCount(1);
    await expect(card(panel, "OPENROUTER_API_KEY").locator(".connector-about")).toHaveCount(0);
    expect(await panel.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.locator("main").screenshot({ path: test.info().outputPath("connector-cards-" + viewport.width + ".png") });
  });
}

test("+ gives the task the connector through the board's API and the card shows its check at once, and the check takes it back", async ({ page }) => {
  // Arrange
  let release = () => {};
  const supervisor: Supervisor = { grants: [], tokens: [], entries: ENTRIES, checking: false, held: new Promise<void>((resolve) => { release = resolve; }) };
  const panel = await open(page, supervisor);
  const github = card(panel, "github");

  // Act
  await github.getByRole("button", { name: "Give github" }).click();

  // Assert: the check shows before the supervisor has even answered.
  await expect(github.getByRole("button", { name: "Take github back" })).toBeVisible();
  await expect.poll(() => supervisor.grants).toEqual([{ task: "connection-proof", generation: "first", connection: "service:github", action: "grant" }]);
  expect(supervisor.tokens).toEqual(["connections-proof"]);

  // Act: the check that follows finds it carried.
  supervisor.entries = ENTRIES.map((entry) => entry.id === "service:github" ? { ...entry, status: "connected", detail: "", grant: "revoke" } : entry);
  release();

  // Assert
  await expect(github.getByRole("button", { name: "Take github back" })).toBeEnabled();
  await expect(github.locator(".connector-trouble")).toHaveCount(0);

  // Act
  await github.getByRole("button", { name: "Take github back" }).click();

  // Assert
  await expect(github.getByRole("button", { name: "Give github" })).toBeVisible();
  await expect.poll(() => supervisor.grants.length).toBe(2);
  expect(supervisor.grants[1]).toEqual({ task: "connection-proof", generation: "first", connection: "service:github", action: "revoke" });
});

test("a press the supervisor refuses puts the card back and says why beside it", async ({ page }) => {
  // Arrange
  const supervisor: Supervisor = { grants: [], tokens: [], entries: ENTRIES, checking: false, refuses: "Change it in the Code Goblins window, or ask the CFO." };
  const panel = await open(page, supervisor);
  const context7 = card(panel, "context7");

  // Act
  await context7.getByRole("button", { name: "Give context7" }).click();

  // Assert
  await expect(panel.locator(".click-feedback")).toHaveText("Change it in the Code Goblins window, or ask the CFO.");
  await expect(context7.getByRole("button", { name: "Give context7" })).toBeVisible();
});

test("a carried connector that fails keeps one faint mark with its reason in the tooltip, and its repair", async ({ page }) => {
  // Arrange
  const supervisor: Supervisor = { grants: [], tokens: [], entries: ENTRIES, checking: false };

  // Act
  const panel = await open(page, supervisor);

  // Assert
  const acme = card(panel, "acme-internal");
  const mark = acme.locator(".connector-trouble");
  await expect(mark).toHaveAttribute("aria-label", "Sign in: Sign-in required.");
  await expect(mark).toHaveAttribute("data-tip", "Sign in: Sign-in required.");
  expect(Number(await mark.evaluate((element) => getComputedStyle(element).opacity))).toBeLessThan(1);
  await expect(acme.getByRole("button", { name: "Sign in to acme-internal" })).toBeVisible();
  await expect(acme).not.toContainText("Sign-in required.");
  await expect(card(panel, "github").locator(".connector-trouble")).toHaveCount(0);
});
