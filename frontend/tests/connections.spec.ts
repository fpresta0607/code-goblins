import { expect, test } from "@playwright/test";

const checkedAt = "2026-09-29T14:56:12Z";
const entries = [
  { id: "mcp:github", name: "GitHub", kind: "mcp", status: "connected", source: "Codex", checked_at: checkedAt },
  { id: "mcp:context7", name: "Context7", kind: "mcp", status: "unauthorized", detail: "Sign-in required.", checked_at: checkedAt, actions: ["login"] },
  { id: "mcp:sentry", name: "Sentry", kind: "mcp", status: "withheld", detail: "OAuth-only. Use a token or your own session.", checked_at: checkedAt },
  { id: "service:github", name: "GitHub CLI", kind: "service", status: "connected", checked_at: checkedAt },
  { id: "credential:TEST_TOKEN", name: "TEST_TOKEN", kind: "credential", status: "provided", checked_at: checkedAt },
];

for (const viewport of [{ width: 1280, height: 1400 }, { width: 390, height: 844 }]) {
  test("connections are readable and refresh after repair at width " + viewport.width, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.clock.install();
    let isFixed = false;
    let checks = 0;
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.route("**/api/**", async (route) => {
      const url = new URL(route.request().url());
      if (url.pathname === "/api/workspace") {
        await route.fulfill({ json: { repository: "code-goblins", root: "C:/scratch/work", harness: "codex", model: "Reported: gpt-6-astra", notes: [] } });
      } else if (url.pathname === "/api/connections" && route.request().method() === "GET") {
        checks++;
        await route.fulfill({ json: { instance: "connections-proof", checking: false, checked_at: checkedAt, entries: entries.map((entry) => entry.name === "Context7" && isFixed ? { ...entry, status: "connected", detail: "", actions: [] } : entry) } });
      } else if (url.pathname === "/api/connections/fix") {
        expect(route.request().headers()["x-cfo-token"]).toBe("connections-proof");
        expect(route.request().postDataJSON()).toEqual({ task: "connection-proof", generation: "first", connection: "mcp:context7", action: "login" });
        isFixed = true;
        await route.fulfill({ json: { message: "Login card is ready in the Command Center.", run_id: "connections-login-fixture" } });
      } else {
        await route.fulfill({ status: 404, json: { error: "Unknown fixture request" } });
      }
    });
    await page.goto("/tests/fixtures/connections.html");
    await page.getByText("Connections", { exact: true }).click();
    const panel = page.getByRole("region", { name: "Connections" });
    await expect(panel.getByText("Connected", { exact: true })).toHaveCount(2);
    await expect(panel.getByText("Withheld", { exact: true })).toBeVisible();
    await expect(panel.getByText("TEST_TOKEN", { exact: true })).toBeVisible();
    await expect(panel.getByText("Configured is not connected.", { exact: false })).toHaveCount(0);
    await page.locator("main").screenshot({ path: "test-results/connections-before-" + viewport.width + ".png" });
    const context7 = panel.getByRole("listitem").filter({ hasText: "Context7" });
    await context7.getByRole("button", { name: "Sign in to Context7" }).click();
    await expect(page).toHaveTitle("run:connections-login-fixture");
    await page.clock.runFor(5 * 60 * 1000);
    await expect(context7.getByText("Sign in", { exact: true })).toBeVisible();
    expect(checks).toBe(1);
    await page.evaluate(() => (window as unknown as { finishRepair: (id: string) => void }).finishRepair("connections-login-fixture"));
    await expect(context7.getByText("Connected", { exact: true })).toBeVisible();
    expect(checks).toBe(2);
    await page.evaluate(() => document.fonts.ready);
    const layout = await panel.evaluate((element) => ({
      overflow: element.scrollWidth > element.clientWidth,
      small: [...element.querySelectorAll("p, span, time, button, h4")].filter((node) => node.textContent?.trim() && parseFloat(getComputedStyle(node).fontSize) < 16).map((node) => node.textContent),
    }));
    expect(layout).toEqual({ overflow: false, small: [] });
    expect(errors).toEqual([]);
    await page.locator("main").screenshot({ path: "test-results/connections-" + viewport.width + ".png" });
  });
}

test("an open panel stays idle and reopening reads only the cached check", async ({ page }) => {
  await page.clock.install();
  const replies = [false, true, false];
  let reads = 0;
  await page.route("**/api/workspace**", (route) => route.fulfill({ json: { repository: "scratch", harness: "codex", notes: [] } }));
  await page.route("**/api/connections**", (route) => {
    const isChecking = replies[reads] ?? false;
    reads++;
    return route.fulfill({ json: { instance: "connections-proof", checking: isChecking, checked_at: checkedAt, entries: isChecking ? [] : [entries[0]] } });
  });
  await page.goto("/tests/fixtures/connections.html");
  const disclosure = page.getByText("Connections", { exact: true });
  await disclosure.click();
  await expect(page.getByText("Connection health", { exact: true })).toBeVisible();
  await page.clock.runFor(5 * 60 * 1000);
  expect(reads).toBe(1);
  await disclosure.click();
  await disclosure.click();
  await expect(page.getByText("Checking connections...", { exact: true })).toBeVisible();
  expect(reads).toBe(2);
  await page.clock.runFor(2000);
  await expect(page.getByText("Connection health", { exact: true })).toBeVisible();
  await page.clock.runFor(5 * 60 * 1000);
  expect(reads).toBe(3);
});

test("a slow check leaves the dropdown usable and ends in an honest timeout", async ({ page }) => {
  let hasTimedOut = false;
  await page.route("**/api/workspace**", (route) => route.fulfill({ json: { repository: "scratch", harness: "claude", notes: [] } }));
  await page.route("**/api/connections**", (route) => route.fulfill({ json: hasTimedOut
    ? { instance: "connections-proof", checking: false, entries: [], error: "Connection check timed out." }
    : { instance: "connections-proof", checking: true, entries: [] } }));
  await page.goto("/tests/fixtures/connections.html");
  const disclosure = page.getByText("Connections", { exact: true });
  await disclosure.click();
  await expect(page.getByText("Checking connections...", { exact: true })).toBeVisible();
  await disclosure.click();
  await expect(page.getByText("Checking connections...", { exact: true })).toBeHidden();
  hasTimedOut = true;
  await disclosure.click();
  await expect(page.getByRole("alert")).toContainText("Connection check timed out.");
});

test("OAuth opens its sign-in and a failed repair remains visible", async ({ page, context }) => {
  let isRejected = true;
  await context.route("https://example.invalid/**", (route) => route.fulfill({ body: "Scratch sign-in", contentType: "text/html" }));
  await page.route("**/api/workspace**", (route) => route.fulfill({ json: { repository: "scratch", harness: "claude", notes: [] } }));
  await page.route("**/api/connections**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/fix")) return route.fulfill(isRejected ? { status: 409, json: { error: "Task restarted. Refresh connections." } } : { json: { url: "https://example.invalid/login", message: "Complete sign-in, then recheck the connection." } });
    return route.fulfill({ json: { instance: "connections-proof", checking: false, entries: [entries[1]] } });
  });
  await page.goto("/tests/fixtures/connections.html");
  await page.getByText("Connections", { exact: true }).click();
  await page.getByRole("button", { name: "Sign in to Context7" }).click();
  await expect(page.getByRole("alert")).toHaveText("Task restarted. Refresh connections.");
  isRejected = false;
  const opened = page.waitForEvent("popup");
  await page.getByRole("button", { name: "Sign in to Context7" }).click();
  const popup = await opened;
  await expect(popup).toHaveURL("https://example.invalid/login");
  await expect(page.getByRole("link", { name: "Open sign-in" })).toHaveAttribute("rel", "noreferrer");
  await popup.close();
});
