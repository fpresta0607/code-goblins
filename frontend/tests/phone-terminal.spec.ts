import { expect, holdStream, test, type Page } from "./site";
import { RUNS, stepIn } from "./step-in";

// The Overlord, 2026-10-20 about 12:20Z, on the board on his phone: "when I
// hit the terminal button, it shouldn't just show up below the task panel. It
// should become the full screen below the header banner ... the text should
// be smaller, and it should fit the screen ... When you type and use the
// keyboard, it covers the terminal." And: "There's some buttons that aren't
// necessary on mobile, like the open terminal on the CFO header." On a small
// screen a goblin's panel opened from its card is the whole view under the
// top bar, its terminal drawn at the phone's font size and sized to that
// room, and held above the on-screen keyboard, which the test plays by
// shrinking the visual viewport as a phone's keyboard does. Back returns to
// the board.

// xterm draws in the DOM, where a test reads it, without WebGL.
test.use({ launchOptions: { args: ["--disable-webgl"] } });

const SESSION = "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34";
// Kip runs the harness of the run (tests/step-in.ts), the CFO Claude Code.
const snapshot = (harness: string) => ({
  healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, cfo_harness: "claude", cfo_terminal: "cfo-1", afk: { state: "off" },
  tasks: [{ id: "nw-invoice-export", title: "Export invoices as CSV", project: "northwind-api", goblin_name: "Kip", goblin_title: "Ledger Scout", phase: "working", verified: false, generation: "nw-invoice-export-1", harness, model: "claude-opus-5-5", effort: "xhigh", backend: "native", session: SESSION }],
  sessions: [
    { id: "cfo-1", role: "cfo", harness: "claude", host_id: "cfo-1", phase: "working" },
    { id: SESSION, native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "nw-invoice-export", role: "goblin", task_id: "nw-invoice-export", generation: "nw-invoice-export-1", harness, phase: "working" },
  ],
});

// A phone's keyboard, portrait: an iPhone's with its suggestion bar, and an
// Android phone's.
const PHONES = [
  { name: "an iPhone", viewport: { width: 390, height: 844 }, keyboard: 336 },
  { name: "a small Android phone", viewport: { width: 360, height: 800 }, keyboard: 320 },
] as const;

const FOOTER = "? for shortcuts";

// harnessScreen is a harness's full screen drawn at the size its terminal
// took: its transcript, its input line between two rules and its footer on
// the last row, the cursor on the input line.
function harnessScreen(cols: number, rows: number): string {
  const rule = "─".repeat(cols);
  const lines = ["● Exported 3 invoices to invoices.csv", "  ⎿  invoices.csv: 3 rows, 4 columns", "● Bash(npm test)", "  ⎿  42 passed"];
  const drawn = lines.slice(0, Math.max(0, rows - 4)).map((line, index) => `\x1b[${index + 1};1H${line.slice(0, cols)}`);
  return "\x1b[2J\x1b[H" + drawn.join("") + `\x1b[${rows - 3};1H${rule}\x1b[${rows - 2};1H❯ \x1b[${rows - 1};1H${rule}\x1b[${rows};1H  ${FOOTER}\x1b[${rows - 2};3H`;
}

interface Relay { resizes: { cols: number; rows: number }[]; say: (message: object) => void }

// open serves the board with a phone's visual viewport the test can shrink,
// and a terminal relay that takes each size the view asks for and redraws
// the harness's screen at it, as the real one does.
async function open(page: Page, run: typeof RUNS[number]): Promise<Relay> {
  await page.addInitScript((steppedIn) => localStorage.setItem("cfo-chat-style", steppedIn ? "modern" : "classic"), run.isSteppedIn);
  await page.addInitScript(() => {
    const real = window.visualViewport!;
    let keyboard = 0, top = 0;
    const viewport = new EventTarget();
    Object.defineProperties(viewport, {
      width: { get: () => real.width },
      height: { get: () => real.height - keyboard },
      offsetTop: { get: () => top },
      offsetLeft: { get: () => 0 },
      scale: { get: () => 1 },
    });
    Object.defineProperty(window, "visualViewport", { value: viewport, configurable: true });
    Object.assign(window, { keyboard: (height: number, offset = 0) => { keyboard = height; top = offset; viewport.dispatchEvent(new Event("resize")); } });
  });
  const relay: Relay = { resizes: [], say: () => {} };
  await holdStream(page, snapshot(run.harness));
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    if (new URL(socket.url()).searchParams.has("task")) relay.say = (message) => socket.send(JSON.stringify(message));
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.onMessage((data) => {
      if (typeof data !== "string") return;
      const message: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
      if (message.type !== "resize" || !message.cols || !message.rows) return;
      if (new URL(socket.url()).searchParams.has("task")) relay.resizes.push({ cols: message.cols, rows: message.rows });
      socket.send(JSON.stringify({ type: "size", cols: message.cols, rows: message.rows }));
      socket.send(Buffer.from(harnessScreen(message.cols, message.rows)));
    });
  });
  await page.goto("/");
  await expect(card(page)).toBeVisible();
  return relay;
}

const card = (page: Page) => page.locator(".task-board .task-card-shell").filter({ hasText: "Kip - Ledger Scout" }).locator(".task-card");
// openGoblin opens Kip's terminal from his card, stepping it in where the run
// does.
async function openGoblin(page: Page, relay: Relay, run: typeof RUNS[number]) {
  await card(page).click();
  if (run.isSteppedIn) await stepIn(page, (message) => relay.say(message));
}
const terminal = (page: Page) => page.getByRole("region", { name: "Goblin terminal" });
const keyboard = (page: Page, height: number, offset = 0) => page.evaluate(([height, offset]) => (window as unknown as { keyboard: (height: number, offset: number) => void }).keyboard(height, offset), [height, offset]);
const box = async (page: Page, selector: string) => (await page.locator(selector).boundingBox())!;

// settled waits until the view stops asking for sizes, as it measures its
// cell again once its font loads, and returns the size it asked for last.
async function settled(relay: Relay): Promise<{ cols: number; rows: number }> {
  let seen = -1;
  await expect.poll(() => {
    const isQuiet = relay.resizes.length > 0 && relay.resizes.length === seen;
    seen = relay.resizes.length;
    return isQuiet;
  }, { intervals: [400] }).toBe(true);
  return relay.resizes.at(-1)!;
}

// The rows xterm drew: the screen's last row holds the footer.
const drawnRows = (page: Page) => terminal(page).locator(".terminal-view:not(.staged) .xterm-rows > div");

for (const run of RUNS) for (const { name, viewport, keyboard: keyboardHeight } of PHONES) {
  test.describe(`on ${name}, ${viewport.width} by ${viewport.height}, ${run.name}`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true });

    test("a goblin's terminal fills the screen below the top bar, fits its width, and stays above the keyboard", async ({ page }, testInfo) => {
      // Arrange
      test.slow();
      const relay = await open(page, run);

      // Act
      await openGoblin(page, relay, run);
      await expect(terminal(page).locator(".xterm-rows")).toContainText(FOOTER);
      await page.evaluate(() => document.fonts.ready.then(() => undefined));
      const full = await settled(relay);
      await page.screenshot({ path: testInfo.outputPath("terminal.png") });

      // Assert: the panel is the whole view under the top bar, the board out
      // of sight, and the terminal at the phone's font, its grid as wide as
      // the panel and the size its program draws at.
      await expect(page.locator(".canvas-region")).toBeHidden();
      const [bar, pane, screen] = [await box(page, ".topbar"), await box(page, ".context-pane"), await box(page, ".terminal-view:not(.staged) .xterm-screen")];
      expect(pane.y).toBeGreaterThan(bar.y + bar.height);
      expect(pane.y - (bar.y + bar.height)).toBeLessThanOrEqual(12);
      expect(viewport.height - (pane.y + pane.height)).toBeLessThanOrEqual(12);
      expect(viewport.width - pane.width).toBeLessThanOrEqual(24);
      expect(screen.x).toBeGreaterThanOrEqual(pane.x);
      expect(screen.x + screen.width).toBeLessThanOrEqual(pane.x + pane.width);
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
      expect(await terminal(page).locator(".xterm-rows").evaluate((rows) => getComputedStyle(rows).fontSize)).toBe("12px");
      expect(full.cols).toBeGreaterThanOrEqual(viewport.width >= 390 ? 46 : 42);
      await expect(drawnRows(page)).toHaveCount(full.rows);
      await expect(drawnRows(page).last()).toContainText(FOOTER);

      // Act: the keyboard opens.
      await keyboard(page, keyboardHeight);
      await expect.poll(() => relay.resizes.at(-1)!.rows).toBeLessThan(full.rows);
      const typing = await settled(relay);
      await expect(drawnRows(page)).toHaveCount(typing.rows);
      await page.screenshot({ path: testInfo.outputPath("terminal-keyboard.png") });

      // Assert: the terminal has shrunk to the room above the keyboard, its
      // columns kept, and its input line and footer are above the keyboard.
      expect(typing.cols).toBe(full.cols);
      expect(typing.rows).toBeLessThan(full.rows);
      const above = viewport.height - keyboardHeight;
      expect((await box(page, ".context-pane")).y + (await box(page, ".context-pane")).height).toBeLessThanOrEqual(above);
      const footer = (await drawnRows(page).last().boundingBox())!;
      expect(footer.y + footer.height).toBeLessThanOrEqual(above);
      await expect(drawnRows(page).nth(typing.rows - 3)).toContainText("❯");

      // Act: Safari moves the visual viewport down the page to keep the
      // field it focused in sight.
      await keyboard(page, keyboardHeight, 120);

      // Assert: the view goes with it.
      await expect.poll(async () => (await box(page, ".app-shell")).y).toBe(120);

      // Act: the keyboard closes.
      await keyboard(page, 0);

      // Assert: the terminal takes its room back.
      await expect.poll(() => relay.resizes.at(-1)).toEqual(full);
      await expect(drawnRows(page)).toHaveCount(full.rows);
      await expect.poll(async () => (await box(page, ".app-shell")).y).toBe(0);
    });

    test("Back returns from the terminal to the board, at the card it was opened from", async ({ page }) => {
      // Arrange
      const relay = await open(page, run);
      await openGoblin(page, relay, run);
      await expect(terminal(page)).toBeVisible();

      // Act
      await page.getByRole("button", { name: "Back to Board", exact: true }).click();

      // Assert
      await expect(page.locator(".canvas-region")).toBeVisible();
      await expect(page.locator(".context-pane")).toBeHidden();
      await expect(card(page)).toBeFocused();
    });
  });
}

// A window on the PC means nothing on a phone, so there the CFO's panel has
// no Open in terminal; the desktop keeps it. The board is stacked there, so
// the panel takes half the window and its row holds every control.
for (const [size, viewport, isShown] of [["a phone", { width: 390, height: 844 }, false], ["the desktop", { width: 1440, height: 900 }, true]] as const) {
  test(`on ${size} the CFO's terminal ${isShown ? "keeps" : "has no"} Open in terminal`, async ({ page }) => {
    // Arrange
    await page.setViewportSize(viewport);
    await page.addInitScript(() => localStorage.setItem("cfo-board-layout", "stacked"));
    await open(page, RUNS[0]);

    // Act
    await page.getByRole("button", { name: "Open the CFO's terminal" }).first().click();

    // Assert: its Terminal view, which shows a Claude Code CFO's messages.
    await expect(page.locator("#panel-title")).toHaveText("CFO");
    await expect(page.getByRole("button", { name: "Open in Windows Terminal", exact: true })).toHaveCount(isShown ? 1 : 0);
  });
}
