import { expect, holdStream, test } from "./site";

const NOW = "2026-10-15T12:00:00Z";
const reading = (changes: Record<string, unknown> = {}) => ({ status: "available", month: "2026-10", total_usd: 8.5,
  repositories: [{ name: "example-user/moss", spent_usd: 5 }, { name: "example-user/fern", spent_usd: 3.5 }],
  read_at: NOW, checked_at: NOW, detail: "", ...changes });

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
