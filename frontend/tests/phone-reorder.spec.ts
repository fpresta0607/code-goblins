import { expect, holdStream, test, type Locator, type Page } from "./site";

// The Overlord on 2026-11-11, asked to drag a goblin to the top of In progress
// from his phone: "Can't click and drag on mobile" (#2390). A finger dragged a
// card only from its small number, and anywhere else on the card it scrolled
// the page. On a phone a finger held still on a card lifts it, as a mouse
// picks a card up anywhere on it, and a finger that moves at once scrolls.
const since = "2026-11-11T18:00:00Z";
const task = (id: string, phase: string, fields: Record<string, unknown>) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const numbered = (count: number, name: string, phase: string, fields: Record<string, unknown>) => Array.from({ length: count }, (_, index) => task(name + "-" + (index + 1), phase, fields));
const QUEUED = { generation: "", brief: true, queue_revision: "q1" };
const LISTS = [
  { region: "In progress", list: "progress", tasks: numbered(4, "working", "working", { harness: "codex" }) },
  { region: "Tasks", list: "queued", tasks: numbered(4, "queued", "queued", QUEUED) },
];
// Longer than a finger rests on a card before the card lifts.
const PAST_LIFT = 600;
// Longer than the browser takes to make a click of a finger's tap.
const PAST_TAP = 600;

test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });

interface Posted { path: string; body: Record<string, unknown> }

// Opens the board on a phone, by default with four queued tasks and four
// goblins in progress, on a stream that stays open, and shows the list named
// region with room above it. posted collects what the board asked the
// supervisor to change.
async function open(page: Page, region: string, tasks = LISTS.flatMap((each) => each.tasks)) {
  const posted: Posted[] = [];
  await page.addInitScript(() => localStorage.setItem("ceo-first-open", "shown"));
  await holdStream(page, { healthy: true, instance: "fixture", ceo_runs: true, revision: 1, attention: [], tasks });
  await page.route("**/api/**", async (route) => {
    if (route.request().method() !== "POST") return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    posted.push({ path: new URL(route.request().url()).pathname, body: route.request().postDataJSON() });
    await route.fulfill({ json: { revision: 2 } });
  });
  await page.goto("/");
  const column = page.getByRole("region", { name: region, exact: true });
  await expect(column.locator("[data-sort-id]").first()).toBeVisible();
  await column.locator(".task-cards.ranked").first().evaluate((list) => window.scrollBy(0, list.getBoundingClientRect().top - 150));
  // In progress draws a card only once it comes near the view, a moment after
  // the scroll brings it, and moves the cards under it then, so the list is
  // read once every card in it is drawn.
  await expect.poll(() => column.locator("[data-sort-id] .card-title").evaluateAll((titles) => titles.every((title) => title.checkVisibility({ contentVisibilityAuto: true })))).toBe(true);
  return { column, posted };
}

const card = (list: Locator, id: string) => list.locator(`[data-sort-id='${id}']`);
const ranked = (list: Locator) => list.locator("[data-sort-id]").evaluateAll((cards) => cards.map((item) => item.getAttribute("data-sort-id")));
const centre = async (part: Locator) => { const box = (await part.boundingBox())!; return { x: box.x + box.width / 2, y: box.y + box.height / 2 }; };
// The card a finger at a point comes down on.
const cardAt = (page: Page, at: { x: number; y: number }) => page.evaluate(({ x, y }) => document.elementFromPoint(x, y)?.closest("[data-sort-id]")?.getAttribute("data-sort-id"), at);

// A finger driven through the browser's own touch input, so the page scrolls
// under it as a phone's would.
async function fingerOn(page: Page) {
  const touch = await page.context().newCDPSession(page);
  return {
    down: (at: { x: number; y: number }) => touch.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [at] }),
    move: (at: { x: number; y: number }) => touch.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [at] }),
    up: () => touch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] }),
    detach: () => touch.detach(),
  };
}

for (const { region, list, tasks } of LISTS) {
  test(`a finger held on a card in ${region} lifts it, and the drop at the top saves the order`, async ({ page }) => {
    // Arrange
    const ids = tasks.map((item) => item.id);
    const { column, posted } = await open(page, region);
    const last = card(column, ids[3]);
    const from = await centre(last.locator(".card-title"));
    const to = await centre(card(column, ids[0]).locator(".card-title"));
    expect(await cardAt(page, from)).toBe(ids[3]);
    const finger = await fingerOn(page);

    // Act: the finger rests on the card's name.
    await finger.down(from);

    // Assert: the card lifts before the finger moves.
    await expect(last).toHaveClass(/dragging/);

    // Act: the finger takes it above the first card.
    for (let step = 1; step <= 10; step++) await finger.move({ x: from.x, y: from.y + (to.y - 20 - from.y) * step / 10 });

    // Assert
    await expect.poll(async () => (await ranked(column))[0]).toBe(ids[3]);

    // Act
    await finger.up();

    // Assert
    const order = [ids[3], ...ids.slice(0, 3)];
    await expect.poll(() => posted).toEqual([{ path: "/api/order", body: { list, order } }]);
    expect(await ranked(column)).toEqual(order);
    await expect(last).not.toHaveClass(/dragging/);
    await finger.detach();
  });
}

test("a finger that moves at once on a card scrolls the page and lifts no card, though it rests after", async ({ page }) => {
  // Arrange
  const ids = LISTS[0].tasks.map((item) => item.id);
  const { column, posted } = await open(page, "In progress");
  const last = card(column, ids[3]);
  const from = await centre(last.locator(".card-title"));
  const before = await page.evaluate(() => scrollY);
  expect(await cardAt(page, from)).toBe(ids[3]);
  const finger = await fingerOn(page);

  // Act: the finger swipes up the screen at once, then rests.
  await finger.down(from);
  for (let step = 1; step <= 10; step++) await finger.move({ x: from.x, y: from.y - 20 * step });
  await page.waitForTimeout(PAST_LIFT);

  // Assert
  expect(await page.evaluate(() => scrollY)).toBeGreaterThan(before);
  await expect(last).not.toHaveClass(/dragging/);

  // Act
  await finger.up();

  // Assert
  expect(posted).toEqual([]);
  expect(await ranked(column)).toEqual(ids);
  await finger.detach();
});

test("a card a finger lifted and let go in place opens nothing and keeps its place", async ({ page }) => {
  // Arrange
  const ids = LISTS[0].tasks.map((item) => item.id);
  const { column, posted } = await open(page, "In progress");
  const last = card(column, ids[3]);
  const from = await centre(last.locator(".card-title"));
  expect(await cardAt(page, from)).toBe(ids[3]);
  const finger = await fingerOn(page);

  // Act: the finger rests on the card's name until the card lifts, then lets
  // go where it is.
  await finger.down(from);
  await expect(last).toHaveClass(/dragging/);
  await finger.up();
  await page.waitForTimeout(PAST_TAP);

  // Assert
  await expect(column.locator(".task-cards.ranked")).toBeVisible();
  await expect(last).not.toHaveClass(/dragging/);
  await expect(last.locator(".task-card")).toHaveAttribute("aria-pressed", "false");
  expect(await ranked(column)).toEqual(ids);
  expect(posted).toEqual([]);
  await finger.detach();
});

test("a card a finger lifted and carries sideways in a paged list turns no page", async ({ page }) => {
  // Arrange
  const { column, posted } = await open(page, "Tasks", numbered(12, "queued", "queued", QUEUED));
  const pages = column.getByRole("group", { name: "Pages" });
  const shown = await pages.innerText();
  const second = card(column, "queued-2");
  const from = await centre(second.locator(".card-title"));
  expect(await cardAt(page, from)).toBe("queued-2");
  const finger = await fingerOn(page);

  // Act: the finger lifts the card and carries it across the screen.
  await finger.down(from);
  await expect(second).toHaveClass(/dragging/);
  for (let step = 1; step <= 6; step++) await finger.move({ x: from.x - 20 * step, y: from.y });
  await finger.up();

  // Assert
  await expect(column.locator(".ranked-item.dragging")).toHaveCount(0);
  await expect(pages).toHaveText(shown);
  expect(posted).toEqual([]);
  await finger.detach();
});
