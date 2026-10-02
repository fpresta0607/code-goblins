import { expect, test, type Locator, type Page } from "./site";

// The board the Overlord looked at on 2026-10-02: 19 queued tasks, 11 goblins
// in progress and 20 completed, here with 12 paused under In progress. In
// progress shows every goblin at once and never pages; Tasks, Paused and
// Completed page, and a page of Tasks is a real portion of the list. A card can
// be dragged to any place in its list: the board scrolls under a card held at
// its edge, and a card held over a page arrow turns the page.
const since = "2026-10-02T09:00:00Z";
const memory = { next: 5368709120, floor: 4294967296, total: 34359738368, available: 6442450944, commit_limit: 51539607552, commit_available: 21474836480, paged_pool: 536870912, nonpaged_pool: 322122547 };
const LONG = "Paused goblins resume by themselves when the reason for the pause clears, and the board says which goblin each one was waiting on and for how long it has waited";
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const numbered = (count: number, make: (number: number) => Record<string, unknown>) => Array.from({ length: count }, (_, index) => make(index + 1));
const QUEUED = numbered(19, (number) => task("queued-" + number, "queued", { generation: "", brief: true, queue_revision: "q1", ...(number === 1 ? { title: LONG } : {}) }));
const WORKING = numbered(11, (number) => task("working-" + number, "working", { harness: "codex", pr: "https://github.com/example/code-goblins/pull/" + (200 + number) }));
const PAUSED = numbered(12, (number) => task("paused-" + number, "paused", { at: since }));
const DONE = numbered(20, (number) => task("finished:done-" + number, "done", { archived: true, merged: true, verified: true, generation: "", branch: "fix/done-" + number, at: since }));
const ids = (tasks: Record<string, unknown>[]) => tasks.map((item) => item.id as string);

interface Posted { path: string; body: Record<string, unknown> }

// posted collects what the board asked the supervisor to change.
async function open(page: Page, posted: Posted[] = []) {
  const snapshot = { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], memory, tasks: [...QUEUED, ...WORKING, ...PAUSED, ...DONE] };
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") {
      posted.push({ path, body: route.request().postDataJSON() });
      await route.fulfill({ json: { revision: 2 } });
    } else if (path === "/api/events") await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const column = (page: Page, name: string) => page.getByRole("region", { name, exact: true });
const ranked = (list: Locator) => list.locator("[data-sort-id]").evaluateAll((cards) => cards.map((card) => card.getAttribute("data-sort-id")));
// The cards a pager says its page shows, such as 6 and 10 for 6–10 of 19.
const paged = async (pager: Locator) => { const [, first, last] = (await pager.innerText()).match(/(\d+)–(\d+) of \d+/)!; return { first: Number(first), last: Number(last) }; };

// Presses the mouse on a card's title and starts dragging it.
async function grab(page: Page, card: Locator) {
  const title = card.locator(".card-title");
  await title.scrollIntoViewIfNeeded();
  const box = (await title.boundingBox())!;
  const at = { x: box.x + 24, y: box.y + 12 };
  await page.mouse.move(at.x, at.y);
  await page.mouse.down();
  await page.mouse.move(at.x, at.y + 12, { steps: 3 });
  await expect(card).toHaveClass(/dragging/);
  return at;
}

// The part of the screen the board scrolls in: its canvas beside the panel,
// or the whole window once the canvas grows with the board.
const view = (page: Page) => page.locator(".canvas-region").evaluate((canvas) => {
  const box = canvas.getBoundingClientRect(), scrolls = canvas.scrollHeight > canvas.clientHeight;
  return { top: scrolls ? box.top : 0, bottom: scrolls ? box.bottom : innerHeight };
});

for (const [layout, width] of [["side by side", 2400], ["stacked", 1000]] as const) {
  test.describe(`with the columns ${layout}, on a 900 px high window`, () => {
    test.use({ viewport: { width, height: 900 } });

    test("In progress shows every goblin's card at once, with no pager", async ({ page }) => {
      await open(page);
      const progress = column(page, "In progress");
      expect(await ranked(progress)).toEqual(ids(WORKING));
      await expect(progress.locator(":scope > .pager")).toHaveCount(0);
    });

    test("a page of Tasks holds at least five cards, and Tasks, Paused and Completed keep their pagers", async ({ page }) => {
      await open(page);
      const tasks = column(page, "Tasks");
      const shown = await paged(tasks.locator(".pager"));
      expect(shown.first).toBe(1);
      expect(shown.last).toBeGreaterThanOrEqual(5);
      expect(await ranked(tasks)).toEqual(ids(QUEUED).slice(0, shown.last));
      await expect(column(page, "Paused").locator(".pager")).toContainText("of 12");
      await expect(column(page, "Completed").locator(".pager")).toContainText("of 20");
    });

    test("the sixth in-progress card can be dragged to the first place, the board scrolling under it", async ({ page }) => {
      const posted: Posted[] = [];
      await open(page, posted);
      const progress = column(page, "In progress");
      const at = await grab(page, progress.locator("[data-sort-id='working-6']"));
      // Held at the board's top edge, the card waits while the board scrolls
      // up under it to the start of its list.
      await page.mouse.move(at.x, (await view(page)).top + 10, { steps: 6 });
      await expect.poll(async () => (await ranked(progress))[0], { timeout: 15_000 }).toBe("working-6");
      await page.mouse.up();
      const order = ["working-6", ...ids(WORKING).filter((id) => id !== "working-6")];
      await expect.poll(() => posted).toEqual([{ path: "/api/order", body: { list: "progress", order } }]);
      expect(await ranked(progress)).toEqual(order);
      await expect(progress.locator(".order-error")).toHaveCount(0);
    });

    test("a queued card held over the next-page arrow turns the page, and a drop there saves its new place", async ({ page }) => {
      const posted: Posted[] = [];
      await open(page, posted);
      const tasks = column(page, "Tasks"), pager = tasks.locator(".pager"), next = tasks.getByRole("button", { name: "Next cards", exact: true });
      const at = await grab(page, tasks.locator("[data-sort-id='queued-2']"));
      // The arrow is under the list, so the card is held at the board's bottom
      // edge until the board has scrolled the arrow well into view.
      const { bottom } = await view(page);
      await page.mouse.move(at.x, bottom - 10, { steps: 6 });
      await expect.poll(async () => { const box = (await next.boundingBox())!; return box.y + box.height < bottom - 160; }, { intervals: [20], timeout: 15_000 }).toBe(true);
      // Clear of the edge the board stops scrolling, and the arrow stays put.
      await page.mouse.move(at.x, bottom - 140);
      await expect.poll(async () => { const before = (await next.boundingBox())!.y; await page.evaluate(() => new Promise(requestAnimationFrame)); return (await next.boundingBox())!.y === before; }).toBe(true);
      const arrow = (await next.boundingBox())!;
      await page.mouse.move(arrow.x + arrow.width / 2, arrow.y + arrow.height / 2, { steps: 4 });
      await expect.poll(async () => (await paged(pager)).first, { timeout: 15_000 }).toBeGreaterThan(1);
      await page.mouse.up();
      // The card took the last place of the page it was dropped on.
      const shown = await paged(pager);
      const rest = ids(QUEUED).filter((id) => id !== "queued-2");
      const order = [...rest.slice(0, shown.last - 1), "queued-2", ...rest.slice(shown.last - 1)];
      await expect.poll(() => posted).toEqual([{ path: "/api/order", body: { list: "queued", order } }]);
      expect(await ranked(tasks)).toEqual(order.slice(shown.first - 1, shown.last));
      expect(await paged(pager)).toEqual(shown);
    });
  });
}

// A drag scrolls the board only while its card is held at an edge: once it
// ends, nothing keeps asking for frames. The board itself asks for one when a
// snapshot arrives, so a few in half a second is still; a loop asks for
// thirty.
test("a board of 30 goblins in progress shows them all and runs no animation once a drag ends", async ({ page }) => {
  await page.setViewportSize({ width: 2400, height: 900 });
  const posted: Posted[] = [];
  const working = numbered(30, (number) => task("working-" + number, "working", { harness: "codex" }));
  const snapshot = { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], memory, tasks: working };
  await page.addInitScript(() => {
    const frame = window.requestAnimationFrame.bind(window);
    window.framesAsked = 0;
    window.requestAnimationFrame = (callback) => { window.framesAsked!++; return frame(callback); };
  });
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") {
      posted.push({ path, body: route.request().postDataJSON() });
      await route.fulfill({ json: { revision: 2 } });
    } else if (path === "/api/events") await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  const progress = column(page, "In progress");
  await expect(progress.locator("[data-sort-id]")).toHaveCount(30);
  await expect(progress.locator(".pager")).toHaveCount(0);
  const at = await grab(page, progress.locator("[data-sort-id='working-30']"));
  await page.mouse.move(at.x, (await view(page)).top + 10, { steps: 6 });
  await expect.poll(async () => (await ranked(progress))[0], { timeout: 30_000 }).toBe("working-30");
  await page.mouse.up();
  await expect.poll(() => posted.length).toBe(1);
  // The drop's own slide ends within a quarter of a second.
  await expect.poll(() => page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running").length)).toBe(0);
  const asked = (await page.evaluate(() => window.framesAsked))!;
  await page.waitForTimeout(500);
  expect((await page.evaluate(() => window.framesAsked))! - asked).toBeLessThan(5);
});

declare global {
  interface Window { framesAsked?: number }
}
