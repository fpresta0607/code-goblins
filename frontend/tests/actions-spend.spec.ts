import { expect, holdStream, servePages, test } from "./site";

const NOW = "2026-10-15T12:00:00Z";
const reading = (changes: Record<string, unknown> = {}) => ({ status: "available", month: "2026-10", total_usd: 8.5,
  repositories: [{ name: "example-user/moss", spent_usd: 5 }, { name: "example-user/fern", spent_usd: 3.5 }],
  read_at: NOW, checked_at: NOW, detail: "", ...changes });

test.describe("device-local Actions month", () => {
  test.use({ timezoneId: "America/Chicago" });

  test("keeps October's report while UTC is already in November", async ({ page }) => {
    await page.clock.install({ time: new Date("2026-11-01T00:30:00Z") });
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, cfo_runs: true,
      actions_spend: reading({ read_at: "2026-11-01T00:30:00Z", checked_at: "2026-11-01T00:30:00Z" }) });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await page.getByRole("button", { name: "GitHub Actions spend: $8.50 this month", exact: true }).click();
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
        expect(route.request().headers()["x-cfo-token"]).toBe("actions-fixture");
        return route.fulfill({ json: reading({ month: scenario.month, total_usd: scenario.amount, read_at: scenario.now, checked_at: scenario.now }) });
      });
      await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, cfo_runs: true, cfo_closed: scenario.isClosed,
        actions_spend: reading({ month: scenario.now.slice(0, 7), total_usd: 40, read_at: scenario.now, checked_at: scenario.now }) });
      await page.goto("/");
      await page.getByRole("button", { name: "Board", exact: true }).click();
      await page.getByRole("button", { name: `GitHub Actions spend: $${scenario.amount.toFixed(2)} this month`, exact: true }).click();
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
  test("marks a selected report old as the existing clock advances", async ({ page }) => {
    await page.clock.install({ time: new Date("2026-11-01T02:30:00Z") });
    await page.route("**/api/actions-spend?*", (route) => route.fulfill({ json: reading({ read_at: "2026-11-01T00:31:00Z", checked_at: "2026-11-01T00:31:00Z" }) }));
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, cfo_runs: true,
      actions_spend: reading({ month: "2026-11", total_usd: 40, read_at: "2026-11-01T02:30:00Z", checked_at: "2026-11-01T02:30:00Z" }) });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await page.getByRole("button", { name: "GitHub Actions spend: $8.50 this month", exact: true }).click();
    await page.clock.runFor(60_000);
    await expect(page.getByRole("button", { name: "GitHub Actions spend: $8.50 this month (old)", exact: true })).toBeVisible();
    await expect(page.getByRole("dialog", { name: "GitHub Actions" })).toContainText("Old reading");
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
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, cfo_runs: true,
      actions_spend: reading({ read_at: "2026-11-01T04:59:30Z", checked_at: "2026-11-01T04:59:30Z" }) });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await page.getByRole("button", { name: "GitHub Actions spend: $8.50 this month", exact: true }).click();
    const panel = page.getByRole("dialog", { name: "GitHub Actions" });
    await expect(panel).toContainText("October 2026");
    await page.clock.runFor(30_000);
    await expect(panel).toContainText("November 2026");
    await expect(panel).toContainText("Spend unavailable");
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
        return route.fulfill({ json: reading({ status: "stale", month: scenario.after, total_usd: 40, read_at: scenario.now, checked_at: checkedAt, detail: "Could not refresh usage." }) });
      });
      await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, cfo_runs: true,
        actions_spend: reading({ month: scenario.before, read_at: scenario.now, checked_at: scenario.now }) });
      await page.goto("/");
      await page.getByRole("button", { name: "Board", exact: true }).click();
      await page.getByRole("button", { name: "GitHub Actions spend: $8.50 this month", exact: true }).click();
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
        return route.fulfill({ json: reading({ month, total_usd: amount, read_at: now, checked_at: now }) });
      });
      await holdStream(page, { instance: "same-board", revision: 1, healthy: true, example: true, cfo_runs: true,
        actions_spend: reading({ month: "2026-11", total_usd: 40, read_at: now, checked_at: now }) });
      await page.goto("/");
      await page.getByRole("button", { name: "Board", exact: true }).click();
      await page.getByRole("button", { name: `GitHub Actions spend: $${amount.toFixed(2)} this month`, exact: true }).click();
      await expect(page.getByRole("dialog", { name: "GitHub Actions" })).toContainText(label);
    }
  } finally {
    for (const context of contexts) await context.close();
  }
});

for (const width of [1440, 390, 320]) {
  test(`the header opens the monthly Actions spend panel at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1100 });
    await page.clock.install({ time: new Date(NOW) });
    const panelRequests: string[] = [];
    page.on("request", (request) => { if (request.url().includes("/actions-spend-panel-")) panelRequests.push(request.url()); });
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, cfo_runs: true, cfo_harness: "codex",
      subscriptions: [{ provider: "codex", status: "available", source: "oauth", percent_remaining: 75, read_at: NOW, resets_at: "2026-10-20T00:00:00Z", floor_percent: 20 }], actions_spend: reading() });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    const button = page.getByRole("button", { name: "GitHub Actions spend: $8.50 this month" });
    await expect(button).toBeVisible();
    await expect(button.locator("svg")).toBeVisible();
    expect(panelRequests).toHaveLength(0);
    await page.evaluate(() => document.fonts.ready);
    await page.getByRole("group", { name: "CFO", exact: true }).screenshot({ path: testInfo.outputPath(`actions-header-${width}.png`) });
    await button.click();
    const panel = page.getByRole("dialog", { name: "GitHub Actions" });
    await expect(panel).toBeVisible();
    expect(panelRequests).toHaveLength(1);
    await expect(panel).toContainText("October 2026");
    await expect(panel).toContainText("after included minutes");
    await expect(panel).toContainText("Read Oct 15, 2026");
    const rows = panel.locator("tbody tr");
    await expect(rows).toHaveText(["example-user/moss$5.00", "example-user/fern$3.50"]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(await panel.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await panel.screenshot({ path: testInfo.outputPath(`actions-panel-${width}.png`) });
    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(button).toBeFocused();
  });
}

for (const [name, changes, label, words] of [
  ["failed first read", { status: "unavailable", total_usd: null, repositories: [], read_at: "", detail: "Check the token's user scope." }, "unavailable", "Check the token's user scope."],
  ["failed later read", { status: "stale", detail: "Could not read usage. Check the token's user scope." }, "$8.50 this month (old)", "Old reading"],
  ["new calendar month", { month: "2026-09" }, "unavailable", "Waiting for this month's GitHub Actions usage."],
  ["included usage only", { total_usd: 0, repositories: [] }, "$0.00 this month", "No repository has billable Actions usage this month."],
] as const) {
  test(`Actions spend shows ${name} without guessing`, async ({ page }, testInfo) => {
    await page.clock.install({ time: new Date(NOW) });
    await page.route("**/api/actions-spend?*", (route) => route.fulfill({ json: reading({ status: "unavailable", total_usd: null, repositories: [], detail: "Waiting for this month's GitHub Actions usage." }) }));
    await holdStream(page, { instance: "actions-fixture", revision: 1, healthy: true, example: true, cfo_runs: true, actions_spend: reading(changes) });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await page.getByRole("button", { name: `GitHub Actions spend: ${label}` }).click();
    const panel = page.getByRole("dialog", { name: "GitHub Actions" });
    await expect(panel).toContainText(words);
    if (label === "unavailable") await expect(panel).not.toContainText("$8.50");
    await panel.screenshot({ path: testInfo.outputPath(`actions-${name.replaceAll(" ", "-")}.png`) });
  });
}
