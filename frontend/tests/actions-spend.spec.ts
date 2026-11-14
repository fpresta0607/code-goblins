import { expect, holdStream, servePages, test } from "./site";

const NOW = "2026-10-15T12:00:00Z";
// A month GitHub bills in part: its use at list price is $3.50 above its bill.
const total = (billed: number) => ({ minutes: 700, used_usd: billed + 3.5, billed_usd: billed });
const spent = (billed: number) => `$${billed.toFixed(2)} billed this month`;
const reading = (changes: Record<string, unknown> = {}) => ({ status: "available", month: "2026-10", total: total(8.5),
  repositories: [{ name: "moss", minutes: 400, used_usd: 7, billed_usd: 5 }, { name: "fern", minutes: 300, used_usd: 5, billed_usd: 3.5 }],
  read_at: NOW, checked_at: NOW, detail: "", ...changes });

for (const width of [1440, 390, 320]) {
  test(`billing permission repair names the account and preserves focus at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.clock.install({ time: new Date(NOW) });
    const unavailable = reading({ status: "unavailable", total: null, repositories: [], detail: "Billing permission is missing; eligibility may also affect access.",
      problem: { kind: "permission", action: "allow", account: "example-user", credential: "gh" } });
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true, actions_spend: unavailable });
    let repairs = 0;
    await page.route("**/api/actions-spend/repair", async (route) => {
      repairs++;
      expect(route.request().method()).toBe("POST");
      expect(route.request().headers()["x-ceo-token"]).toBe("actions-fixture");
      expect(route.request().postDataJSON()).toEqual({ month: "2026-10", action: "allow" });
      await route.fulfill({ json: { ...unavailable, recovery: { run_id: "billing-consent", state: "ready" } } });
    });
    await page.route("**/api/actions-spend?*", (route) => route.fulfill({ json: { ...unavailable, recovery: { run_id: "billing-consent", state: "ready" } } }));
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    const opener = page.getByRole("button", { name: "GitHub Actions spend: unavailable", exact: true });
    await opener.click();
    const panel = page.getByRole("dialog", { name: "GitHub Actions" });
    await expect(panel).toContainText("example-user");
    await expect(panel).toContainText("user scope");
    await expect(panel).not.toContainText("$0.00");
    const allow = panel.getByRole("button", { name: "Allow billing access", exact: true });
    await allow.click();
    await expect(panel.getByRole("status")).toContainText("Command Center");
    await expect(allow).toBeDisabled();
    expect(repairs).toBe(1);
    expect(await panel.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await panel.screenshot({ path: testInfo.outputPath(`billing-consent-${width}.png`) });
    await page.keyboard.press("Escape");
    await expect(opener).toBeFocused();
  });
}

test("transient billing failure retries once and displays the refreshed reading", async ({ page }) => {
  await page.clock.install({ time: new Date(NOW) });
  await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
    actions_spend: reading({ status: "unavailable", total: null, repositories: [], detail: "GitHub billing is temporarily unavailable.", problem: { kind: "transient", action: "retry", account: "example-user", credential: "gh" } }) });
  let repairs = 0;
  await page.route("**/api/actions-spend/repair", async (route) => {
    repairs++;
    expect(route.request().postDataJSON()).toEqual({ month: "2026-10", action: "retry" });
    await route.fulfill({ json: reading({ checked_at: "2026-10-15T12:00:01Z" }) });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.getByRole("button", { name: "GitHub Actions spend: unavailable", exact: true }).click();
  const panel = page.getByRole("dialog", { name: "GitHub Actions" });
  await expect(panel.getByRole("button", { name: "Allow billing access" })).toHaveCount(0);
  await panel.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(panel.locator("dl > div")).toHaveText(["Used$12.00", "Covered$3.50", "Billed$8.50"]);
  expect(repairs).toBe(1);
});

// A retry is one icon in the title row with no words beside it, which turns
// only while the read runs, and no progress text shows meanwhile (#2536).
test("a retry is one icon in the title row that turns only while the read runs", async ({ page }, testInfo) => {
  await page.clock.install({ time: new Date(NOW) });
  await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
    actions_spend: reading({ status: "unavailable", total: null, repositories: [], detail: "GitHub billing is temporarily unavailable.", problem: { kind: "transient", action: "retry", account: "example-user", credential: "gh" } }) });
  let release: () => void = () => { throw new Error("the retry has not started"); };
  await page.route("**/api/actions-spend/repair", async (route) => {
    await new Promise<void>((resolve) => { release = resolve; });
    await route.fulfill({ json: reading({ checked_at: "2026-10-15T12:00:01Z" }) });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.getByRole("button", { name: "GitHub Actions spend: unavailable", exact: true }).click();
  const panel = page.getByRole("dialog", { name: "GitHub Actions" });
  const retry = panel.getByRole("button", { name: "Retry", exact: true });
  await expect(retry).toHaveCount(1);
  await expect(panel.locator("header").getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await expect(retry).toHaveText("");
  const turning = () => retry.locator("svg").evaluate((icon) => getComputedStyle(icon).animationName);
  expect(await turning()).toBe("none");
  await retry.click();
  await expect(retry).toHaveAttribute("aria-busy", "true");
  expect(await turning()).toBe("terminal-spin");
  await expect(panel).not.toContainText("Checking billing");
  await expect(panel).not.toContainText("Reading GitHub billing");
  await panel.screenshot({ path: testInfo.outputPath("actions-retry-running.png") });
  release();
  await expect(panel.locator("dl > div")).toHaveText(["Used$12.00", "Covered$3.50", "Billed$8.50"]);
  await expect(retry).toHaveCount(0);
});

// The month beside the title opens this month and last month, and each shows
// its own figures and rows in the same window while the bar keeps this
// month's bill (#2536).
test("the month opens a menu of this month and last month, each with its own figures", async ({ page }, testInfo) => {
  await page.clock.install({ time: new Date(NOW) });
  await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true, actions_spend: reading() });
  const months: string[] = [];
  await page.route("**/api/actions-spend?*", (route) => {
    const month = new URL(route.request().url()).searchParams.get("month")!;
    months.push(month);
    expect(route.request().headers()["x-ceo-token"]).toBe("actions-fixture");
    return route.fulfill({ json: reading({ month, total: { minutes: 90, used_usd: 4.25, billed_usd: 1.25 }, repositories: [{ name: "reed", minutes: 90, used_usd: 4.25, billed_usd: 1.25 }],
      read_at: "2026-10-02T09:00:00Z", checked_at: "2026-10-02T09:00:00Z" }) });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  const header = page.getByRole("button", { name: `GitHub Actions spend: ${spent(8.5)}`, exact: true });
  await header.click();
  const panel = page.getByRole("dialog", { name: "GitHub Actions" });
  const month = panel.getByRole("button", { name: "Month: October 2026", exact: true });
  await expect(month).toHaveAttribute("aria-expanded", "false");
  await month.click();
  const menu = panel.getByRole("menu", { name: "Month" });
  await expect(menu.getByRole("menuitemradio")).toHaveText(["October 2026", "September 2026"]);
  await expect(menu.getByRole("menuitemradio", { name: "October 2026" })).toBeFocused();
  await expect(menu.getByRole("menuitemradio", { name: "October 2026" })).toHaveAttribute("aria-checked", "true");
  await page.keyboard.press("ArrowDown");
  await expect(menu.getByRole("menuitemradio", { name: "September 2026" })).toBeFocused();
  await panel.screenshot({ path: testInfo.outputPath("actions-month-menu.png") });
  await page.keyboard.press("Enter");
  await expect(menu).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "Month: September 2026", exact: true })).toBeFocused();
  await expect(panel.locator("dl > div")).toHaveText(["Used$4.25", "Covered$3.00", "Billed$1.25"]);
  await expect(panel.locator("tbody tr")).toHaveText(["reed90$4.25$1.25"]);
  expect(months).toEqual(["2026-09"]);
  await expect(header).toBeAttached();
  await panel.getByRole("button", { name: "Month: September 2026", exact: true }).click();
  await expect(menu.getByRole("menuitemradio", { name: "September 2026" })).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(menu.getByRole("menuitemradio", { name: "October 2026" })).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(menu.getByRole("menuitemradio", { name: "September 2026" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(panel.getByRole("menu")).toHaveCount(0);
  await expect(panel).toBeVisible();
  await panel.getByRole("button", { name: "Month: September 2026", exact: true }).click();
  await panel.getByRole("menuitemradio", { name: "October 2026" }).click();
  await expect(panel.locator("dl > div")).toHaveText(["Used$12.00", "Covered$3.50", "Billed$8.50"]);
  await expect(panel.locator("tbody tr")).toHaveText(["moss400$7.00$5.00", "fern300$5.00$3.50"]);
  expect(months).toEqual(["2026-09"]);
  await panel.getByRole("button", { name: "Month: October 2026", exact: true }).click();
  await expect(menu).toBeVisible();
  await panel.getByRole("heading", { name: "GitHub Actions" }).click();
  await expect(menu).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "Month: October 2026", exact: true })).toHaveAttribute("aria-expanded", "false");
  await panel.getByRole("button", { name: "Month: October 2026", exact: true }).click();
  await panel.getByRole("menuitemradio", { name: "September 2026" }).click();
  await expect(panel.locator("dl > div")).toHaveText(["Used$4.25", "Covered$3.00", "Billed$1.25"]);
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
  await header.click();
  await expect(panel.getByRole("button", { name: "Month: October 2026", exact: true })).toBeVisible();
  await expect(panel.locator("dl > div")).toHaveText(["Used$12.00", "Covered$3.50", "Billed$8.50"]);
  await expect(panel.locator("tbody tr")).toHaveText(["moss400$7.00$5.00", "fern300$5.00$3.50"]);
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
  await expect(page.getByRole("button", { name: `GitHub Actions spend: ${spent(8.5)}`, exact: true })).toBeVisible();
});

test("environment billing credential offers update guidance without CLI consent", async ({ page }) => {
  await page.clock.install({ time: new Date(NOW) });
  await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
    actions_spend: reading({ status: "unavailable", total: null, repositories: [], detail: "Update the selected environment credential.", problem: { kind: "permission", action: "update", account: "example-user", credential: "GH_TOKEN" } }) });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.getByRole("button", { name: "GitHub Actions spend: unavailable", exact: true }).click();
  const panel = page.getByRole("dialog", { name: "GitHub Actions" });
  await panel.getByRole("button", { name: "Update GH_TOKEN", exact: true }).click();
  await expect(panel.getByRole("status")).toContainText("environment that starts Code Goblins");
  await expect(panel.getByRole("button", { name: "Allow billing access" })).toHaveCount(0);
});

test("a missing CLI login offers sign-in guidance and Retry, never a consent run", async ({ page }) => {
  await page.clock.install({ time: new Date(NOW) });
  await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
    actions_spend: reading({ status: "unavailable", total: null, repositories: [], detail: "The GitHub CLI login on this PC is missing or was refused.", problem: { kind: "authentication", action: "renew", account: "", credential: "gh" } }) });
  const actions: unknown[] = [];
  await page.route("**/api/actions-spend/repair", async (route) => {
    actions.push(route.request().postDataJSON());
    await route.fulfill({ json: reading({ checked_at: "2026-10-15T12:00:01Z" }) });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.getByRole("button", { name: "GitHub Actions spend: unavailable", exact: true }).click();
  const panel = page.getByRole("dialog", { name: "GitHub Actions" });
  await panel.getByRole("button", { name: "Renew GitHub login", exact: true }).click();
  await expect(panel.getByRole("status")).toContainText("gh auth login");
  expect(actions).toEqual([]);
  await panel.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(panel.locator("dl > div")).toHaveText(["Used$12.00", "Covered$3.50", "Billed$8.50"]);
  expect(actions).toEqual([{ month: "2026-10", action: "retry" }]);
});

test("sign-in guidance leaves once a retried reading needs billing access instead", async ({ page }) => {
  await page.clock.install({ time: new Date(NOW) });
  await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
    actions_spend: reading({ status: "unavailable", total: null, repositories: [], detail: "The GitHub CLI login on this PC is missing or was refused.", problem: { kind: "authentication", action: "renew", account: "", credential: "gh" } }) });
  await page.route("**/api/actions-spend/repair", (route) => route.fulfill({ json: reading({ status: "unavailable", total: null, repositories: [], detail: "Billing permission is missing.", checked_at: "2026-10-15T12:00:01Z",
    problem: { kind: "permission", action: "allow", account: "example-user", credential: "gh" } }) }));
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.getByRole("button", { name: "GitHub Actions spend: unavailable", exact: true }).click();
  const panel = page.getByRole("dialog", { name: "GitHub Actions" });
  await panel.getByRole("button", { name: "Renew GitHub login", exact: true }).click();
  await expect(panel.getByRole("status")).toContainText("gh auth login");
  await panel.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(panel.getByRole("button", { name: "Allow billing access", exact: true })).toBeEnabled();
  await expect(panel).not.toContainText("gh auth login");
  await expect(panel).not.toContainText("environment that starts Code Goblins");
  await expect(panel.getByRole("button", { name: "Retry", exact: true })).toHaveCount(0);
});

test("billing repair shows loading and a recoverable owner refusal", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 1000 });
  await page.clock.install({ time: new Date(NOW) });
  await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
    actions_spend: reading({ status: "unavailable", total: null, repositories: [], problem: { kind: "permission", action: "allow", account: "example-user", credential: "gh" } }) });
  let release: () => void = () => { throw new Error("repair has not started"); };
  await page.route("**/api/actions-spend/repair", async (route) => {
    await new Promise<void>((resolve) => { release = resolve; });
    await route.fulfill({ status: 403, json: { error: "Allow billing access in the Code Goblins window on this PC." } });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.getByRole("button", { name: "GitHub Actions spend: unavailable", exact: true }).click();
  const panel = page.getByRole("dialog", { name: "GitHub Actions" });
  await expect(panel.getByRole("button", { name: "Close Actions spend" })).toBeFocused();
  await panel.getByRole("button", { name: "Allow billing access", exact: true }).click();
  await expect(panel.getByRole("button", { name: "Allow billing access", exact: true })).toBeDisabled();
  await expect(panel.getByRole("region", { name: "Billing recovery" })).toHaveAttribute("aria-busy", "true");
  await expect(panel).not.toContainText("Checking billing");
  release();
  await expect(panel.locator(".notice")).toContainText("on this PC");
  await expect(panel.getByRole("button", { name: "Allow billing access", exact: true })).toBeEnabled();
});

for (const state of ["succeeded", "failed", "withdrawn", "stopped"]) {
  test(`billing consent ${state} is read once without automatically repeating consent`, async ({ page }) => {
    await page.clock.install({ time: new Date(NOW) });
    const unavailable = reading({ status: "unavailable", total: null, repositories: [], detail: "Billing permission is missing.",
      problem: { kind: "permission", action: "allow", account: "example-user", credential: "gh" } });
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true, actions_spend: unavailable });
    let repairs = 0;
    let polls = 0;
    await page.route("**/api/actions-spend/repair", async (route) => {
      repairs++;
      await route.fulfill({ json: { ...unavailable, recovery: { run_id: "billing-consent", state: "ready" } } });
    });
    await page.route("**/api/actions-spend?*", (route) => {
      polls++;
      return route.fulfill({ json: { ...(state === "succeeded" ? reading({ checked_at: "2026-10-15T12:00:01Z" }) : unavailable), recovery: { run_id: "billing-consent", state } } });
    });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await page.getByRole("button", { name: "GitHub Actions spend: unavailable", exact: true }).click();
    const panel = page.getByRole("dialog", { name: "GitHub Actions" });
    await panel.getByRole("button", { name: "Allow billing access", exact: true }).click();
    await expect(panel.getByRole("status")).toContainText("Command Center");
    await page.clock.runFor(2000);
    if (state === "succeeded") await expect(panel.locator("dl > div")).toHaveText(["Used$12.00", "Covered$3.50", "Billed$8.50"]);
    else {
      await expect(panel.getByRole("status")).toContainText("Consent was not completed (" + state + ")");
      await expect(panel.getByRole("button", { name: "Allow billing access", exact: true })).toBeEnabled();
    }
    expect(repairs).toBe(1);
    expect(polls).toBe(1);
  });
}

test.describe("device-local Actions month", () => {
  test.use({ timezoneId: "America/Chicago" });

  test("keeps October's report while UTC is already in November", async ({ page }) => {
    await page.clock.install({ time: new Date("2026-11-01T00:30:00Z") });
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
      actions_spend: reading({ read_at: "2026-11-01T00:30:00Z", checked_at: "2026-11-01T00:30:00Z" }) });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await page.getByRole("button", { name: `GitHub Actions spend: ${spent(8.5)}`, exact: true }).click();
    await expect(page.getByRole("dialog", { name: "GitHub Actions" })).toContainText("October 2026");
  });
});

for (const scenario of [
  { zone: "America/Chicago", now: "2026-11-01T00:30:00Z", month: "2026-10", label: "October 2026", amount: 8.5, time: "Oct 31, 2026, 7:30 PM", isClosed: true },
  { zone: "Asia/Tokyo", now: "2026-11-01T00:30:00Z", month: "2026-11", label: "November 2026", amount: 40, time: "Nov 1, 2026, 9:30 AM", isClosed: false },
  { zone: "Pacific/Kiritimati", now: "2026-12-31T10:30:00Z", month: "2027-01", label: "January 2027", amount: 40, time: "Jan 1, 2027, 12:30 AM", isClosed: false },
  { zone: "America/Chicago", now: "2027-01-01T00:30:00Z", month: "2026-12", label: "December 2026", amount: 8.5, time: "Dec 31, 2026, 6:30 PM", isClosed: false },
  { zone: "America/Los_Angeles", now: "2028-03-01T00:30:00Z", month: "2028-02", label: "February 2028", amount: 8.5, time: "Feb 29, 2028, 4:30 PM", isClosed: false },
  { zone: "America/New_York", now: "2026-04-01T00:30:00Z", month: "2026-03", label: "March 2026", amount: 8.5, time: "Mar 31, 2026, 8:30 PM", isClosed: false },
]) {
  test.describe(`Actions calendar in ${scenario.zone} at ${scenario.now}`, () => {
    test.use({ timezoneId: scenario.zone });
    test("requests the local month and displays absolute reading times locally", async ({ page }) => {
      await page.clock.install({ time: new Date(scenario.now) });
      const months: string[] = [];
      await page.route("**/api/actions-spend?*", (route) => {
        months.push(new URL(route.request().url()).searchParams.get("month") || "");
        expect(route.request().headers()["x-ceo-token"]).toBe("actions-fixture");
        return route.fulfill({ json: reading({ month: scenario.month, total: total(scenario.amount), read_at: scenario.now, checked_at: scenario.now }) });
      });
      await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true, ceo_closed: scenario.isClosed,
        actions_spend: reading({ month: scenario.now.slice(0, 7), total: total(40), read_at: scenario.now, checked_at: scenario.now }) });
      await page.goto("/");
      await page.getByRole("button", { name: "Board", exact: true }).click();
      await page.getByRole("button", { name: `GitHub Actions spend: ${spent(scenario.amount)}`, exact: true }).click();
      const panel = page.getByRole("dialog", { name: "GitHub Actions" });
      await expect(panel).toContainText(scenario.label);
      await expect(panel.locator("time")).toHaveText(scenario.time);
      await expect(panel.locator("time")).toHaveAttribute("datetime", scenario.now);
      expect(months).toEqual(scenario.month === scenario.now.slice(0, 7) ? [] : [scenario.month]);
    });
  });
}

test.describe("Actions rollover on an open device", () => {
  test.use({ timezoneId: "America/Chicago" });

  for (const scenario of [
    { initial: "available", refreshed: "stale" },
    { initial: "unavailable", refreshed: "available" },
    { initial: "stale", refreshed: "available" },
    { initial: "available", refreshed: "request failure" },
  ]) {
    test(`refreshes ${scenario.initial} usage to ${scenario.refreshed} on the selected month's offset schedule`, async ({ page }) => {
      await page.clock.install({ time: new Date("2026-11-01T00:10:00Z") });
      const requests: string[] = [];
      await page.route("**/api/actions-spend?*", async (route) => {
        const now = await page.evaluate(() => new Date().toISOString());
        requests.push(now);
        expect(new URL(route.request().url()).searchParams.get("month")).toBe("2026-10");
        if (now < "2026-11-01T01:10:00") {
          return route.fulfill({ json: reading({ status: scenario.initial, total: scenario.initial === "unavailable" ? null : total(8.5),
            read_at: scenario.initial === "unavailable" ? "" : scenario.initial === "stale" ? "2026-10-31T23:10:00Z" : "2026-11-01T00:10:00Z",
            checked_at: "2026-11-01T00:10:00Z" }) });
        }
        if (now < "2026-11-01T02:10:00") {
          if (scenario.refreshed === "request failure") return route.fulfill({ status: 503, json: { error: "Billing unavailable" } });
          return route.fulfill({ json: reading({ status: scenario.refreshed, total: total(scenario.refreshed === "stale" ? 8.5 : 12),
            read_at: scenario.refreshed === "stale" ? "2026-11-01T00:10:00Z" : "2026-11-01T01:10:00Z",
            checked_at: "2026-11-01T01:10:00Z", detail: scenario.refreshed === "stale" ? "Could not refresh usage." : "" }) });
        }
        return route.fulfill({ json: reading({ total: total(15), read_at: "2026-11-01T02:10:00Z", checked_at: "2026-11-01T02:10:00Z" }) });
      });
      await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
        actions_spend: reading({ month: "2026-11", total: total(40), read_at: "2026-11-01T00:05:00Z", checked_at: "2026-11-01T00:05:00Z" }) });
      await page.goto("/");
      await page.getByRole("button", { name: "Board", exact: true }).click();
      const initialLabel = scenario.initial === "unavailable" ? "unavailable" : spent(8.5) + (scenario.initial === "stale" ? " (old)" : "");
      await page.getByRole("button", { name: `GitHub Actions spend: ${initialLabel}`, exact: true }).click();
      const panel = page.getByRole("dialog", { name: "GitHub Actions" });
      await expect(panel).toContainText("October 2026");
      await page.clock.pauseAt(new Date("2026-11-01T00:11:00Z"));

      await page.clock.runFor(54 * 60_000);
      expect(requests).toHaveLength(1);
      await page.clock.runFor(5 * 60_000);
      const refreshedLabel = scenario.refreshed === "stale" ? spent(8.5) + " (old)" : scenario.refreshed === "available" ? spent(12) : "unavailable";
      await expect(page.getByRole("button", { name: `GitHub Actions spend: ${refreshedLabel}`, exact: true })).toBeVisible();
      if (scenario.refreshed === "stale") {
        await expect(panel).toContainText("Could not refresh usage.");
        await expect(panel.getByText("Old", { exact: true })).toBeVisible();
        await expect(panel.locator("time").nth(1)).toHaveAttribute("datetime", "2026-11-01T01:10:00Z");
      } else if (scenario.refreshed === "available") {
        await expect(panel).toContainText("$12.00");
        await expect(panel.getByText("Old", { exact: true })).toHaveCount(0);
      } else {
        await expect(panel).toContainText("Could not read this month's GitHub Actions usage.");
        await expect(panel).not.toContainText("$8.50");
      }
      expect(requests).toHaveLength(2);

      await page.clock.runFor(55 * 60_000);
      expect(requests).toHaveLength(2);
      await page.clock.runFor(5 * 60_000);
      await expect(page.getByRole("button", { name: `GitHub Actions spend: ${spent(15)}`, exact: true })).toBeVisible();
      await expect(panel).toContainText("$15.00");
      await expect(panel.getByText("Old", { exact: true })).toHaveCount(0);
      expect(requests).toHaveLength(3);
    });
  }

  for (const scenario of [
    { initial: "available", refreshed: "stale", isAlreadyDue: false },
    { initial: "unavailable", refreshed: "available", isAlreadyDue: false },
    { initial: "stale", refreshed: "available", isAlreadyDue: false },
    { initial: "available", refreshed: "available", isAlreadyDue: true },
  ]) {
    test(`refreshes ${scenario.initial} usage after an unchanged cache hit${scenario.isAlreadyDue ? " on initial load" : ""}`, async ({ page }) => {
      await page.clock.install({ time: new Date(scenario.isAlreadyDue ? "2026-11-01T01:10:00Z" : "2026-11-01T01:09:00Z") });
      const requests: number[] = [];
      await page.route("**/api/actions-spend?*", async (route) => {
        expect(new URL(route.request().url()).searchParams.get("month")).toBe("2026-10");
        const deviceNow = await page.evaluate(() => Date.now());
        const serverNow = deviceNow - 5_000;
        requests.push(deviceNow);
        if (serverNow < Date.parse("2026-11-01T01:10:00Z")) {
          return route.fulfill({ json: reading({ status: scenario.initial, total: scenario.initial === "unavailable" ? null : total(8.5),
            read_at: scenario.initial === "unavailable" ? "" : scenario.initial === "stale" ? "2026-10-31T23:10:00Z" : "2026-11-01T00:10:00Z",
            checked_at: "2026-11-01T00:10:00Z", detail: scenario.initial === "unavailable" ? "Could not read usage." : "" }) });
        }
        return route.fulfill({ json: reading({ status: scenario.refreshed, total: total(scenario.refreshed === "stale" ? 8.5 : 12),
          read_at: scenario.refreshed === "stale" ? "2026-11-01T00:10:00Z" : "2026-11-01T01:10:55Z",
          checked_at: "2026-11-01T01:10:55Z", detail: scenario.refreshed === "stale" ? "Could not refresh usage." : "" }) });
      });
      await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
        actions_spend: reading({ month: "2026-11", total: total(40), read_at: "2026-11-01T00:05:00Z", checked_at: "2026-11-01T00:05:00Z" }) });
      await page.goto("/");
      await page.getByRole("button", { name: "Board", exact: true }).click();
      const initialLabel = scenario.initial === "unavailable" ? "unavailable" : spent(8.5) + (scenario.initial === "stale" ? " (old)" : "");
      await page.getByRole("button", { name: `GitHub Actions spend: ${initialLabel}`, exact: true }).click();
      const panel = page.getByRole("dialog", { name: "GitHub Actions" });
      // A first failed read has no reading time, only its problem line.
      const showsInitialReading = () => scenario.initial === "unavailable"
        ? expect(panel).toContainText("Could not read usage.")
        : expect(panel.locator("time").last()).toHaveAttribute("datetime", "2026-11-01T00:10:00Z");
      await showsInitialReading();

      await page.clock.pauseAt(new Date(scenario.isAlreadyDue ? "2026-11-01T01:10:30Z" : "2026-11-01T01:10:00Z"));
      const cachedRequests = scenario.isAlreadyDue ? 1 : 2;
      await expect.poll(() => requests.length).toBe(cachedRequests);
      await expect(page.getByRole("button", { name: `GitHub Actions spend: ${initialLabel}`, exact: true })).toBeVisible();
      await showsInitialReading();

      await page.clock.runFor(scenario.isAlreadyDue ? 30_000 : 60_000);
      const refreshedLabel = scenario.refreshed === "stale" ? spent(8.5) + " (old)" : spent(12);
      await expect(page.getByRole("button", { name: `GitHub Actions spend: ${refreshedLabel}`, exact: true })).toBeVisible();
      await expect(panel).toContainText("October 2026");
      await expect(panel.locator("time").last()).toHaveAttribute("datetime", "2026-11-01T01:10:55Z");
      if (scenario.refreshed === "stale") {
        await expect(panel.getByText("Old", { exact: true })).toBeVisible();
        await expect(panel).toContainText("Could not refresh usage.");
        await expect(panel.locator("time").first()).toHaveAttribute("datetime", "2026-11-01T00:10:00Z");
      } else {
        await expect(panel).toContainText("$12.00");
        await expect(panel.getByText("Old", { exact: true })).toHaveCount(0);
      }
      expect(requests).toHaveLength(cachedRequests + 1);

      await page.clock.runFor(60_000);
      expect(requests).toHaveLength(cachedRequests + 1);
    });
  }

  test("a device clock hours ahead asks for its month at most once a minute", async ({ page }) => {
    await page.clock.install({ time: new Date("2026-11-01T02:30:00Z") });
    let requests = 0;
    // The supervisor's clock is two hours behind this device's.
    await page.route("**/api/actions-spend?*", (route) => {
      requests++;
      return route.fulfill({ json: reading({ read_at: "2026-11-01T00:30:00Z", checked_at: "2026-11-01T00:30:00Z" }) });
    });
    const snapshot = { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
      actions_spend: reading({ month: "2026-11", total: total(40), read_at: "2026-11-01T00:30:00Z", checked_at: "2026-11-01T00:30:00Z" }) };
    await page.addInitScript((data) => {
      const streams: EventTarget[] = [];
      class PushedStream extends EventTarget {
        onerror: (() => void) | null = null;
        constructor() {
          super();
          streams.push(this);
          setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data })));
        }
        close() {}
      }
      Object.assign(window, { EventSource: PushedStream, pushSnapshot: () => { for (const stream of streams) stream.dispatchEvent(new MessageEvent("snapshot", { data })); } });
    }, JSON.stringify(snapshot));
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await expect(page.getByRole("button", { name: `GitHub Actions spend: ${spent(8.5)}`, exact: true })).toBeVisible();
    await page.clock.pauseAt(new Date("2026-11-01T02:31:30Z"));
    await expect.poll(() => requests).toBeGreaterThanOrEqual(2);
    const settled = requests;

    for (let update = 0; update < 12; update++) {
      await page.clock.runFor(10_000);
      await page.evaluate(() => (window as unknown as { pushSnapshot: () => void }).pushSnapshot());
    }
    expect(requests - settled).toBeLessThanOrEqual(2);
  });

  test("a device clock hours behind still checks its month within the hour", async ({ page }) => {
    await page.clock.install({ time: new Date("2026-10-31T21:30:00Z") });
    let requests = 0;
    // The supervisor's clock is three hours ahead of this device's.
    await page.route("**/api/actions-spend?*", (route) => {
      requests++;
      return route.fulfill({ json: requests === 1
        ? reading({ read_at: "2026-11-01T00:30:00Z", checked_at: "2026-11-01T00:30:00Z" })
        : reading({ status: "stale", read_at: "2026-11-01T00:30:00Z", checked_at: "2026-11-01T01:30:00Z", detail: "Could not refresh usage." }) });
    });
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
      actions_spend: reading({ month: "2026-11", total: total(40), read_at: "2026-11-01T00:30:00Z", checked_at: "2026-11-01T00:30:00Z" }) });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await expect(page.getByRole("button", { name: `GitHub Actions spend: ${spent(8.5)}`, exact: true })).toBeVisible();
    await page.clock.pauseAt(new Date("2026-10-31T21:31:00Z"));

    await page.clock.runFor(58 * 60_000);
    expect(requests).toBe(1);
    await page.clock.runFor(2 * 60_000);
    await expect(page.getByRole("button", { name: `GitHub Actions spend: ${spent(8.5)} (old)`, exact: true })).toBeVisible();
    expect(requests).toBe(2);
  });

  test("switches reports at local midnight and contains a failed refresh", async ({ page }) => {
    await page.clock.install({ time: new Date("2026-11-01T04:59:30Z") });
    let release: () => void = () => undefined;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const months: string[] = [];
    await page.route("**/api/actions-spend?*", async (route) => {
      months.push(new URL(route.request().url()).searchParams.get("month") || "");
      await pending;
      await route.fulfill({ status: 503, json: { error: "Billing unavailable" } });
    });
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
      actions_spend: reading({ read_at: "2026-11-01T04:59:30Z", checked_at: "2026-11-01T04:59:30Z" }) });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await page.getByRole("button", { name: `GitHub Actions spend: ${spent(8.5)}`, exact: true }).click();
    const panel = page.getByRole("dialog", { name: "GitHub Actions" });
    await expect(panel).toContainText("October 2026");
    await page.clock.runFor(30_000);
    await expect(panel).toContainText("November 2026");
    await expect(panel).toContainText("Waiting for this month's GitHub Actions usage.");
    await expect(panel).not.toContainText("$8.50");
    await expect(panel).not.toContainText("$0.00");
    await expect.poll(() => months).toEqual(["2026-11"]);
    release();
    await expect(panel).toContainText("Could not read this month's GitHub Actions usage.");
    await page.clock.runFor(60_000);
    expect(months).toEqual(["2026-11"]);
  });
});

for (const scenario of [
  { now: "2027-01-01T05:59:30Z", before: "2026-12", after: "2027-01", label: "January 2027" },
  { now: "2028-03-01T05:59:30Z", before: "2028-02", after: "2028-03", label: "March 2028" },
]) {
  test.describe(`open Actions calendar from ${scenario.before} to ${scenario.after}`, () => {
    test.use({ timezoneId: "America/Chicago" });
    test("loads the next report at local midnight with truthful reading and check times", async ({ page }) => {
      await page.clock.install({ time: new Date(scenario.now) });
      const checkedAt = new Date(Date.parse(scenario.now) + 30_000).toISOString();
      await page.route("**/api/actions-spend?*", (route) => {
        expect(new URL(route.request().url()).searchParams.get("month")).toBe(scenario.after);
        return route.fulfill({ json: reading({ status: "stale", month: scenario.after, total: total(40), read_at: scenario.now, checked_at: checkedAt, detail: "Could not refresh usage." }) });
      });
      await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true,
        actions_spend: reading({ month: scenario.before, read_at: scenario.now, checked_at: scenario.now }) });
      await page.goto("/");
      await page.getByRole("button", { name: "Board", exact: true }).click();
      await page.getByRole("button", { name: `GitHub Actions spend: ${spent(8.5)}`, exact: true }).click();
      await page.clock.runFor(30_000);
      const panel = page.getByRole("dialog", { name: "GitHub Actions" });
      await expect(panel).toContainText(scenario.label);
      await expect(panel).toContainText("$40.00");
      await expect(panel).not.toContainText("$8.50");
      await expect(panel.locator("time").nth(0)).toHaveAttribute("datetime", scenario.now);
      await expect(panel.locator("time").nth(1)).toHaveAttribute("datetime", checkedAt);
      await expect(panel.locator("time").nth(0)).toContainText("11:59 PM");
      await expect(panel.locator("time").nth(1)).toContainText("12:00 AM");
    });
  });
}

test("devices in different zones select independent months from the same board", async ({ browser }) => {
  const now = "2026-11-01T00:30:00Z";
  const contexts = [];
  try {
    for (const [zone, month, amount, label] of [
      ["America/Chicago", "2026-10", 8.5, "October 2026"],
      ["Asia/Tokyo", "2026-11", 40, "November 2026"],
    ] as const) {
      const context = await browser.newContext({ timezoneId: zone });
      contexts.push(context);
      await servePages(context);
      const page = await context.newPage();
      await page.clock.install({ time: new Date(now) });
      await page.route("**/api/actions-spend?*", (route) => {
        expect(new URL(route.request().url()).searchParams.get("month")).toBe(month);
        return route.fulfill({ json: reading({ month, total: total(amount), read_at: now, checked_at: now }) });
      });
      await holdStream(page, { instance: "same-board", revision: 1, healthy: true, example: true, ceo_runs: true,
        actions_spend: reading({ month: "2026-11", total: total(40), read_at: now, checked_at: now }) });
      await page.goto("/");
      await page.getByRole("button", { name: "Board", exact: true }).click();
      await page.getByRole("button", { name: `GitHub Actions spend: ${spent(amount)}`, exact: true }).click();
      await expect(page.getByRole("dialog", { name: "GitHub Actions" })).toContainText(label);
    }
  } finally {
    for (const context of contexts) await context.close();
  }
});

test.describe("Actions spend panel", () => {
  test.use({ timezoneId: "UTC" });

  for (const month of [
    { name: "partly billed", changes: {}, label: spent(8.5), figures: ["Used$12.00", "Covered$3.50", "Billed$8.50"], rows: ["moss400$7.00$5.00", "fern300$5.00$3.50"] },
    { name: "fully covered", label: "$0.00 billed this month", figures: ["Used$9.60", "Covered$9.60", "Billed$0.00"], rows: ["moss1,200$6.40$0.00", "fern400$3.20$0.00"],
      changes: { total: { minutes: 1600, used_usd: 9.6, billed_usd: 0 }, repositories: [{ name: "moss", minutes: 1200, used_usd: 6.4, billed_usd: 0 }, { name: "fern", minutes: 400, used_usd: 3.2, billed_usd: 0 }] } },
  ]) {
    for (const width of [1440, 390, 320]) {
      test(`the header opens a ${month.name} month's panel at ${width}px`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width, height: 1100 });
        await page.clock.install({ time: new Date(NOW) });
        const panelRequests: string[] = [];
        page.on("request", (request) => { if (request.url().includes("/actions-spend-panel-")) panelRequests.push(request.url()); });
        await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true, ceo_harness: "codex",
          subscriptions: [{ provider: "codex", status: "available", source: "oauth", percent_remaining: 75, read_at: NOW, resets_at: "2026-10-20T00:00:00Z", floor_percent: 20 }], actions_spend: reading(month.changes) });
        await page.goto("/");
        await page.getByRole("button", { name: "Board", exact: true }).click();
        const button = page.getByRole("button", { name: `GitHub Actions spend: ${month.label}`, exact: true });
        await expect(button).toBeVisible();
        await expect(button.locator("svg")).toBeVisible();
        // The bar shows the GitHub mark and the billed amount alone, a gap
        // apart, and what was used is the panel's (2026-11-04).
        await expect(button).toHaveText(month.label.split(" ")[0]);
        const [mark, amount] = await Promise.all([button.locator("svg").boundingBox(), button.locator(".actions-spend-billed").boundingBox()]);
        if (!mark || !amount) throw new Error("the mark or the amount is not drawn");
        expect(Math.round(amount.x - mark.x - mark.width), "the mark and the amount keep the bar's gap").toBe(6);
        expect(Math.abs(amount.y + amount.height / 2 - (mark.y + mark.height / 2)), "the mark and the amount share one line").toBeLessThanOrEqual(1);
        // What shows of it sits the row's own gap from the items either side, on
        // a phone only before it, since the AFK switch and the button that opens
        // the CEO take the next line there.
        if (width >= 390) {
          const around = await button.evaluate((element) => {
            const drawn = (selector: string) => element.querySelector(selector)!.getBoundingClientRect();
            return {
              gap: parseFloat(getComputedStyle(element.parentElement!).columnGap),
              before: drawn("svg").left - element.previousElementSibling!.getBoundingClientRect().right,
              isNextBeside: element.nextElementSibling!.getBoundingClientRect().top < element.getBoundingClientRect().bottom,
              after: element.nextElementSibling!.getBoundingClientRect().left - drawn(".actions-spend-billed").right,
            };
          });
          expect(Math.round(around.before), "the row's gap before the mark").toBe(around.gap);
          expect(around.isNextBeside, "the next item shares its line").toBe(width > 390);
          if (around.isNextBeside) expect(Math.round(around.after), "the row's gap after the amount").toBe(around.gap);
        }
        expect(await button.evaluate((element) => element.getBoundingClientRect().right <= innerWidth && element.scrollWidth <= element.clientWidth)).toBe(true);
        expect(panelRequests).toHaveLength(0);
        await page.evaluate(() => document.fonts.ready);
        const slug = month.name.replaceAll(" ", "-");
        await page.getByRole("group", { name: "CEO", exact: true }).screenshot({ path: testInfo.outputPath(`actions-header-${slug}-${width}.png`) });
        await button.click();
        const panel = page.getByRole("dialog", { name: "GitHub Actions" });
        await expect(panel).toBeVisible();
        expect(panelRequests).toHaveLength(1);
        const [title, label, close] = await Promise.all([panel.getByRole("heading").boundingBox(), panel.getByText("October 2026", { exact: true }).boundingBox(),
          panel.getByRole("button", { name: "Close Actions spend" }).boundingBox()]);
        if (!title || !label || !close) throw new Error("the title line is not drawn");
        const isBeside = (a: typeof title, b: typeof title) => a.y < b.y + b.height && b.y < a.y + a.height;
        expect(label.x + label.width <= close.x || !isBeside(label, close)).toBe(true);
        expect(label.height).toBeLessThan(title.height + 4);
        if (width >= 390) expect(isBeside(title, label)).toBe(true);
        await expect(panel.locator("dl > div")).toHaveText(month.figures);
        await expect(panel.locator("tbody tr")).toHaveText(month.rows);
        await expect(panel.locator("p")).toHaveText(["Read Oct 15, 2026, 12:00 PM"]);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        expect(await panel.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
        await panel.screenshot({ path: testInfo.outputPath(`actions-panel-${slug}-${width}.png`) });
        await page.keyboard.press("Escape");
        await expect(panel).toHaveCount(0);
        await expect(button).toBeFocused();
      });
    }
  }

  for (const [name, changes, label, lines] of [
    ["failed first read", { status: "unavailable", total: null, repositories: [], read_at: "", detail: "Check the token's user scope." }, "unavailable", ["Check the token's user scope."]],
    ["failed later read", { status: "stale", checked_at: "2026-10-15T13:00:00Z", detail: "Could not read usage. Check the token's user scope." }, spent(8.5) + " (old)",
      ["Could not read usage. Check the token's user scope.", "Read Oct 15, 2026, 12:00 PM · Old · Last attempt Oct 15, 2026, 1:00 PM"]],
    ["aged reading", { status: "stale" }, spent(8.5) + " (old)", ["Read Oct 15, 2026, 12:00 PM · Old"]],
    ["new calendar month", { month: "2026-09" }, "unavailable", ["Waiting for this month's GitHub Actions usage."]],
    ["month without Actions use", { total: { minutes: 0, used_usd: 0, billed_usd: 0 }, repositories: [] }, "$0.00 billed this month", ["No Actions use this month.", "Read Oct 15, 2026, 12:00 PM"]],
  ] as const) {
    test(`shows a ${name} in one line each without guessing`, async ({ page }, testInfo) => {
      await page.clock.install({ time: new Date(NOW) });
      await page.route("**/api/actions-spend?*", (route) => route.fulfill({ json: reading({ status: "unavailable", total: null, repositories: [], detail: "Waiting for this month's GitHub Actions usage." }) }));
      await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, ceo_runs: true, actions_spend: reading(changes) });
      await page.goto("/");
      await page.getByRole("button", { name: "Board", exact: true }).click();
      await page.getByRole("button", { name: `GitHub Actions spend: ${label}`, exact: true }).click();
      const panel = page.getByRole("dialog", { name: "GitHub Actions" });
      await expect(panel.locator("p")).toHaveText([...lines]);
      if (label === "unavailable") await expect(panel).not.toContainText("$");
      await panel.screenshot({ path: testInfo.outputPath(`actions-${name.replaceAll(" ", "-")}.png`) });
    });
  }
});
