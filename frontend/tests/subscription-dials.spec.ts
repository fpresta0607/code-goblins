import { expect, holdStream, test, type Page } from "./site";

const NOW = "2026-10-03T13:07:00Z";
const RESET = "Oct 9, 2026, 5:27 PM CDT";
test.use({ locale: "en-US", timezoneId: "America/Chicago" });
const memory = { total: 32 * 2 ** 30, available: 7.3 * 2 ** 30, commit_limit: 48 * 2 ** 30, commit_available: 20 * 2 ** 30, floor: 4 * 2 ** 30, next: 5 * 2 ** 30 };
const usage = (provider: string, percent: number | null, status = "available", floor = 5) => ({ provider, status, percent_remaining: percent, read_at: NOW, resets_at: "2026-10-09T22:27:42Z", source: "oauth", floor_percent: floor });

// The dials sit on the CEO's bar and nowhere else: the Overlord, 2026-10-07,
// "you should put calude usage at cfo header only..". open returns the bar.
async function open(page: Page, subscriptions = [usage("claude", null, "auth_required"), usage("codex", 76)]) {
  await page.clock.install({ time: new Date(NOW) });
  await holdStream(page, { instance: "usage-fixture", revision: 1, healthy: true, example: true, ceo_runs: true, ceo_harness: "claude", memory, subscriptions, tasks: [] });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await expect(page.getByRole("region", { name: "Tasks", exact: true }).getByRole("group", { name: "Memory" })).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  return page.getByRole("group", { name: "CEO" });
}

for (const width of [1440, 390, 320]) {
  for (const colorScheme of ["dark", "light"] as const) {
    test(`weekly dials stay readable on the CEO's bar, and only there, at ${width}px with ${colorScheme} preference`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1100 });
      await page.emulateMedia({ colorScheme });
      const bar = await open(page);
      const claude = bar.getByRole("img", { name: "Claude weekly remaining unavailable" });
      const openai = bar.getByRole("progressbar", { name: "OpenAI 76% weekly remaining" });
      await expect(claude).toBeVisible();
      await expect(openai).toBeVisible();
      await expect(claude.locator(".subscription-percent")).toHaveText("?");
      await expect(openai.locator(".subscription-percent")).toHaveText("76%");
      const layout = await bar.evaluate((element) => ({
        overflows: element.scrollWidth > element.clientWidth,
        smallest: Math.min(...[...element.querySelectorAll<HTMLElement>(".subscription-percent")].map((text) => parseFloat(getComputedStyle(text).fontSize))),
        ring: element.querySelector(".subscription-ring")!.getBoundingClientRect().width,
      }));
      expect(layout.overflows).toBe(false);
      expect(layout.smallest).toBeGreaterThanOrEqual(16);
      expect(layout.ring).toBeGreaterThanOrEqual(44);
      expect(await marks(bar)).toEqual([{ path: CLAUDE_PATH, isInside: true, words: [] }, { path: OPENAI_PATH, isInside: true, words: [] }]);
      await expect(page.locator(".subscription-dial")).toHaveCount(2);
      await expect(page.getByRole("region", { name: "Tasks", exact: true }).locator(".subscription-dial")).toHaveCount(0);
      await openai.hover();
      await expect(page.getByRole("tooltip")).toContainText("5% reserve");
      await expect(page.getByRole("tooltip")).toContainText(RESET);
      await expect(page.getByRole("tooltip")).not.toContainText(/quota-axi|OAuth/);
      const tip = (await page.getByRole("tooltip").boundingBox())!;
      expect(tip.x).toBeGreaterThanOrEqual(0);
      expect(tip.x + tip.width).toBeLessThanOrEqual(width);
      await page.mouse.move(0, 0);
      await openai.focus();
      await page.keyboard.press("Tab");
      await page.keyboard.press("Shift+Tab");
      await expect(openai).toBeFocused();
      await expect(page.getByRole("tooltip")).toContainText("just now");
      await expect(openai).toHaveAccessibleDescription(/Resets.*Reading.*5% reserve/);
      await page.keyboard.press("Shift+Tab");
      await expect(claude).toBeFocused();
      await expect(claude).toHaveAccessibleName("Claude weekly remaining unavailable");
      await bar.screenshot({ path: testInfo.outputPath(`dials-${width}-${colorScheme}.png`) });
    });
  }
}

// The ring carries no tick for the 5% reserve: the Overlord, 2026-10-07, "why
// tick here with 75% remaining ... that doesnt make sense". It turns the
// warning colour at 10% and under, and its tip names the reserve.
for (const percent of [0, 5, 10, 10.1, 75, 100]) {
  test(`${percent}% remaining draws the correct ring, with no reserve tick, warning only near the reserve`, async ({ page }) => {
    const bar = await open(page, [usage("codex", percent)]);
    const dial = bar.getByRole("progressbar", { name: `OpenAI ${percent}% weekly remaining` });
    await expect(dial.locator(".subscription-fill")).toHaveAttribute("stroke-dasharray", `${percent} 100`);
    await expect(dial.locator(".subscription-ring > :not(circle, svg)")).toHaveCount(0);
    if (percent <= 10) await expect(dial).toHaveClass(/reserve/);
    else await expect(dial).not.toHaveClass(/reserve/);
    await expect(dial).toHaveAccessibleDescription(/5% reserve/);
  });
}

// The Overlord, 2026-10-09: "keep using claude until at 0". Under a floor of
// 0 the tip names no reserve, and the ring warns only in the last five points.
for (const percent of [1, 5.1]) {
  test(`under a floor of 0, ${percent}% remaining names no reserve and warns only in the last five points`, async ({ page }) => {
    const bar = await open(page, [usage("claude", percent, "available", 0)]);
    const dial = bar.getByRole("progressbar", { name: `Claude ${percent}% weekly remaining` });
    if (percent <= 5) await expect(dial).toHaveClass(/reserve/);
    else await expect(dial).not.toHaveClass(/reserve/);
    await expect(dial).toHaveAccessibleDescription(/No reserve, runs to 0%/);
  });
}

// The Overlord, 2026-11-02, on the bar's "Claude 100%" and "Codex 4%": each
// provider's icon sits inside its ring in place of its name beside it. The
// bar still wears no mark of the harness the CEO runs (goblin-alerts.spec.ts).
// marks reads each dial's ring: the brand path drawn in it, whether that
// mark lies wholly inside the ring, and any words beside the ring other than
// its percentage.
async function marks(bar: ReturnType<Page["getByRole"]>) {
  return bar.locator(".subscription-dial").evaluateAll((dials) => dials.map((dial) => {
    const ring = dial.querySelector(".subscription-ring")!.getBoundingClientRect();
    const mark = dial.querySelector<SVGSVGElement>(".subscription-ring svg");
    const box = mark?.getBoundingClientRect();
    const middle = box && { x: box.left + box.width / 2 - (ring.left + ring.width / 2), y: box.top + box.height / 2 - (ring.top + ring.height / 2) };
    return {
      path: mark?.querySelector("path")?.getAttribute("d")?.slice(0, 12) ?? "",
      isInside: !!box && !!middle && Math.hypot(box.width / 2, box.height / 2) + Math.hypot(middle.x, middle.y) <= ring.width / 2,
      words: [...dial.querySelectorAll<HTMLElement>(":scope > :not(.subscription-ring, .subscription-percent)")].map((element) => element.textContent),
    };
  }));
}
const CLAUDE_PATH = "m4.7144 15.9", OPENAI_PATH = "M22.2819 9.8";

test("the CEO's bar draws each subscription's icon inside its ring, with no name beside it", async ({ page }) => {
  // Arrange and Act
  const bar = await open(page, [usage("claude", 40), usage("codex", 76)]);

  // Assert
  const claude = bar.getByRole("progressbar", { name: "Claude 40% weekly remaining" });
  const openai = bar.getByRole("progressbar", { name: "OpenAI 76% weekly remaining" });
  await expect(claude).toHaveText("40%");
  await expect(openai).toHaveText("76%");
  await expect(claude.locator(".subscription-fill")).toHaveAttribute("stroke-dasharray", "40 100");
  await expect(openai.locator(".subscription-fill")).toHaveAttribute("stroke-dasharray", "76 100");
  expect(await marks(bar)).toEqual([{ path: CLAUDE_PATH, isInside: true, words: [] }, { path: OPENAI_PATH, isInside: true, words: [] }]);
  await expect(bar.locator(".mark")).toHaveCount(0);
});

test("one subscription's ring carries its icon too", async ({ page }) => {
  // Arrange and Act
  const bar = await open(page, [usage("codex", 76)]);

  // Assert
  await expect(bar.getByRole("progressbar", { name: "OpenAI 76% weekly remaining" })).toHaveText("76%");
  expect(await marks(bar)).toEqual([{ path: OPENAI_PATH, isInside: true, words: [] }]);
});

// A provider with no reading yet, one that needs a sign-in and one whose
// reading went stale each keep their icon in a ring with no fill and a "?".
for (const status of ["unavailable", "auth_required", "stale"]) {
  test(`a ${status} reading keeps its provider's icon in an unknown ring`, async ({ page }) => {
    // Arrange and Act
    const bar = await open(page, [usage("claude", status === "stale" ? 40 : null, status), usage("codex", 76)]);

    // Assert
    const claude = bar.getByRole("img", { name: "Claude weekly remaining unavailable" });
    await expect(claude).toHaveText("?");
    await expect(claude).toHaveClass(/unknown/);
    await expect(claude.locator(".subscription-fill")).toHaveCount(0);
    expect(await marks(bar)).toEqual([{ path: CLAUDE_PATH, isInside: true, words: [] }, { path: OPENAI_PATH, isInside: true, words: [] }]);
  });
}

test("no active providers means no dials on the CEO's bar", async ({ page }) => {
  const bar = await open(page, []);
  await expect(bar.locator(".subscription-dial")).toHaveCount(0);
});

test("an active stale provider stays visible with an unknown ring", async ({ page }) => {
  const bar = await open(page, [usage("codex", 76, "stale")]);
  const dial = bar.getByRole("img", { name: "OpenAI weekly remaining unavailable" });
  await expect(dial.locator(".subscription-percent")).toHaveText("?");
  await expect(dial.locator(".subscription-fill")).toHaveCount(0);
  await dial.hover();
  await expect(page.getByRole("tooltip")).toContainText("stale");
});

test("a measured dial exposes weekly remaining as accessible progress", async ({ page }) => {
  const bar = await open(page, [usage("codex", 76)]);
  const dial = bar.getByRole("progressbar", { name: "OpenAI 76% weekly remaining" });
  await expect(dial).toHaveAttribute("aria-valuemin", "0");
  await expect(dial).toHaveAttribute("aria-valuemax", "100");
  await expect(dial).toHaveAttribute("aria-valuenow", "76");
});

test.describe("touch details", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 1100 } });

  test("holding a finger on a dial shows its reset and freshness", async ({ page }) => {
    const bar = await open(page, [usage("codex", 76)]);
    const dial = bar.getByRole("progressbar", { name: "OpenAI 76% weekly remaining" });
    const box = (await dial.boundingBox())!;
    const touch = await page.context().newCDPSession(page);
    await touch.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: box.x + box.width / 2, y: box.y + box.height / 2 }] });
    await expect(page.getByRole("tooltip")).toContainText(RESET);
    await expect(page.getByRole("tooltip")).toContainText("just now");
    await touch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    await expect(page.getByRole("tooltip")).not.toBeVisible();
    await touch.detach();
  });
});

test.describe("localized reset time", () => {
  test.use({ locale: "de-DE", timezoneId: "Europe/Berlin" });

  test("the reset uses the viewer's locale and timezone across a date boundary", async ({ page }) => {
    const bar = await open(page, [usage("codex", 76)]);
    await bar.getByRole("progressbar", { name: "OpenAI 76% weekly remaining" }).hover();
    await expect(page.getByRole("tooltip")).toContainText("10. Okt. 2026, 00:27 MESZ");
    await expect(page.getByRole("tooltip")).not.toContainText(/quota-axi|OAuth/);
  });
});
