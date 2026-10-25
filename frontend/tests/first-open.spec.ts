import { expect, holdStream, openTaskList, type Page, test } from "./site";

// The DOM renderer makes terminal output observable in headless screenshots.
test.use({ launchOptions: { args: ["--disable-webgl"] } });

// The Overlord, 2026-10-02, with a screenshot of his own board: "make sure
// that when you open the Code Goblins board for the first time, or any type of
// the Code Goblins app or UI for the first time, the default window is what
// you're seeing now on my screen, which would be the CFO terminal open and
// then the board on the left hand side." So the first time a browser shows the
// board of a home whose CFO runs, it opens on the Board with the CFO's
// panel beside it, not maximized, and the keyboard in it: since design A (the
// Overlord's choice on 2026-10-22) a Claude Code CFO's panel opens on its
// Messages view, whose box types into the CFO's terminal laid out behind it.
// What he arranges afterwards is kept, and a browser that already keeps a
// layout has had its first open.
const since = "2026-09-30T20:00:00Z";
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const TASKS = [
  task("queued-one", "queued", { generation: "", brief: true }),
  task("working-one", "working", { backend: "native" }),
  task("finished:done-one", "done", { archived: true, merged: true, verified: true, generation: "", branch: "fix/done-one" }),
];
const running = { healthy: true, instance: "fixture", cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "claude", revision: 1, attention: [], tasks: TASKS };

// open holds the supervisor's stream and answers each terminal's size claim
// with a repaint. kept is what the browser already keeps, set before load.
async function open(page: Page, snapshot: Record<string, unknown> = running, kept: Record<string, string> = {}) {
  const typed: string[] = [];
  await page.addInitScript((entries: Record<string, string>) => {
    for (const [key, value] of Object.entries(entries)) localStorage.setItem(key, value);
  }, kept);
  await holdStream(page, snapshot);
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/setup") await route.fulfill({ json: { home: "C:\\Users\\franco\\AppData\\Local\\CodeGoblins", agent: "claude", projects_root: "", checkouts: [], agents: [{ id: "claude", name: "Claude Code", recommended: true, note: "the best experience", installed: true, sign_in: "signed_in" }], cfo_runs: false } });
    else if (path === "/api/setup/start") await route.fulfill({ json: { started: true } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(JSON.stringify({ type: "size", cols: 60, rows: 20 }));
    socket.onMessage((data) => {
      if (typeof data !== "string") { typed.push(data.toString("utf8")); return; }
      const command: unknown = JSON.parse(data);
      if (typeof command === "object" && command !== null && "type" in command && command.type === "resize" && "cols" in command && "rows" in command) {
        socket.send(JSON.stringify({ type: "size", cols: command.cols, rows: command.rows }));
        socket.send(Buffer.from("\x1b[2J\x1b[HREADY\r\n"));
      }
    });
  });
  await page.goto("/");
  return typed;
}

const board = (page: Page) => page.getByRole("main", { name: "Board" });
const panel = (page: Page) => page.getByRole("complementary", { name: "Task review" });
const terminalInput = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });
const cfoBox = (page: Page) => page.getByRole("textbox", { name: "Message CFO", exact: true });
const columns = (page: Page) => page.locator(".task-board").evaluate((element) => getComputedStyle(element).gridTemplateColumns.split(" ").length);

test("a failed first Start keeps its error and selection while configuration snapshots change", async ({ page }, testInfo) => {
  // Arrange
  const initial = { ...running, cfo_runs: false, cfo_terminal: "", cfo_harness: "", cfo_configured: false };
  await page.addInitScript((snapshot) => {
    class HeldStream extends EventTarget {
      onerror: unknown = null;
      constructor() {
        super();
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(snapshot) })));
        window.addEventListener("setup-snapshot", (event) => {
          if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(event.detail) }));
        });
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: HeldStream });
  }, initial);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.route("**/api/setup?*", (route) => route.fulfill({ json: {
    home: "C:\\fixture", agent: "claude", projects_root: "", checkouts: [], cfo_runs: false,
    agents: [{ id: "claude", name: "Claude Code", recommended: true, installed: true, sign_in: "signed_in" }, { id: "codex", name: "Codex", recommended: false, installed: true, sign_in: "signed_in" }],
  } }));
  let release = () => {};
  const pending = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/setup/start", async (route) => {
    await pending;
    await route.fulfill({ status: 500, json: { error: "The chosen CFO could not start" } });
  });
  await page.goto("/");
  await page.getByRole("tab", { name: "Codex", exact: true }).click();
  await page.getByRole("textbox", { name: "Projects folder" }).fill("C:\\chosen-projects");

  // Act
  await page.getByRole("button", { name: "Start the CFO" }).click();
  await page.evaluate((snapshot) => window.dispatchEvent(new CustomEvent("setup-snapshot", { detail: snapshot })), { ...initial, cfo_configured: true, revision: 2 });

  // Assert
  await expect(page.getByRole("button", { name: "Starting the CFO…" })).toBeDisabled();
  const rejected = page.waitForResponse("**/api/setup/start");
  release();
  await rejected;
  await expect(page.locator(".first-run-actions .click-feedback")).toHaveText("The chosen CFO could not start.");
  await page.evaluate((snapshot) => window.dispatchEvent(new CustomEvent("setup-snapshot", { detail: snapshot })), { ...initial, revision: 3 });
  await expect(page.locator(".first-run-actions .click-feedback")).toHaveText("The chosen CFO could not start.");
  await expect(page.getByRole("tab", { name: "Codex", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("textbox", { name: "Projects folder" })).toHaveValue("C:\\chosen-projects");
  await expect(page.getByRole("button", { name: "Start the CFO" })).toBeEnabled();
  await page.locator(".first-run").screenshot({ path: testInfo.outputPath("first-start-failed.png") });

  await page.route("**/api/setup/start", async (route) => {
    expect(route.request().postDataJSON()).toEqual({ root: "", agent: "codex" });
    await route.fulfill({ json: { started: true } });
  });
  await page.getByRole("button", { name: "Start the CFO" }).click();
  await expect(board(page)).toBeVisible();
  await expect(page.getByRole("heading", { name: "Start Code Goblins" })).toHaveCount(0);
});

test("reopening a configured home with no registered CFO stays on the board and can retry", async ({ page }) => {
  // Arrange
  const setupRequests: string[] = [];
  const reopenRequests: { token: string | null; body: string | null }[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname.startsWith("/api/setup")) setupRequests.push(request.url());
  });
  await open(page, { ...running, cfo_runs: false, cfo_terminal: "", cfo_harness: "", cfo_configured: true });
  await page.route("**/api/cfo/reopen", async (route) => {
    reopenRequests.push({ token: await route.request().headerValue("X-CFO-Token"), body: route.request().postData() });
    return route.fulfill({ status: 500, json: { error: "The CFO could not be reopened" } });
  });

  // Act
  await expect(board(page)).toBeVisible();
  await page.getByRole("button", { name: "Reopen the CFO" }).click();

  // Assert
  await expect(page.locator(".cfo-pin .click-feedback")).toHaveText("The CFO could not be reopened.");
  await expect(page.getByRole("button", { name: "Reopen the CFO" })).toBeEnabled();
  expect(reopenRequests).toEqual([{ token: "fixture", body: "{}" }]);
  await page.reload();
  await expect(board(page)).toBeVisible();
  await expect(page.getByRole("button", { name: "Reopen the CFO" })).toBeEnabled();
  await expect(page.getByRole("heading", { name: "Start Code Goblins" })).toHaveCount(0);
  expect(setupRequests).toEqual([]);
});

test("reopening with the saved CFO running keeps the board and its Restart control", async ({ page }) => {
  // Arrange
  const setupRequests: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname.startsWith("/api/setup")) setupRequests.push(request.url());
  });
  await open(page, { ...running, cfo_configured: true }, { "cfo-first-open": "shown", "cfo-pane-width": "650" });

  // Act
  await page.reload();
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await openTaskList(page);

  // Assert
  await expect(board(page)).toBeVisible();
  await page.locator(".panel-content > .disclosure.harness > summary").click();
  await expect(page.locator(".panel-content > .disclosure.harness").getByRole("button", { name: "Restart the CFO" })).toBeEnabled();
  await expect(page.getByRole("heading", { name: "Start Code Goblins" })).toHaveCount(0);
  expect(setupRequests).toEqual([]);
});

// besideTheBoard is the default view: the Board with the CFO's panel on its
// Terminal view, its messages, to the right of it, neither covering the
// other, its terminal whole behind the messages.
async function besideTheBoard(page: Page) {
  await expect(page.getByRole("group", { name: "Workspace view" }).getByRole("button", { name: "Board" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await expect(page.getByRole("button", { name: "Maximize the panel" })).toBeVisible();
  await expect(board(page)).toBeVisible();
  await expect(cfoBox(page)).toBeVisible();
  await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
  await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
  await expect(page.locator(".terminal-view .xterm-rows")).toContainText("READY");
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

    test("the first open shows the board with the CFO's panel beside it and the keyboard in its box, which types into its terminal", async ({ page }, testInfo) => {
      // Act
      const typed = await open(page);

      // Assert
      await besideTheBoard(page);
      await expect(cfoBox(page)).toBeFocused();
      await page.keyboard.type("help");
      await page.keyboard.press("Enter");
      await expect.poll(() => typed.join("")).toBe("help\r");
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
    await page.locator(".task-card").filter({ hasText: "working-one" }).click();

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

// A window too narrow for two columns, a phone's, shows the board or an open
// panel across the whole screen, one at a time. The first open there shows
// the board alone, with the CFO's terminal a tap away on its bar, and leaves
// the keyboard and the scroll where they are.
test.describe("in a narrow window", () => {
  test.use({ viewport: { width: 390, height: 800 } });

  test("the first open shows the board alone, with the CFO's terminal a tap away and the keyboard left alone", async ({ page }, testInfo) => {
    // Act
    await open(page);

    // Assert
    await expect(page.locator(".task-card").first()).toBeVisible();
    await expect(board(page)).toBeInViewport();
    await expect(page.locator("#panel-title")).toHaveCount(0);
    await expect(terminalInput(page)).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Open the CFO's terminal" }).first()).toBeVisible();
    expect(await page.evaluate(() => window.scrollY)).toBe(0);
    await page.screenshot({ path: testInfo.outputPath("first-open-narrow.png") });
  });
});
