import { expect, test } from "./site";

const engines = { harnesses: [{ id: "codex", name: "Codex", models: [{ id: "gpt-6-astra", name: "gpt-6-astra", efforts: ["xhigh"], default_effort: "xhigh" }] }] };
const now = Date.parse("2026-10-13T20:00:00Z");
const hoursAgo = (hours: number) => new Date(now - hours * 3_600_000).toISOString();
const own = { id: "stripe-precisiondocs", provider: "Stripe", account: "PrecisionDocs", projects: ["code-goblins"], signed_in: true, signed_in_at: hoursAgo(2), used_at: hoursAgo(5 / 60), signs_out_after_fetch: false, in_use: false };
const shared = { id: "stripe-siqstack", provider: "Stripe", account: "SIQstack", projects: ["SIQshift", "code-goblins"], signed_in: false, used_at: hoursAgo(72), signs_out_after_fetch: true, in_use: false };

for (const viewport of [{ width: 1280, height: 1400 }, { width: 390, height: 844 }]) {
  test("a goblin's connections list its project's logins, sign out of one and forget another only once asked, at width " + viewport.width, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.clock.install({ time: now });
    let logins = [own, shared];
    const posted: { path: string; token: string | undefined; body: unknown }[] = [];
    await page.route("**/api/**", async (route) => {
      const url = new URL(route.request().url());
      if (url.pathname === "/api/engines") {
        await route.fulfill({ json: engines });
      } else if (url.pathname === "/api/workspace") {
        await route.fulfill({ json: { repository: "code-goblins", root: "C:/scratch/work", harness: "codex", model: "Reported: gpt-6-astra", notes: [] } });
      } else if (url.pathname === "/api/connections") {
        await route.fulfill({ json: { instance: "connections-proof", checking: false, checked_at: hoursAgo(0), entries: [], browser_logins: logins } });
      } else if (url.pathname === "/api/browser-logins/sign-out" || url.pathname === "/api/browser-logins/forget") {
        posted.push({ path: url.pathname, token: route.request().headers()["x-cfo-token"], body: route.request().postDataJSON() });
        logins = url.pathname.endsWith("forget") ? logins.filter((login) => login.id !== "stripe-siqstack") : logins.map((login) => login.id === "stripe-precisiondocs" ? { ...login, signed_in: false, signed_in_at: "" } : login);
        await route.fulfill({ json: { browser_logins: logins } });
      } else {
        await route.fulfill({ status: 404, json: { error: "Unknown fixture request" } });
      }
    });
    await page.goto("/tests/fixtures/connections.html");
    await page.getByText("Connections", { exact: true }).click();
    const list = page.getByRole("list", { name: "Browser logins" });
    const precision = list.getByRole("listitem").filter({ hasText: "Stripe · PrecisionDocs" });
    const siqstack = list.getByRole("listitem").filter({ hasText: "Stripe · SIQstack" });

    await expect(precision.getByText("Signed in", { exact: true })).toBeVisible();
    await expect(precision.getByText("For code-goblins. You signed in 2 hours ago, last used 5 minutes ago.", { exact: true })).toBeVisible();
    await expect(siqstack.getByText("Signed out", { exact: true })).toBeVisible();
    await expect(siqstack.getByText("For SIQshift and code-goblins. It signs out after each fetch, so each fetch brings the sign-in card. Last used 3 days ago.", { exact: true })).toBeVisible();
    await expect(siqstack.getByRole("button", { name: "Sign out of Stripe · SIQstack" })).toHaveCount(0);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    // Each login's name shows whole, so two logins of one provider are told
    // apart at every width: its signed-in state goes below it when both do
    // not fit on one line.
    for (const name of ["Stripe · PrecisionDocs", "Stripe · SIQstack"]) {
      const isWhole = await list.getByText(name, { exact: true }).evaluate((element) => element.scrollWidth <= element.clientWidth);
      expect(isWhole, name + " shows whole").toBe(true);
    }
    await page.locator("main").screenshot({ path: "test-results/browser-logins-" + viewport.width + ".png" });

    await precision.getByRole("button", { name: "Sign out of Stripe · PrecisionDocs" }).click();
    await expect(precision.getByText("Signed out", { exact: true })).toBeVisible();
    expect(posted).toEqual([{ path: "/api/browser-logins/sign-out", token: "connections-proof", body: { task: "connection-proof", generation: "first", login: "stripe-precisiondocs" } }]);

    await siqstack.getByRole("button", { name: "Forget Stripe · SIQstack" }).click();
    const dialog = page.getByRole("dialog", { name: "Forget this login?" });
    await expect(dialog).toContainText("Stripe · SIQstack. Its browser profile is deleted, so the next fetch for SIQshift and code-goblins asks you to sign in again. Your Stripe account and its keys are not touched.");
    await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toHaveCount(0);
    expect(posted).toHaveLength(1);
    await siqstack.getByRole("button", { name: "Forget Stripe · SIQstack" }).click();
    await page.getByRole("dialog", { name: "Forget this login?" }).getByRole("button", { name: "Forget" }).click();
    await expect(siqstack).toHaveCount(0);
    expect(posted[1]).toEqual({ path: "/api/browser-logins/forget", token: "connections-proof", body: { task: "connection-proof", generation: "first", login: "stripe-siqstack" } });
  });
}

test("a login whose browser is in use waits for it to close before it signs out or is forgotten", async ({ page }) => {
  await page.clock.install({ time: now });
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/engines") await route.fulfill({ json: engines });
    else if (url.pathname === "/api/workspace") await route.fulfill({ json: { repository: "code-goblins", root: "C:/scratch/work", harness: "codex", model: "Reported: gpt-6-astra", notes: [] } });
    else if (url.pathname === "/api/connections") await route.fulfill({ json: { instance: "connections-proof", checking: false, checked_at: hoursAgo(0), entries: [], browser_logins: [{ ...own, in_use: true }] } });
    else await route.fulfill({ status: 404, json: { error: "Unknown fixture request" } });
  });
  await page.goto("/tests/fixtures/connections.html");
  await page.getByText("Connections", { exact: true }).click();
  const row = page.getByRole("list", { name: "Browser logins" }).getByRole("listitem");
  await expect(row.getByRole("button", { name: "Sign out of Stripe · PrecisionDocs" })).toBeDisabled();
  await expect(row.getByRole("button", { name: "Forget Stripe · PrecisionDocs" })).toBeDisabled();
});
