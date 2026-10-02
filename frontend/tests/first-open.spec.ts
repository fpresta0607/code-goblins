import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-02, with a screenshot of his own board: "make sure
// that when you open the Code Goblins board for the first time, or any type of
// the Code Goblins app or UI for the first time, the default window is what
// you're seeing now on my screen, which would be the CFO terminal open and
// then the board on the left hand side." So the first time a browser shows the
// board of a home whose CFO runs, it opens on the Board with the CFO's
// terminal beside it, not maximized, and the keyboard in that terminal. What
// he arranges afterwards is kept, and a browser that already keeps a layout
// has had its first open.
const since = "2026-09-30T20:00:00Z";
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const TASKS = [
  task("queued-one", "queued", { generation: "", brief: true }),
  task("working-one", "working", { backend: "native" }),
  task("finished:done-one", "done", { archived: true, merged: true, verified: true, generation: "", branch: "fix/done-one" }),
];
const running = { healthy: true, instance: "fixture", cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "claude", revision: 1, attention: [], tasks: TASKS };

// open shows the board over a supervisor that sends one snapshot and answers
// every terminal with an empty screen. kept is what the browser already
// keeps, set before the page loads.
async function open(page: Page, snapshot: Record<string, unknown> = running, kept: Record<string, string> = {}) {
  await page.addInitScript((entries: Record<string, string>) => {
    for (const [key, value] of Object.entries(entries)) localStorage.setItem(key, value);
  }, kept);
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/events") await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
    else if (path === "/api/setup") await route.fulfill({ json: { home: "C:\\Users\\franco\\AppData\\Local\\CodeGoblins", agent: "claude", projects_root: "", checkouts: [], agents: [{ id: "claude", name: "Claude Code", recommended: true, note: "the best experience", installed: true, signed_in: true }], cfo_runs: false } });
    else if (path === "/api/setup/start") await route.fulfill({ json: { started: true } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from("READY\r\n"));
  });
  await page.goto("/");
}

const board = (page: Page) => page.getByRole("main", { name: "Board" });
const panel = (page: Page) => page.getByRole("complementary", { name: "Task review" });
const terminalInput = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });
const columns = (page: Page) => page.locator(".task-board").evaluate((element) => getComputedStyle(element).gridTemplateColumns.split(" ").length);

// besideTheBoard is the default view: the Board with the CFO's panel on its
// Terminal view to the right of it, neither covering the other.
async function besideTheBoard(page: Page) {
  await expect(page.getByRole("group", { name: "Workspace view" }).getByRole("button", { name: "Board" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await expect(page.getByRole("group", { name: "Panel view" }).getByRole("button", { name: "Terminal" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("button", { name: "Maximize the panel" })).toBeVisible();
  await expect(board(page)).toBeVisible();
  await expect(terminalInput(page)).toBeVisible();
  const [left, right] = [(await board(page).boundingBox())!, (await panel(page).boundingBox())!];
  expect(left.x + left.width).toBeLessThanOrEqual(right.x);
  expect(Math.abs(left.y - right.y)).toBeLessThan(2);
}

// The desktop window, where he works, is 1707 CSS pixels wide at device scale
// 1.5 (maximized on a 2560 by 1600 screen at 150 percent). There and on any
// window wider than 1410 pixels the panel yields, so the kanban keeps its
// three columns; a narrower window still shows both, with the board stacked.
for (const [name, viewport, scale, wantColumns] of [
  ["the desktop window", { width: 1707, height: 960 }, 1.5, 3],
  ["a full HD browser", { width: 1920, height: 1080 }, 1, 3],
  ["a small laptop", { width: 1280, height: 800 }, 1, 1],
] as const) {
  test.describe(`in ${name}`, () => {
    test.use({ viewport, deviceScaleFactor: scale });

    test("the first open shows the board with the CFO's terminal beside it and the keyboard in the terminal", async ({ page }, testInfo) => {
      // Act
      await open(page);

      // Assert
      await besideTheBoard(page);
      await expect(terminalInput(page)).toBeFocused();
      expect(await columns(page)).toBe(wantColumns);
      await page.screenshot({ path: testInfo.outputPath("first-open.png") });
    });
  });
}

test.describe("in the desktop window", () => {
  test.use({ viewport: { width: 1707, height: 960 }, deviceScaleFactor: 1.5 });

  test("a second open opens nothing by itself, and the terminal he opens is still beside the board", async ({ page }) => {
    // Arrange
    await open(page);
    await besideTheBoard(page);

    // Act
    await page.reload();

    // Assert: the board as it opened before this change, with nothing chosen.
    await expect(page.getByRole("heading", { name: "Review the work" })).toBeVisible();
    await expect(terminalInput(page)).toHaveCount(0);

    // Act
    await page.getByRole("button", { name: "Open the CFO's terminal" }).first().click();

    // Assert
    await besideTheBoard(page);
  });

  test("a browser that keeps a layout keeps its own: nothing opens by itself, and its width and maximize choice hold", async ({ page }) => {
    // Arrange: a width he dragged and a terminal he maximized, kept before
    // any first open was recorded.
    await open(page, running, { "cfo-pane-width": "520", "cfo-terminal-maximized": "true" });

    // Assert
    await expect(page.getByRole("heading", { name: "Review the work" })).toBeVisible();
    await expect(terminalInput(page)).toHaveCount(0);
    expect(Math.round((await panel(page).boundingBox())!.width)).toBe(520);

    // Act
    await page.getByRole("button", { name: "Open the CFO's terminal" }).first().click();

    // Assert: maximized, as he left it.
    await expect(page.getByRole("button", { name: "Restore the panel" })).toBeVisible();
    await expect(board(page)).toBeHidden();
    expect(await page.evaluate(() => [localStorage.getItem("cfo-pane-width"), localStorage.getItem("cfo-terminal-maximized")])).toEqual(["520", "true"]);
  });

  test("a goblin's terminal he never sized opens beside the board too", async ({ page }) => {
    // Arrange
    await open(page);
    await besideTheBoard(page);

    // Act
    await page.getByRole("button", { name: "Open the terminal of working-one" }).click();

    // Assert
    await expect(page.locator("#panel-title")).toHaveText("working-one");
    await expect(page.getByRole("button", { name: "Maximize the panel" })).toBeVisible();
    await expect(board(page)).toBeVisible();
  });

  test("with no CFO the first-run page comes first, and Start lands on the board with the CFO's terminal beside it", async ({ page }) => {
    // Arrange
    await open(page, { ...running, cfo_runs: false, cfo_terminal: "", cfo_harness: "" });
    await expect(page.getByRole("heading", { name: "Start Code Goblins" })).toBeVisible();

    // Act
    await page.getByRole("button", { name: "Start the CFO" }).click();

    // Assert: the CFO's panel on its Terminal view beside the board; the
    // snapshot here never shows the CFO running, so its terminal has no
    // screen yet.
    await expect(page.locator("#panel-title")).toHaveText("CFO");
    await expect(page.getByRole("group", { name: "Workspace view" }).getByRole("button", { name: "Board" })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByRole("button", { name: "Maximize the panel" })).toBeVisible();
    await expect(board(page)).toBeVisible();
    const [left, right] = [(await board(page).boundingBox())!, (await panel(page).boundingBox())!];
    expect(left.x + left.width).toBeLessThanOrEqual(right.x);
  });
});

// A window too narrow for two columns shows one: the board, then the panel
// under it. The first open there chooses the same view, the CFO's terminal,
// but leaves the keyboard and the scroll where they are, so the board is what
// he sees first.
test.describe("in a narrow window", () => {
  test.use({ viewport: { width: 390, height: 800 } });

  test("the first open keeps the board in view, with the CFO's terminal under it and the keyboard left alone", async ({ page }, testInfo) => {
    // Act
    await open(page);

    // Assert
    await expect(page.locator("#panel-title")).toHaveText("CFO");
    await expect(page.getByRole("group", { name: "Panel view" }).getByRole("button", { name: "Terminal" })).toHaveAttribute("aria-pressed", "true");
    await expect(terminalInput(page)).toBeAttached();
    await expect(terminalInput(page)).not.toBeFocused();
    expect(await page.evaluate(() => window.scrollY)).toBe(0);
    const [top, under] = [(await board(page).boundingBox())!, (await panel(page).boundingBox())!];
    expect(top.y + top.height).toBeLessThanOrEqual(under.y);
    await expect(board(page)).toBeInViewport();
    await page.screenshot({ path: testInfo.outputPath("first-open-narrow.png") });
  });
});
