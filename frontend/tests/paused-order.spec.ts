import { expect, holdStream, test, type Locator, type Page } from "./site";

// The Overlord, 2026-10-17, with the paused list he looked at, a goblin paused
// until tomorrow on top and Next up near the bottom: "make sure that the next
// up one is the top priority of what will unpause, and that it's organized
// positionally. You can still click and drag. You don't need to put the
// numberings there". Paused runs as its goblins resume: Next up, then the
// rest paused for memory in his order, then each paused until a time,
// earliest first, then each waiting on another task, a pull request, CI or a
// person. A drop saves the In progress order the supervisor resumes by.
const GB = 2 ** 30;
const now = Date.parse("2026-10-17T18:00:00Z");
const at = (minutes: number) => new Date(now + minutes * 60_000).toISOString();
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since: at(-95), ...fields });
const paused = (id: string, reason: string, until: string) => task(id, "paused", { at: at(-60),
  lifecycle: { phase: "paused", action: "pause", at: at(-60), kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason, until, at: at(-60) } } });
// In the snapshot's order, his In progress order, as the board he looked at
// listed them, with a pause of every other kind after.
const PAUSED = [
  paused("wanda", "dependency", "date:" + at(19 * 60)),
  paused("murray", "dependency", "task:wanda"),
  paused("pearl", "dependency", "date:" + at(90)),
  paused("duke", "dependency", "date:" + at(90)),
  paused("oona", "dependency", "date:" + at(45)),
  paused("greta", "memory", ""),
  paused("dex", "memory", ""),
  paused("reset", "allowance", at(60)),
  paused("pull", "dependency", "pr:https://github.com/o/r/pull/331"),
  paused("asks", "question", "q-7"),
  paused("yours", "overlord", ""),
];
const RESUME_ORDER = ["greta", "dex", "oona", "reset", "pearl", "duke", "wanda", "murray", "pull", "asks", "yours"];
const WORKING = [task("working-one", "working"), task("working-two", "working")];
const memory = { available: 4.6 * GB, total: 32 * GB, commit_available: 20 * GB, commit_limit: 48 * GB, paged_pool: 0.5 * GB, nonpaged_pool: 0.3 * GB, floor: 4 * GB, next: 5 * GB, holders: [] };

interface Posted { path: string; body: Record<string, unknown> }

// Opens the board on a stream that stays open, so no reconnect moves a card
// from under the pointer. posted collects what it asked the supervisor to
// change.
async function open(page: Page, tasks: object[] = [...WORKING, ...PAUSED]) {
  const posted: Posted[] = [];
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  await page.clock.install({ time: now });
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], memory, tasks });
  await page.route("**/api/**", async (route) => {
    if (route.request().method() !== "POST") return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    posted.push({ path: new URL(route.request().url()).pathname, body: route.request().postDataJSON() });
    await route.fulfill({ json: { revision: 2 } });
  });
  await page.goto("/");
  const section = page.getByRole("region", { name: "Paused", exact: true });
  await expect(section.locator("[data-sort-id]")).toHaveCount(tasks.filter((item) => (item as { phase: string }).phase === "paused").length);
  await page.evaluate(() => document.fonts.ready);
  return { section, posted };
}

const order = (list: Locator) => list.locator("[data-sort-id]").evaluateAll((cards) => cards.map((card) => card.getAttribute("data-sort-id")));

// Drags a card by its title until its middle is above the middle of another.
async function drag(page: Page, card: Locator, over: Locator) {
  const title = card.locator(".card-title");
  await title.scrollIntoViewIfNeeded();
  const from = (await title.boundingBox())!, to = (await over.boundingBox())!;
  const start = { x: from.x + 24, y: from.y + 12 };
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  await page.mouse.move(start.x, start.y + 12, { steps: 3 });
  await expect(card).toHaveClass(/dragging/);
  await page.mouse.move(start.x, to.y + 4, { steps: 8 });
  await page.mouse.up();
}

// The times read as on his desk, in Central time.
test.use({ viewport: { width: 1440, height: 2400 }, timezoneId: "America/Chicago" });

test("Paused runs as its goblins resume, Next up on top, whatever order he left them in", async ({ page }) => {
  // Arrange
  const { section } = await open(page);

  // Act
  const shown = await order(section);

  // Assert
  expect(shown).toEqual(RESUME_ORDER);
  await expect(page.locator(".task-board .next-chip")).toHaveCount(1);
  await expect(section.locator("[data-sort-id]").first().locator(".next-chip")).toHaveText("Next up");
  await expect(section.locator(".card-status-text")).toHaveText([
    "Paused for memory", "Paused for memory", "Paused until 1:45 PM", "Paused for allowance", "Paused until 2:30 PM", "Paused until 2:30 PM",
    "Paused until Sun", "Paused until wanda delivers", "Paused until PR #331 merges", "Paused until you answer", "Paused by you",
  ]);
  // No numbers: his words, "You don't need to put the numberings there".
  await expect(section.locator(".rank")).toHaveCount(0);
});

test("a goblin paused until a time joins the goblins resuming, in his order, once its time passes", async ({ page }) => {
  // Arrange
  const { section } = await open(page);

  // Act: 46 minutes on, Oona's 1:45 PM has passed.
  await page.clock.fastForward(46 * 60_000);

  // Assert: she is above Greta in his order, so she resumes first and is
  // Next up.
  await expect.poll(() => order(section)).toEqual(["oona", "greta", "dex", "reset", "pearl", "duke", "wanda", "murray", "pull", "asks", "yours"]);
  await expect(section.locator("[data-sort-id='oona'] .next-chip")).toHaveText("Next up");
  await expect(page.locator(".task-board .next-chip")).toHaveCount(1);
});

test("dragging a goblin paused for memory above Next up saves it first in the order the supervisor resumes by", async ({ page }) => {
  // Arrange
  const { section, posted } = await open(page);

  // Act
  await drag(page, section.locator("[data-sort-id='dex']"), section.locator("[data-sort-id='greta']"));

  // Assert
  const resumes = ["dex", "greta", ...RESUME_ORDER.slice(2)];
  await expect.poll(() => posted).toEqual([{ path: "/api/order", body: { list: "progress", order: ["working-one", "working-two", ...resumes] } }]);
  expect(await order(section)).toEqual(resumes);
});

test("a drop the resume order undoes puts the card back, says why and saves nothing", async ({ page }) => {
  // Arrange
  const { section, posted } = await open(page);

  // Act: Wanda resumes tomorrow, so she cannot go above Next up.
  await drag(page, section.locator("[data-sort-id='wanda']"), section.locator("[data-sort-id='greta']"));

  // Assert
  await expect(page.locator(".workspace .notice")).toHaveText("Paused goblins stay in the order they resume.");
  expect(await order(section)).toEqual(RESUME_ORDER);
  expect(posted).toEqual([]);
});

test("a drag among the working goblins keeps the paused goblins' order after them", async ({ page }) => {
  // Arrange
  const { posted } = await open(page);
  const progress = page.getByRole("region", { name: "In progress", exact: true }).locator(".task-cards.ranked").first();

  // Act
  await drag(page, progress.locator("[data-sort-id='working-two']"), progress.locator("[data-sort-id='working-one']"));

  // Assert
  await expect.poll(() => posted).toEqual([{ path: "/api/order", body: { list: "progress", order: ["working-two", "working-one", ...RESUME_ORDER] } }]);
});

test("a drag among the working goblins before the snapshot catches up keeps a paused drop it has not seen", async ({ page }) => {
  // Arrange
  const { section, posted } = await open(page);
  const progress = page.getByRole("region", { name: "In progress", exact: true }).locator(".task-cards.ranked").first();
  await drag(page, section.locator("[data-sort-id='dex']"), section.locator("[data-sort-id='greta']"));
  await expect.poll(() => posted.length).toBe(1);

  // Act: no snapshot at the saved revision has streamed in.
  await drag(page, progress.locator("[data-sort-id='working-two']"), progress.locator("[data-sort-id='working-one']"));

  // Assert
  await expect.poll(() => posted.length).toBe(2);
  expect(posted[1]).toEqual({ path: "/api/order", body: { list: "progress", order: ["working-two", "working-one", "dex", "greta", ...RESUME_ORDER.slice(2)] } });
});
