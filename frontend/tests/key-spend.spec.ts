import { expect, holdStream, test, type Page } from "./site";

// The Overlord's own row on 2026-10-24: $9.04 spent against a $50 week on the
// OpenRouter key Pi runs on, shown where the subscription rings are.
const NOW = "2026-10-15T12:00:00Z";
const RESET = "2026-10-20T00:00:00Z";
test.use({ locale: "en-US", timezoneId: "America/Chicago" });
const memory = { total: 32 * 2 ** 30, available: 7.3 * 2 ** 30, commit_limit: 48 * 2 ** 30, commit_available: 20 * 2 ** 30, floor: 4 * 2 ** 30, next: 5 * 2 ** 30 };
const keySpend = (changes: Record<string, unknown> = {}) => ({
  provider: "openrouter", status: "available", spent_usd: 9.04, limit_usd: 50, remaining_usd: 40.96, period: "weekly",
  resets_at: RESET, read_at: NOW, byok_usd: 0, includes_byok: false, blocking: null, profile: "default", detail: "", ...changes,
});

// open gives the page a Pi home with one OpenRouter key reading and opens the
// board, which holds the CFO's bar and the canvas.
async function open(page: Page, spend: unknown = keySpend(), tasks: unknown[] = []) {
  await page.clock.install({ time: new Date(NOW) });
  const keySpendList = Array.isArray(spend) ? spend : [spend];
  await holdStream(page, { instance: "key-spend-fixture", revision: 1, healthy: true, example: true, cfo_runs: true, cfo_harness: "pi", memory, subscriptions: [], key_spend: keySpendList, tasks });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await expect(page.getByRole("region", { name: "Tasks", exact: true }).getByRole("group", { name: "Memory" })).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  return page.getByRole("group", { name: "CFO", exact: true });
}

// Dollars are the dial's value, with the limit and the window they count
// against beside them, in the same surface as the subscription rings, at a
// person's widths and in both colour schemes.
for (const width of [1440, 390, 320]) {
  for (const colorScheme of ["dark", "light"] as const) {
    test(`the OpenRouter key's spend reads on the CFO's bar at ${width}px with ${colorScheme} preference`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1100 });
      await page.emulateMedia({ colorScheme });
      const bar = await open(page);
      const dial = bar.getByRole("progressbar", { name: "OpenRouter $9.04 of $50 this week" });
      await expect(dial).toBeVisible();
      await expect(dial.locator(".key-spend-spent")).toHaveText("$9.04");
      await expect(dial.locator(".key-spend-budget")).toHaveText("/ $50 this week");
      await expect(dial.locator(".subscription-fill")).toHaveAttribute("stroke-dasharray", "81.92 100");
      const layout = await bar.evaluate((element) => ({
        overflows: element.scrollWidth > element.clientWidth,
        dollars: parseFloat(getComputedStyle(element.querySelector<HTMLElement>(".key-spend-spent")!).fontSize),
        budget: parseFloat(getComputedStyle(element.querySelector<HTMLElement>(".key-spend-budget")!).fontSize),
        ring: element.querySelector(".key-spend-dial .subscription-ring")!.getBoundingClientRect().width,
      }));
      expect(layout.overflows).toBe(false);
      expect(layout.dollars).toBeGreaterThanOrEqual(16);
      expect(layout.budget).toBeGreaterThanOrEqual(13);
      expect(layout.ring).toBeGreaterThanOrEqual(44);
      await expect(bar.locator(".key-spend-dial")).toHaveCount(1);
      await expect(page.locator("body")).not.toContainText(/sk-or|Bearer|OPENROUTER_API_KEY/);
      await dial.hover();
      const tip = page.getByRole("tooltip");
      await expect(tip).toContainText("Spent $9.04 of $50 this week.");
      await expect(tip).toContainText("Resets Oct 19, 2026, 7:00 PM CDT");
      await expect(tip).toContainText("Reading just now");
      await expect(tip).toContainText("Pi's profile: default.");
      await expect(tip).not.toContainText(/sk-or|OpenAI|weekly remaining/);
      const box = (await tip.boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(width);
      await page.mouse.move(0, 0);
      await expect(dial).toHaveAccessibleDescription(/Spent \$9\.04 of \$50 this week\. Resets.*Reading just now/);
      await bar.screenshot({ path: testInfo.outputPath(`key-spend-${width}-${colorScheme}.png`) });
    });
  }
}

// The canvas carries the same dial on the CFO's card, where a phone shows the
// canvas as a reported workflow list. The card is drawn once a goblin hangs
// under the CFO.
const liveGoblin = { id: "cg-pi-key-usage", title: "Show the OpenRouter key's spend", project: "code-goblins", phase: "working", verified: false, generation: "g1", since: NOW, harness: "pi", backend: "native" };
for (const width of [1440, 390]) {
  test(`the CFO's card on the canvas carries the key's spend at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1100 });
    await open(page, keySpend(), [liveGoblin]);
    await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    const card = page.locator(".flow-node, .workflow-node").filter({ has: page.locator(".key-spend-dial") });
    await expect(card).toHaveCount(1);
    const dial = card.locator(".key-spend-dial");
    await expect(dial.locator(".key-spend-spent")).toHaveText("$9.04");
    await expect(dial.locator(".key-spend-budget")).toHaveText("/ $50 this week");
    await expect(dial).toHaveAccessibleDescription(/Spent \$9\.04 of \$50 this week\./);
    // The canvas card's foot is one row of 28px marks, so the dollars and the
    // budget they count against sit on one line there, and the card holds all
    // of it inside its border at either width.
    const layout = await card.evaluate((element) => {
      const foot = element.querySelector<HTMLElement>(".flow-node-foot");
      return {
        overflows: element.scrollWidth > element.clientWidth + 1,
        footHeight: foot?.offsetHeight ?? 0,
        values: getComputedStyle(element.querySelector<HTMLElement>(".key-spend-values")!).flexDirection,
      };
    });
    expect(layout.overflows).toBe(false);
    // The canvas foot is one row of 28px marks, so its dollars and budget sit
    // on one line; the reported workflow list has the room for two.
    if (width === 1440) {
      expect(layout.values).toBe("row");
      expect(layout.footHeight).toBeLessThanOrEqual(28);
    } else {
      expect(layout.values).toBe("column");
    }
    await card.screenshot({ path: testInfo.outputPath(`key-spend-canvas-${width}.png`) });
  });
}

// A window that is not a week, and a limit that is not $50: the dial says the
// provider's window, never the one a fresh install assumes.
for (const [period, limit, words] of [["daily", 10, "$10 today"], ["monthly", 100, "$100 this month"], ["lifetime", 100, "$100 in total"]] as const) {
  test(`the dial names the ${period} window the key's own limit resets over`, async ({ page }) => {
    const bar = await open(page, keySpend({ period, limit_usd: limit, remaining_usd: limit - 9.04 }));
    const dial = bar.getByRole("progressbar", { name: `OpenRouter $9.04 of ${words}` });
    await expect(dial).toBeVisible();
    await expect(dial.locator(".key-spend-budget")).toHaveText(`/ ${words}`);
  });
}

// A key with no limit has no percentage and no ring fraction: the dial shows
// what it spent and says there is no limit, rather than inventing a budget.
test("a key with no limit shows its dollars and no percentage", async ({ page }) => {
  const bar = await open(page, keySpend({ period: "lifetime", limit_usd: null, remaining_usd: null, spent_usd: 25.5 }));
  const dial = bar.getByRole("img", { name: "OpenRouter $25.50 spent, no limit" });
  await expect(dial).toBeVisible();
  await expect(dial.locator(".key-spend-budget")).toHaveText("no limit");
  await expect(dial.locator(".subscription-fill")).toHaveCount(0);
  await dial.hover();
  await expect(page.getByRole("tooltip")).toContainText("Spent $25.50 in total, no limit.");
  await expect(page.getByRole("tooltip")).not.toContainText("%");
});

// Every way the reading can be absent says so in its own words, and none of
// them reads as $0 or as an OpenAI subscription.
for (const [status, name] of [["rate_limited", "OpenRouter spend rate limited"], ["missing", "OpenRouter spend key missing"], ["auth_required", "OpenRouter spend key refused"], ["unavailable", "OpenRouter spend unavailable"], ["stale", "OpenRouter spend stale"], ["unattributed", "OpenRouter spend not attributed to this session"]] as const) {
  test(`a ${status} reading is named on the dial with its last dollars beside their age`, async ({ page }) => {
    const bar = await open(page, keySpend({ status, detail: status === "missing" ? "Pi has no OpenRouter key on this machine" : "" }));
    const dial = bar.getByRole("img", { name });
    await expect(dial).toBeVisible();
    await expect(dial.locator(".key-spend-spent")).toHaveText("?");
    await expect(dial.locator(".subscription-fill")).toHaveCount(0);
    await dial.hover();
    const tip = page.getByRole("tooltip");
    await expect(tip).toContainText("Last read $9.04 of $50 this week.");
    await expect(tip).toContainText(/Reading just now/);
    await expect(tip).not.toContainText("$0");
    await expect(tip).not.toContainText(/OpenAI/);
    if (status === "unattributed") await expect(tip).toContainText(/not attributed to this session/);
  });
}

// Two live credentials are two dials whose dollars are never merged, each
// told apart by its own Pi profile in the accessible name.
test("two distinct credentials keep their own dollars and their own names", async ({ page }) => {
  const bar = await open(page, [keySpend({ key_id: "one", profile: "default" }), keySpend({ key_id: "two", profile: "work", spent_usd: 4.5, limit_usd: 20, remaining_usd: 15.5 })]);
  const first = bar.getByRole("progressbar", { name: "OpenRouter $9.04 of $50 this week (profile default)" });
  const second = bar.getByRole("progressbar", { name: "OpenRouter $4.50 of $20 this week (profile work)" });
  await expect(first).toBeVisible();
  await expect(second).toBeVisible();
  await expect(bar.locator(".key-spend-dial")).toHaveCount(2);
  await first.hover();
  await expect(page.getByRole("tooltip")).toContainText("Spent $9.04 of $50 this week.");
  await expect(page.getByRole("tooltip")).not.toContainText("$4.50");
});


// A member budget that stops requests before the key's own is named as that
// budget, with its own spend, and BYOK spend beside the key's dollars.
test("the dial says what blocks first and keeps BYOK spend apart", async ({ page }) => {
  const bar = await open(page, keySpend({ byok_usd: 1.2, blocking: { scope: "member", spent_usd: 32.5, limit_usd: 50, period: "weekly" } }));
  const dial = bar.getByRole("progressbar", { name: "OpenRouter $9.04 of $50 this week" });
  await dial.hover();
  const tip = page.getByRole("tooltip");
  await expect(tip).toContainText("BYOK spend $1.20 in the same window, counted separately.");
  await expect(tip).toContainText("Requests stop at the member budget first: $32.50 of $50 this week.");
});
