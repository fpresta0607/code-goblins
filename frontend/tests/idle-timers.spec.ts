import { expect, holdStream, type Page, test } from "./site";

declare global { interface Window { intervals: number[]; goToTray: () => void } }

const GB = 2 ** 30;
const snapshot = {
  instance: "idle-timers-fixture", revision: 1, healthy: true, cfo_runs: true, attention: [], tasks: [],
  memory: { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, available: 9.4 * GB, commit_available: 20 * GB },
};

// recordIntervals keeps the delay of every interval timer the page starts.
async function recordIntervals(page: Page) {
  await page.addInitScript(() => {
    window.intervals = [];
    const start = window.setInterval.bind(window);
    window.setInterval = ((handler: TimerHandler, timeout?: number, ...rest: unknown[]) => {
      window.intervals.push(timeout ?? 0);
      return start(handler, timeout, ...rest);
    }) as typeof window.setInterval;
  });
}

// What an event can announce is not polled for (#1598): a board that is
// connected and has nothing open but the CFO starts no interval timer, save a
// one-second clock.
test("a connected, idle board starts no interval timer but a one-second clock", async ({ page }) => {
  // Arrange
  await recordIntervals(page);
  await holdStream(page, snapshot);

  // Act
  await page.goto("/");
  await expect(page.getByRole("region", { name: "Tasks", exact: true }).getByRole("group", { name: "Memory" })).toBeVisible();
  await expect(page.locator(".topbar").getByText("Live", { exact: true })).toBeVisible();

  // Assert
  expect(await page.evaluate(() => window.intervals.filter((delay) => delay !== 1000))).toEqual([]);
});

// The desktop window moves onto the program an update installed once it is
// in the tray, which the page hears from its visibility and its connection,
// with no timer to look again.
for (const { when, isInTrayFirst } of [{ when: "goes to the tray", isInTrayFirst: false }, { when: "is in the tray as the board connects", isInTrayFirst: true }]) {
  test(`the desktop window that ${when} asks at once to move onto the update's program`, async ({ page }) => {
    // Arrange: WebView2 puts chrome.webview in the desktop window's page, and
    // the window in the tray is a page that is hidden.
    await page.addInitScript((isHidden) => {
      (window as unknown as { chrome: Record<string, unknown> }).chrome = { ...(window as unknown as { chrome?: Record<string, unknown> }).chrome, webview: {} };
      let hidden = isHidden;
      Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
      window.goToTray = () => { hidden = true; document.dispatchEvent(new Event("visibilitychange")); };
    }, isInTrayFirst);
    const moves: unknown[] = [];
    await page.route("**/api/window/move", (route) => { moves.push(route.request().postDataJSON()); return route.fulfill({ json: { moved: false } }); });
    await recordIntervals(page);
    await holdStream(page, snapshot);
    await page.goto("/");
    await expect(page.getByRole("region", { name: "Tasks", exact: true }).getByRole("group", { name: "Memory" })).toBeAttached();

    // Act
    if (!isInTrayFirst) {
      expect(moves).toEqual([]);
      await page.evaluate(() => window.goToTray());
    }

    // Assert
    await expect.poll(() => moves).toEqual([{ hidden: true }]);
    expect(await page.evaluate(() => window.intervals.filter((delay) => delay !== 1000))).toEqual([]);
  });
}
