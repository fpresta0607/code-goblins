import { expect, holdStream, test, type Locator, type Page } from "./site";

// The Overlord on 2026-10-08: "i also dont like how number disappears in board
// on hover". A ranked card's number shows its place while the pointer is on
// the card, while the card has focus and while it is dragged, and the card
// still moves by mouse, by a finger on its number and by Alt with an arrow.
const since = "2026-10-08T18:00:00Z";
const WORKING = Array.from({ length: 4 }, (_, index) => ({ id: "working-" + (index + 1), title: "working-" + (index + 1), project: "code-goblins", phase: "working", verified: false, generation: "g" + (index + 1), since, harness: "codex" }));

interface Posted { path: string; body: Record<string, unknown> }

// Opens a board of four goblins in progress on a stream that stays open, so
// no reconnect banner moves a card from under the pointer. posted collects
// what the board asked the supervisor to change.
async function open(page: Page) {
  const posted: Posted[] = [];
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], tasks: WORKING });
  await page.route("**/api/**", async (route) => {
    if (route.request().method() !== "POST") return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    posted.push({ path: new URL(route.request().url()).pathname, body: route.request().postDataJSON() });
    await route.fulfill({ json: { revision: 2 } });
  });
  await page.goto("/");
  const progress = page.getByRole("region", { name: "In progress", exact: true });
  await expect(progress.locator("[data-sort-id]")).toHaveCount(WORKING.length);
  return { progress, posted };
}

const card = (list: Locator, id: string) => list.locator(`[data-sort-id='${id}']`);
const ranked = (list: Locator) => list.locator("[data-sort-id]").evaluateAll((cards) => cards.map((item) => item.getAttribute("data-sort-id")));
const place = (item: Locator) => item.locator(".rank");
// Whether a card's badge is drawn over everything at its centre, its own
// lifted card included. The badge lets a mouse through to the card, so it
// takes the pointer only for the moment it is looked for.
const onTop = (item: Locator) => place(item).evaluate((badge: HTMLElement) => {
  const box = badge.getBoundingClientRect(), before = badge.style.pointerEvents;
  badge.style.pointerEvents = "auto";
  const top = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
  badge.style.pointerEvents = before;
  return !!top && badge.contains(top);
});
// A card's badge shows its number in sight: read as the screen draws it, so a
// number the page hides reads as nothing, and not covered by the card.
async function shows(item: Locator, number: number) {
  await expect(place(item)).toHaveText(String(number), { useInnerText: true });
  await expect.poll(() => onTop(item)).toBe(true);
}
const centre = async (part: Locator) => { const box = (await part.boundingBox())!; return { x: box.x + box.width / 2, y: box.y + box.height / 2 }; };

test("a card's number stays in sight while the pointer is on it and while it is dragged, and the drop saves its place", async ({ page }) => {
  // Arrange
  const { progress, posted } = await open(page);
  const third = card(progress, "working-3");
  const from = await centre(third.locator(".card-title"));
  const to = await centre(card(progress, "working-1").locator(".card-title"));

  // Act and Assert: the pointer rests on the card.
  await page.mouse.move(from.x, from.y);
  await shows(third, 3);

  // Held and moved, the card is dragged and shows the place it would take.
  await page.mouse.down();
  await page.mouse.move(from.x, from.y - 12, { steps: 3 });
  await expect(third).toHaveClass(/dragging/);
  await shows(third, 3);
  await page.mouse.move(to.x, to.y - 20, { steps: 6 });
  await expect.poll(async () => (await ranked(progress))[0]).toBe("working-3");
  await shows(third, 1);

  // Dropped, it keeps its new place under the pointer.
  await page.mouse.up();
  const order = ["working-3", "working-1", "working-2", "working-4"];
  await expect.poll(() => posted).toEqual([{ path: "/api/order", body: { list: "progress", order } }]);
  expect(await ranked(progress)).toEqual(order);
  await expect(third).not.toHaveClass(/dragging/);
  await shows(third, 1);
});

test("a focused card's number stays in sight, and Alt with an arrow moves the card and its number", async ({ page }) => {
  // Arrange
  const { progress, posted } = await open(page);
  const second = card(progress, "working-2");
  await page.mouse.move(0, 0);

  // Act
  await second.locator(".task-card").focus();

  // Assert
  await shows(second, 2);

  // Act
  await page.keyboard.press("Alt+ArrowDown");

  // Assert
  const order = ["working-1", "working-3", "working-2", "working-4"];
  await expect.poll(() => posted).toEqual([{ path: "/api/order", body: { list: "progress", order } }]);
  expect(await ranked(progress)).toEqual(order);
  await expect(second.locator(".task-card")).toBeFocused();
  await shows(second, 3);
});

test.describe("on a touch screen", () => {
  test.use({ hasTouch: true });

  test("a finger on a card's number drags the card, and the number stays in sight", async ({ page }) => {
    // Arrange
    const { progress, posted } = await open(page);
    const fourth = card(progress, "working-4");
    const from = await centre(place(fourth));
    const to = await centre(place(card(progress, "working-2")));
    const touch = await page.context().newCDPSession(page);
    const finger = (type: "touchStart" | "touchMove", x: number, y: number) => touch.send("Input.dispatchTouchEvent", { type, touchPoints: [{ x, y }] });

    // Act
    await finger("touchStart", from.x, from.y);
    for (let step = 1; step <= 8; step++) await finger("touchMove", from.x, from.y + (to.y - 20 - from.y) * step / 8);

    // Assert
    await expect(fourth).toHaveClass(/dragging/);
    await expect.poll(async () => (await ranked(progress))[1]).toBe("working-4");
    await shows(fourth, 2);

    // Act
    await touch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });

    // Assert
    const order = ["working-1", "working-4", "working-2", "working-3"];
    await expect.poll(() => posted).toEqual([{ path: "/api/order", body: { list: "progress", order } }]);
    expect(await ranked(progress)).toEqual(order);
    await shows(fourth, 2);
    await touch.detach();
  });
});
