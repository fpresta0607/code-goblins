import { expect, holdStream, test, type Page } from "./site";

// The Overlord, 2026-11-25 about 17:00Z: "terminal reconnects or loads scroll
// tirelessly through history before reaching bottom. How do we make it faster
// and smoother on classic" (#2903). A long session's terminal opens and
// reconnects in the classic chat style already at its bottom: no frame he
// sees shows the history it replays on its way there, and the history stays
// there to scroll back to. The supervisor's relay is played here as it
// sends: the history's length first, then its bytes a window at a time, and
// the size the view claims only after the bytes already queued before it.
test.use({ chatStyle: "classic", launchOptions: { args: ["--disable-webgl"] } });

const SESSION = "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34";
const SNAPSHOT = {
  healthy: true, instance: "fixture", revision: 1, attention: [], ceo_runs: true, afk: { state: "off" },
  tasks: [{ id: "nw-invoice-export", title: "Export invoices as CSV", project: "northwind-api", phase: "working", verified: false, generation: "nw-invoice-export-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", session: SESSION }],
  sessions: [{ id: SESSION, native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "nw-invoice-export", role: "goblin", task_id: "nw-invoice-export", generation: "nw-invoice-export-1", harness: "claude", phase: "working" }],
};

const ESC = "\x1b";
const PROMPT = "READY> ";
// A long session's history, about 2 MB of coloured output as a harness
// writes it, and the prompt it waits at under it.
const HISTORY = Array.from({ length: 30_000 }, (_, index) => `${ESC}[38;5;${index % 200}mhistory line ${index + 1}${ESC}[0m ` + "-".repeat(40)).join("\r\n") + "\r\n" + PROMPT;
// The relay's window of unacknowledged output and how long one window takes
// to arrive on a loaded machine.
const WINDOW = 32 * 1024;
const WINDOW_MS = 12;

// A frame drawn while the terminal was in sight: when, which of the views
// the board made it was, counting from 1 in the order they appeared, and
// whether it showed the bottom, where the prompt is.
interface Frame { at: number; view: number; isBottom: boolean; text: string }

// replay answers each connection of the view as the relay does, and says
// when the connection opened.
async function replay(page: Page): Promise<{ opened: number[]; close: (code: number, reason: string) => void }> {
  const opened: number[] = [];
  let closeLast: (code: number, reason: string) => void = () => {};
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    opened.push(Date.now());
    closeLast = (code, reason) => void socket.close({ code, reason });
    socket.send(JSON.stringify({ type: "history", bytes: Buffer.byteLength(HISTORY) }));
    const bytes = Buffer.from(HISTORY);
    let sent = 0;
    let claimed: { cols: number; rows: number } | null = null;
    let isAnswered = false;
    const answer = () => {
      if (!claimed || sent < bytes.length || isAnswered) return;
      isAnswered = true;
      socket.send(JSON.stringify({ type: "size", ...claimed }));
      socket.send(Buffer.from("\r" + ESC + "[K" + PROMPT));
    };
    const next = () => {
      if (sent >= bytes.length) { answer(); return; }
      socket.send(bytes.subarray(sent, sent + WINDOW));
      sent += WINDOW;
      setTimeout(next, WINDOW_MS);
    };
    next();
    socket.onMessage((data) => {
      if (typeof data !== "string") return;
      const control: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
      if (control.type !== "resize") return;
      claimed = { cols: control.cols!, rows: control.rows! };
      answer();
    });
  });
  return { opened, close: (code, reason) => closeLast(code, reason) };
}

// watchFrames records every frame in which the terminal is in sight.
async function watchFrames(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const frames: { at: number; view: number; isBottom: boolean; text: string }[] = [];
    const views = new WeakMap<Element, number>();
    let made = 0;
    (window as unknown as { frames_: typeof frames }).frames_ = frames;
    const look = () => {
      for (const each of document.querySelectorAll(".host-terminal .terminal-view")) if (!views.has(each)) views.set(each, ++made);
      const view = document.querySelector(".host-terminal .terminal-view:not(.staged)");
      const rows = view?.querySelector(":scope > .xterm .xterm-rows");
      if (rows && view instanceof HTMLElement && view.offsetParent !== null) {
        const text = rows.textContent ?? "";
        frames.push({ at: Date.now(), view: views.get(view)!, isBottom: text.includes("READY>"), text: text.slice(0, 120) });
      }
      requestAnimationFrame(look);
    };
    requestAnimationFrame(look);
  });
}

const framesOf = (page: Page) => page.evaluate(() => (window as unknown as { frames_: Frame[] }).frames_);

async function openTerminal(page: Page): Promise<{ opened: number[]; close: (code: number, reason: string) => void }> {
  await watchFrames(page);
  await holdStream(page, SNAPSHOT);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  const relay = await replay(page);
  await page.goto("/");
  const board = page.getByRole("button", { name: "Board", exact: true });
  if (await board.isVisible()) await board.click();
  await page.locator(".task-board .task-card").filter({ hasText: "Export invoices as CSV" }).click();
  return relay;
}

// shownAtBottom waits for the terminal to show its bottom and says how long
// that took from when its connection opened.
async function shownAtBottom(page: Page, opened: number): Promise<number> {
  await expect(page.locator(".host-terminal .terminal-view:not(.staged) > .xterm .xterm-rows")).toContainText(PROMPT.trim(), { timeout: 30_000 });
  const frames = await framesOf(page);
  const first = frames.find((frame) => frame.at >= opened && frame.isBottom);
  return first ? first.at - opened : NaN;
}

test("a long session's terminal opens in the classic style already at its bottom, with no replay on screen", async ({ page }) => {
  // Arrange
  const relay = await openTerminal(page);

  // Act
  const took = await shownAtBottom(page, relay.opened[0]);

  // Assert: every frame he saw showed the bottom, and the history is there.
  const frames = await framesOf(page);
  const replayed = frames.filter((frame) => !frame.isBottom);
  test.info().annotations.push({ type: "open to bottom", description: `${took} ms` });
  expect(replayed, "frames that showed the history on the way to the bottom").toEqual([]);
  // He scrolls back a page with Shift+PageUp, the terminal's own key, and
  // finds the history above the last screen kept.
  const rows = page.locator(".host-terminal .terminal-view:not(.staged) > .xterm .xterm-rows");
  await page.locator(".host-terminal .terminal-view:not(.staged) > .xterm .xterm-screen").click();
  await page.keyboard.press("Shift+PageUp");
  await expect(rows).not.toContainText(PROMPT.trim());
  await expect(rows).not.toContainText("history line 30000");
  await expect(rows, "a line from above the last screen").toContainText(/history line (29[0-8]\d\d|299[0-7]\d)\b/);
});

test("a long session's terminal reconnects in the classic style at its bottom, with no replay on screen", async ({ page }) => {
  // Arrange
  const relay = await openTerminal(page);
  await shownAtBottom(page, relay.opened[0]);
  await page.evaluate(() => { (window as unknown as { frames_: Frame[] }).frames_.length = 0; });

  // Act: the relay drops the view, which reconnects by itself.
  relay.close(1013, "The view fell behind the terminal's output.");
  await expect.poll(() => relay.opened.length).toBe(2);
  await expect(page.getByRole("status").filter({ hasText: "Reconnecting" })).toHaveCount(0, { timeout: 30_000 });
  await expect(page.locator(".host-terminal .terminal-view")).toHaveCount(1);
  await expect(page.locator(".host-terminal .terminal-view > .xterm .xterm-rows")).toContainText(PROMPT.trim(), { timeout: 30_000 });

  // Assert
  const frames = await framesOf(page);
  const newest = Math.max(...frames.map((frame) => frame.view));
  const reconnected = frames.find((frame) => frame.view === newest && frame.isBottom);
  test.info().annotations.push({ type: "reconnect to bottom", description: `${reconnected ? reconnected.at - relay.opened[1] : NaN} ms` });
  expect(frames.filter((frame) => !frame.isBottom), "frames that showed the history on the way to the bottom").toEqual([]);
});
