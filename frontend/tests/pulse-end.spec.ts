import { expect, test, type Page } from "@playwright/test";
import type { EffectReport } from "./fixtures/effect-watch";

const report = (page: Page) => page.evaluate(() => window.effectWatch?.report() ?? null);

// A pulse is over once something that animated has left and nothing moves.
async function pulseEnds(page: Page): Promise<EffectReport> {
  await expect.poll(async () => {
    const seen = await report(page);
    return !!seen && seen.ended > 0 && seen.running === 0;
  }, { timeout: 12_000, intervals: [250] }).toBe(true);
  return (await report(page))!;
}

async function setHidden(page: Page, hidden: boolean) {
  await page.evaluate((hidden) => {
    if (hidden) {
      Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
      Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    } else {
      Reflect.deleteProperty(document, "hidden");
      Reflect.deleteProperty(document, "visibilityState");
    }
    document.dispatchEvent(new Event("visibilitychange"));
  }, hidden);
}

for (const mode of ["report", "message", "created", "compact-message", "compact-created"]) {
  test(`a ${mode} pulse fades out before it leaves, never stale and never cut`, async ({ page }) => {
    await page.goto(`/tests/fixtures/pulse-end.html#${mode}`);
    const elements = await page.locator("#effects *").count();
    expect(elements).toBeGreaterThan(0);

    await page.getByRole("button", { name: "Report" }).click();
    const seen = await pulseEnds(page);

    // The pulse was really shown, animated, and then left the page.
    expect(seen.peak).toBeGreaterThan(.5);
    expect(seen.animated).toBeGreaterThan(0);
    // Every part of it was faded out when it left, and nothing sat visible
    // and still between its animation and its removal.
    expect({ leftVisible: seen.leftVisible, stale: seen.stale }).toEqual({ leftVisible: [], stale: [] });
    // Nothing is left behind and nothing keeps animating while idle.
    expect(seen.elements).toBe(elements);
    expect(seen.running).toBe(0);
  });
}

for (const [mode, collapse, expand] of [["report", "Collapse descendants of", "Expand descendants of"], ["compact-message", "Collapse children of", "Expand children of"]]) {
  test(`a ${mode} pulse drawn late, on a branch expanded mid-pulse, still ends with its life`, async ({ page }) => {
    await page.goto(`/tests/fixtures/pulse-end.html#${mode}`);
    await page.getByRole("button", { name: "Report" }).click();
    await page.getByRole("button", { name: new RegExp(collapse) }).click();
    await page.waitForTimeout(1500);
    await page.getByRole("button", { name: "Watch" }).click();
    await page.getByRole("button", { name: new RegExp(expand) }).click();
    const seen = await pulseEnds(page);

    expect(seen.peak).toBeGreaterThan(.5);
    expect({ leftVisible: seen.leftVisible, stale: seen.stale }).toEqual({ leftVisible: [], stale: [] });
  });
}

test("a newer report plays its own pulse while the earlier one finishes and fades", async ({ page }) => {
  await page.goto("/tests/fixtures/pulse-end.html#report");
  const elements = await page.locator("#effects *").count();

  await page.getByRole("button", { name: "Report" }).click();
  await page.waitForTimeout(1000);
  await page.getByRole("button", { name: "Follow up" }).click();
  const seen = await pulseEnds(page);

  expect(seen.peak).toBeGreaterThan(.5);
  expect({ leftVisible: seen.leftVisible, stale: seen.stale }).toEqual({ leftVisible: [], stale: [] });
  expect(seen.elements).toBe(elements);
  expect(seen.running).toBe(0);
});

test("a report that arrives while the board is hidden never plays later", async ({ page }) => {
  await page.goto("/tests/fixtures/pulse-end.html#report");
  await setHidden(page, true);
  await page.getByRole("button", { name: "Report" }).click();
  await page.waitForTimeout(500);
  await setHidden(page, false);
  await page.waitForTimeout(500);
  const hiddenReport = await report(page);
  expect(hiddenReport?.animated).toBe(0);
  expect(hiddenReport?.running).toBe(0);

  // The next report, seen, still pulses and ends cleanly.
  await page.getByRole("button", { name: "Report" }).click();
  const seen = await pulseEnds(page);
  expect(seen.peak).toBeGreaterThan(.5);
  expect({ leftVisible: seen.leftVisible, stale: seen.stale }).toEqual({ leftVisible: [], stale: [] });
});
