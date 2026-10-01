import { expect, test, type Page } from "@playwright/test";

// The board as the Overlord approved it: a kanban of Tasks, In progress and
// Completed side by side, paused tasks inside In progress under a divider,
// and a button in the top bar that switches to the stacked layout and back,
// remembered in this browser.
const since = "2026-09-30T20:00:00Z";
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const BOARD = [
  task("queued-one", "queued", { generation: "", brief: true }),
  task("working-one", "working"),
  task("working-two", "waiting", { waiting_on: "ci" }),
  task("paused-one", "paused"),
  task("finished:done-one", "done", { archived: true, merged: true, verified: true, generation: "", branch: "fix/done-one" }),
];

async function open(page: Page, tasks: Record<string, unknown>[] = BOARD) {
  const snapshot = { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], tasks };
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/events") await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}
const columns = (page: Page) => page.locator(".task-board").evaluate((board) => getComputedStyle(board).gridTemplateColumns.split(" ").length);
const inProgress = (page: Page) => page.getByRole("region", { name: "In progress", exact: true });

test.describe("on a wide screen", () => {
  test.use({ viewport: { width: 2400, height: 1300 } });

  test("the board is a kanban by default, with paused tasks under a divider inside In progress", async ({ page }) => {
    await open(page);
    expect(await columns(page)).toBe(3);
    expect(await page.locator(".board-column").evaluateAll((sections) => sections.map((section) => section.getAttribute("aria-label")))).toEqual(["Tasks", "In progress", "Completed"]);
    const tops = await page.locator(".board-column").evaluateAll((sections) => sections.map((section) => Math.round(section.getBoundingClientRect().top)));
    expect(new Set(tops).size).toBe(1);
    const paused = inProgress(page).getByRole("region", { name: "Paused", exact: true });
    await expect(paused.getByRole("heading", { name: /^Paused/ })).toHaveText("Paused1");
    await expect(paused.getByRole("button", { name: "Resume paused-one" })).toBeVisible();
    await expect(paused.getByRole("button", { name: "Stop paused-one" })).toBeVisible();
    const lastWorking = (await inProgress(page).locator(".task-card-shell").filter({ hasText: "working-two" }).boundingBox())!;
    expect((await paused.boundingBox())!.y).toBeGreaterThan(lastWorking.y + lastWorking.height);
  });

  test("with nothing paused there is no divider", async ({ page }) => {
    await open(page, BOARD.filter((item) => item.phase !== "paused"));
    await expect(inProgress(page).locator(".task-card-shell")).toHaveCount(2);
    await expect(page.getByRole("region", { name: "Paused", exact: true })).toHaveCount(0);
  });

  test("the layout button switches to stacked and back, and the browser remembers it", async ({ page }) => {
    await open(page);
    await page.getByRole("button", { name: "Stacked layout" }).click();
    expect(await columns(page)).toBe(1);
    await expect(page.locator(".task-board")).toHaveClass(/stacked/);
    await page.reload();
    await expect(page.locator(".board-column").first()).toBeVisible();
    expect(await columns(page)).toBe(1);
    await page.getByRole("button", { name: "Kanban layout" }).click();
    expect(await columns(page)).toBe(3);
    await page.reload();
    await expect(page.locator(".board-column").first()).toBeVisible();
    expect(await columns(page)).toBe(3);
  });

  test("a browser whose storage is blocked still gets the kanban and can switch", async ({ page }) => {
    await page.addInitScript(() => Object.defineProperty(window, "localStorage", { get() { throw new Error("storage blocked"); } }));
    await open(page);
    expect(await columns(page)).toBe(3);
    await page.getByRole("button", { name: "Stacked layout" }).click();
    expect(await columns(page)).toBe(1);
  });
});

test("a board too narrow for three columns stacks either way", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await open(page);
  expect(await page.locator(".task-board").evaluate((board) => board.clientWidth)).toBeLessThan(960);
  expect(await columns(page)).toBe(1);
  await expect(page.getByRole("button", { name: "Stacked layout" })).toBeVisible();
});

// In progress pages once it holds more than ten cards; its paused tasks under
// the divider keep their room, so they show without scrolling the board.
test("when In progress pages, its paused tasks still show on the board", async ({ page }) => {
  await page.setViewportSize({ width: 2400, height: 1100 });
  const working = Array.from({ length: 12 }, (_, index) => task("working-" + (index + 1), "working"));
  await open(page, [...working, task("paused-one", "paused")]);
  await expect(inProgress(page).locator(".pager")).toBeVisible();
  const canvas = (await page.locator(".canvas-region").boundingBox())!;
  const paused = (await inProgress(page).getByRole("region", { name: "Paused", exact: true }).boundingBox())!;
  expect(paused.y + paused.height).toBeLessThanOrEqual(canvas.y + canvas.height);
});
