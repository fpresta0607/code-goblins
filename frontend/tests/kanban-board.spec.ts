import { expect, test, type Page } from "./site";

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

// A window that cannot hold three columns beside a panel stacks the board
// whatever the layout, so there the layout button changes nothing and says
// why; with the panel closed the board has the room and the button works.
test("a board too narrow for three columns stacks, and its layout button says why", async ({ page }) => {
  await page.setViewportSize({ width: 1300, height: 900 });
  await open(page);
  expect(await page.locator(".canvas-region").evaluate((canvas) => canvas.clientWidth)).toBeLessThanOrEqual(960);
  expect(await columns(page)).toBe(1);
  const layout = page.getByRole("button", { name: /^Layout: stacked/ });
  await expect(layout).toHaveAttribute("aria-disabled", "true");
  await expect(layout).toHaveAttribute("data-tip", /^Too narrow for columns side by side, so the board is stacked\./);
  // The button is marked disabled, so the press skips the wait for an enabled one.
  await layout.click({ force: true });
  expect(await columns(page)).toBe(1);
  await page.getByRole("button", { name: "Close panel" }).click();
  await expect(page.getByRole("button", { name: "Stacked layout" })).toBeVisible();
  expect(await columns(page)).toBe(3);
});

// The Overlord's window on 2026-10-02: 2560 by 1600 at 150 percent, so 1707
// CSS pixels wide, each drawn on one and a half device pixels. The panel took half and left the board 835, under the 961
// its columns need, and the layout button looked dead. By default the panel
// yields to a kanban board, long enough here to scroll, since its scroll bar
// takes room from its columns.
test.describe("in a window 1707 px wide", () => {
  test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

  test("the kanban keeps its three columns beside an open panel", async ({ page }) => {
    await open(page, [...BOARD, ...Array.from({ length: 9 }, (_, index) => task("working-" + (index + 3), "working"))]);
    expect(await page.locator(".canvas-region").evaluate((canvas) => canvas.scrollHeight > canvas.clientHeight)).toBe(true);
    await expect(page.locator(".context-pane")).toBeVisible();
    expect(await columns(page)).toBe(3);
    // The CFO's own panel, on its Task view, beside the board.
    await page.keyboard.press("Control+Alt+1");
    await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
    await expect(page.locator(".context-pane")).toBeVisible();
    expect(await columns(page)).toBe(3);
    expect(await page.locator(".canvas-region").evaluate((canvas) => canvas.clientWidth)).toBeGreaterThan(960);
    expect(Math.round(await page.locator(".context-pane").evaluate((pane) => pane.getBoundingClientRect().width))).toBe(693);
    await expect(page.getByRole("button", { name: "Stacked layout" })).toBeVisible();
  });

  test("a stacked board leaves the panel its half", async ({ page }) => {
    await open(page);
    await page.getByRole("button", { name: "Stacked layout" }).click();
    expect(await columns(page)).toBe(1);
    const board = await page.locator(".canvas-region").evaluate((canvas) => canvas.getBoundingClientRect().width);
    const pane = await page.locator(".context-pane").evaluate((panel) => panel.getBoundingClientRect().width);
    expect(Math.abs(board - pane)).toBeLessThanOrEqual(1);
  });

  test("a panel width the Overlord dragged still wins", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("cfo-pane-width", "900"));
    await open(page);
    expect(Math.round(await page.locator(".context-pane").evaluate((pane) => pane.getBoundingClientRect().width))).toBe(900);
    expect(await columns(page)).toBe(1);
    await expect(page.getByRole("button", { name: /^Layout: stacked/ })).toHaveAttribute("aria-disabled", "true");
  });
});

// In progress never pages, so past ten goblins the board scrolls and the
// paused tasks follow the last working card.
test("past ten goblins In progress still shows every card, with its paused tasks under the last", async ({ page }) => {
  await page.setViewportSize({ width: 2400, height: 1100 });
  const working = Array.from({ length: 12 }, (_, index) => task("working-" + (index + 1), "working"));
  await open(page, [...working, task("paused-one", "paused")]);
  await expect(inProgress(page).locator("[data-sort-id]")).toHaveCount(12);
  await expect(inProgress(page).locator(".pager")).toHaveCount(0);
  const last = (await inProgress(page).locator("[data-sort-id='working-12']").boundingBox())!;
  const paused = (await inProgress(page).getByRole("region", { name: "Paused", exact: true }).boundingBox())!;
  expect(paused.y).toBeGreaterThan(last.y + last.height);
});
