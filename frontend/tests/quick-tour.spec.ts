import { expect, test, type Locator, type Page } from "./site";

// The DOM renderer makes terminal output observable in headless screenshots.
test.use({ hadTour: false, launchOptions: { args: ["--disable-webgl"] } });

// The Overlord, 2026-10-02: "just a quick tour walk through, very quick, how
// it works, explaining how to use it would be really nice for new users".
// Approved on his review page, 2026-10-06: the CFO walks a new user through
// three steps on the first open, each lighting one part of the board and
// saying one line; it is skippable at once, shown once, and replayed from the
// question mark in the top bar, in the browser and the desktop window alike.
const since = "2026-09-30T20:00:00Z";
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const TASKS = [
  task("queued-one", "queued", { generation: "", brief: true }),
  task("working-one", "working", { backend: "native" }),
  task("finished:done-one", "done", { archived: true, merged: true, verified: true, generation: "", branch: "fix/done-one" }),
];
const running = { healthy: true, instance: "fixture", cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "claude", revision: 1, attention: [], tasks: TASKS };
const noCfo = { ...running, cfo_runs: false, cfo_terminal: "", cfo_harness: "" };

// open gives the page the supervisor's stream, held open as tests/site.ts
// holdStream does, which a test hands a later snapshot with send. Each
// terminal's size claim is answered with a repaint, and what is typed into a
// terminal is returned. kept is what the browser already keeps.
async function open(page: Page, snapshot: Record<string, unknown> = running, kept: Record<string, string> = {}) {
  const typed: string[] = [];
  await page.addInitScript((entries: Record<string, string>) => {
    for (const [key, value] of Object.entries(entries)) localStorage.setItem(key, value);
  }, kept);
  await page.addInitScript((data) => {
    class HeldStream extends EventTarget {
      onerror: (() => void) | null = null;
      constructor() {
        super();
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data })));
        addEventListener("test-snapshot", (event) => { if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: event.detail })); });
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: HeldStream });
  }, JSON.stringify(snapshot));
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/setup") await route.fulfill({ json: { home: "C:\\Users\\franco\\AppData\\Local\\CodeGoblins", agent: "claude", projects_root: "", checkouts: [], agents: [{ id: "claude", name: "Claude Code", recommended: true, note: "the best experience", installed: true, signed_in: true }], cfo_runs: false } });
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

const send = (page: Page, snapshot: Record<string, unknown>) => page.evaluate((data) => { dispatchEvent(new CustomEvent("test-snapshot", { detail: data })); }, JSON.stringify(snapshot));

const tour = (page: Page) => page.getByRole("dialog");
const board = (page: Page) => page.locator("main.canvas-region");
const panel = (page: Page) => page.locator("aside.context-pane");
const commandCenter = (page: Page) => page.locator(".command-center-menu > summary");
const terminalInput = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });
const box = async (locator: Locator) => (await locator.boundingBox())!;

// lights says the tour's step lights the part, dims around it, and keeps its
// card off it and inside the window.
async function lights(page: Page, part: Locator) {
  const [light, lit, card] = [await box(page.locator(".quick-tour-light")), await box(part), await box(page.locator(".quick-tour-card"))];
  for (const key of ["x", "y", "width", "height"] as const) expect(Math.abs(light[key] - lit[key])).toBeLessThan(1);
  const overlaps = card.x < lit.x + lit.width && lit.x < card.x + card.width && card.y < lit.y + lit.height && lit.y < card.y + card.height;
  expect(overlaps).toBe(false);
  const view = page.viewportSize()!;
  expect(card.x).toBeGreaterThanOrEqual(0);
  expect(card.y).toBeGreaterThanOrEqual(0);
  expect(card.x + card.width).toBeLessThanOrEqual(view.width);
  expect(card.y + card.height).toBeLessThanOrEqual(view.height);
}

// The desktop window, where he works, is 1707 CSS pixels wide at device scale
// 1.5; a full HD browser is the other common shape.
for (const [name, viewport, scale] of [
  ["the desktop window", { width: 1707, height: 960 }, 1.5],
  ["a full HD browser", { width: 1920, height: 1080 }, 1],
] as const) {
  test.describe(`in ${name}`, () => {
    test.use({ viewport, deviceScaleFactor: scale });

    test("the first open walks through the CFO's terminal, the board and the Command Center, then hands the keyboard to the terminal", async ({ page }, testInfo) => {
      // Three steps, each measured and photographed at the window's full size.
      test.slow();

      // Act
      const typed = await open(page);

      // Assert: step 1, the CFO's terminal, which the first open put beside
      // the board; the tour says how to speak, so no voice hint repeats it.
      await expect(tour(page).getByRole("heading", { name: "I'm the CFO. Tell me what to build." })).toBeVisible();
      await expect(page.locator("#panel-title")).toHaveText("CFO");
      await expect(page.locator(".terminal-view .xterm-rows")).toContainText("READY");
      await expect(tour(page).getByRole("button", { name: "Next" })).toBeFocused();
      await expect(tour(page).getByRole("img", { name: "Step 1 of 3" })).toBeVisible();
      await expect(page.getByText("Speak into this terminal")).toHaveCount(0);
      await lights(page, panel(page));
      await page.screenshot({ path: testInfo.outputPath("1-talk-to-the-cfo.png"), animations: "disabled" });

      // Act
      await page.keyboard.press("Enter");

      // Assert: step 2, the board.
      await expect(tour(page).getByRole("heading", { name: "Goblins do the work." })).toBeVisible();
      await expect(tour(page).getByRole("button", { name: "Next" })).toBeFocused();
      await lights(page, board(page));
      await page.screenshot({ path: testInfo.outputPath("2-goblins-do-the-work.png"), animations: "disabled" });

      // Act
      await tour(page).getByRole("button", { name: "Next" }).click();

      // Assert: step 3, the Command Center, and where the tour replays from.
      await expect(tour(page).getByRole("heading", { name: "When I need you, it waits here." })).toBeVisible();
      await expect(tour(page).getByText("Replay this tour with the question mark button.")).toBeVisible();
      await lights(page, commandCenter(page));
      await page.screenshot({ path: testInfo.outputPath("3-when-i-need-you.png"), animations: "disabled" });

      // Act
      await tour(page).getByRole("button", { name: "Done" }).click();

      // Assert: the board as the first open leaves it, the keyboard in the
      // CFO's terminal.
      await expect(tour(page)).toHaveCount(0);
      await expect(terminalInput(page)).toBeFocused();
      await page.keyboard.type("help");
      await expect.poll(() => typed.join("")).toBe("help");
      await expect(page.getByRole("button", { name: "Maximize the panel" })).toBeVisible();
    });
  });
}

test.describe("in the desktop window", () => {
  test.use({ viewport: { width: 1707, height: 960 }, deviceScaleFactor: 1.5 });

  for (const [how, skip] of [
    ["X", (page: Page) => tour(page).getByRole("button", { name: "Skip the tour" }).click()],
    ["Escape", (page: Page) => page.keyboard.press("Escape")],
  ] as const) {
    test(`${how} ends the tour at once and leaves the CFO's terminal open beside the board`, async ({ page }) => {
      // Arrange
      await open(page);
      await expect(tour(page)).toBeVisible();

      // Act
      await skip(page);

      // Assert
      await expect(tour(page)).toHaveCount(0);
      await expect(page.locator("#panel-title")).toHaveText("CFO");
      await expect(terminalInput(page)).toBeFocused();
      await expect(board(page)).toBeVisible();
    });
  }

  test("the tour shows once: a reload while it shows, or after it ended, shows none", async ({ page }) => {
    // Arrange
    await open(page);
    await expect(tour(page)).toBeVisible();

    // Act
    await page.reload();

    // Assert
    await expect(page.getByRole("heading", { name: "Review the work" })).toBeVisible();
    await expect(tour(page)).toHaveCount(0);
  });

  test("a browser that has used the board before shows no tour, and the question mark replays it from any panel", async ({ page }) => {
    // Arrange
    await open(page, running, { "cfo-first-open": "shown" });
    await expect(page.getByRole("heading", { name: "Review the work" })).toBeVisible();
    await expect(tour(page)).toHaveCount(0);
    await page.getByRole("button", { name: "Open the terminal of working-one" }).click();
    await expect(page.locator("#panel-title")).toHaveText("working-one");

    // Act
    await page.getByRole("button", { name: "Replay the tour" }).click();

    // Assert: it begins at the CFO's terminal, wherever the panel was.
    await expect(tour(page).getByRole("heading", { name: "I'm the CFO. Tell me what to build." })).toBeVisible();
    await expect(page.locator("#panel-title")).toHaveText("CFO");
    await lights(page, panel(page));
  });

  test("with no CFO the tour waits for the one the first-run page starts, through a reload", async ({ page }) => {
    // Arrange
    await open(page, noCfo);
    await expect(page.getByRole("heading", { name: "Start Code Goblins" })).toBeVisible();

    // Act
    await page.getByRole("button", { name: "Start the CFO" }).click();

    // Assert: the board, with no tour while the CFO is not running yet.
    await expect(page.locator("#panel-title")).toHaveText("CFO");
    await expect(tour(page)).toHaveCount(0);

    // Act: the page reloads before the CFO runs, then the CFO runs.
    await page.reload();
    await expect(page.getByRole("heading", { name: "Start Code Goblins" })).toBeVisible();
    await send(page, { ...running, revision: 2 });

    // Assert
    await expect(tour(page).getByRole("heading", { name: "I'm the CFO. Tell me what to build." })).toBeVisible();
    await lights(page, panel(page));
  });
});

// A window too narrow for two columns shows the board, then the panel under
// it. The tour lights each part there too, its card inside the window, and
// its end leaves the keyboard alone, as the first open there does.
test.describe("in a narrow window", () => {
  test.use({ viewport: { width: 390, height: 800 } });

  test("the tour fits the window and its end leaves the keyboard alone", async ({ page }, testInfo) => {
    // Act
    await open(page);

    // Assert
    await expect(tour(page).getByRole("heading", { name: "I'm the CFO. Tell me what to build." })).toBeVisible();
    const card = await box(page.locator(".quick-tour-card"));
    expect(card.x).toBeGreaterThanOrEqual(0);
    expect(card.x + card.width).toBeLessThanOrEqual(390);
    expect(card.y + card.height).toBeLessThanOrEqual(800);
    await page.screenshot({ path: testInfo.outputPath("narrow-1.png"), animations: "disabled" });

    // Act
    await page.keyboard.press("Enter");
    await page.keyboard.press("Enter");
    await expect(tour(page).getByRole("heading", { name: "When I need you, it waits here." })).toBeVisible();
    await lights(page, commandCenter(page));
    await page.screenshot({ path: testInfo.outputPath("narrow-3.png"), animations: "disabled" });
    await tour(page).getByRole("button", { name: "Done" }).click();

    // Assert
    await expect(tour(page)).toHaveCount(0);
    await expect(terminalInput(page)).not.toBeFocused();
  });
});
