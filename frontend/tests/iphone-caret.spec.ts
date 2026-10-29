import { devices, expect, holdStream, servePages, test, type Page } from "./site";

// The Overlord, 2026-10-29 about 03:40Z, on his iPhone: "also temrinal inlit
// doent scorlll to bottom the longer and longe rth wmessge goes", then "on
// mobile". With the keyboard up an iPhone 15 leaves the panel about 320 px,
// and the goblin's name block above the view took 190 of them, so a growing
// message's last line went under the keyboard: in the terminal view the
// screen kept a 200 px floor below the panel's foot, and Modern's box grew
// down past it. The panel's header is now its one row (2026-10-29), which
// stays with the keyboard up. The keyboard is played by shrinking the visual
// viewport as Safari's does. WebKit refuses the suite's own port, so the
// board is served at the private board's address, as
// tests/iphone-terminal.spec.ts does.
const PRIVATE = "https://pc.example.ts.net:4311";
const SESSION = "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34";
const snapshot = {
  healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, cfo_harness: "claude", cfo_terminal: "cfo-1", afk: { state: "off" },
  tasks: [{ id: "nw-invoice-export", title: "Export invoices as CSV", project: "northwind-api", goblin_name: "Kip", goblin_title: "Ledger Scout", phase: "working", verified: false, generation: "nw-invoice-export-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", session: SESSION }],
  sessions: [
    { id: "cfo-1", role: "cfo", harness: "claude", host_id: "cfo-1", phase: "working" },
    { id: SESSION, native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "nw-invoice-export", role: "goblin", task_id: "nw-invoice-export", generation: "nw-invoice-export-1", harness: "claude", phase: "working" },
  ],
};
// An iPhone's keyboard with its suggestion bar.
const KEYBOARD = 336;
const MESSAGE = "please export the invoices for every customer in the northwind tenant as one csv per month and ".repeat(2).trim().split(" ");

// harnessScreen is a harness's screen with its input at the foot, as Claude
// Code draws it: what it said, a rule, the input wrapped to the width, a rule
// and the footer, the cursor at the input's end.
function harnessScreen(cols: number, rows: number, input: string): string {
  const wrapped: string[] = [];
  const text = "> " + input;
  for (let at = 0; at < text.length || !wrapped.length; at += cols) wrapped.push(text.slice(at, at + cols));
  const rule = "-".repeat(cols);
  const foot = [rule, ...wrapped, rule, "  ? for shortcuts"];
  const said = Array.from({ length: Math.max(0, rows - foot.length) }, (_, index) => `Exported invoice batch ${index + 1}`);
  const lines = [...said, ...foot].slice(-rows);
  return "\x1b[2J\x1b[H" + lines.map((line, index) => `\x1b[${index + 1};1H${line}`).join("") + `\x1b[${lines.length - 2};${wrapped.at(-1)!.length + 1}H`;
}

test.use({ ...devices["iPhone 15"], browserName: "webkit" });

// open serves Kip's panel in a chat style with a keyboard the test can raise,
// and a terminal whose harness redraws its input as it is typed, and answers
// the sizes the view asks for, in the order it asked.
async function open(page: Page, style: "classic" | "modern"): Promise<{ cols: number; rows: number }[]> {
  const sizes: { cols: number; rows: number }[] = [];
  await page.addInitScript((chatStyle) => {
    localStorage.setItem("cfo-chat-style", chatStyle);
    localStorage.setItem("cfo-voice-hint-v1", "dismissed");
    const real = window.visualViewport!;
    let keyboard = 0;
    const viewport = new EventTarget();
    Object.defineProperties(viewport, {
      width: { get: () => real.width }, height: { get: () => real.height - keyboard },
      offsetTop: { get: () => 0 }, offsetLeft: { get: () => 0 }, scale: { get: () => 1 },
    });
    Object.defineProperty(window, "visualViewport", { value: viewport, configurable: true });
    Object.assign(window, { keyboard: (height: number) => { keyboard = height; viewport.dispatchEvent(new Event("resize")); } });
  }, style);
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    let input = "", size = { cols: 46, rows: 30 };
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.onMessage((data) => {
      if (typeof data === "string") {
        const control: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
        if (control.type !== "resize" || !control.cols || !control.rows) return;
        size = { cols: control.cols, rows: control.rows };
        sizes.push(size);
        socket.send(JSON.stringify({ type: "size", ...size }));
      } else input += Buffer.from(data).toString();
      socket.send(Buffer.from(harnessScreen(size.cols, size.rows, input)));
    });
  });
  await servePages(page.context(), PRIVATE);
  await page.goto(PRIVATE + "/");
  const board = page.getByRole("button", { name: "Board", exact: true });
  if (await board.isVisible()) await board.click();
  await page.locator(".task-board .task-card-shell").filter({ hasText: "Kip - Ledger Scout" }).locator(".task-card").click();
  return sizes;
}

const keyboard = (page: Page, height: number) => page.evaluate((height) => (window as unknown as { keyboard: (height: number) => void }).keyboard(height), height);

// settled waits until the terminal stops asking for sizes, as it refits to
// the room the keyboard left it.
async function settled(sizes: { cols: number; rows: number }[]) {
  let seen = -1;
  await expect.poll(() => {
    const isQuiet = sizes.length > 0 && sizes.length === seen;
    seen = sizes.length;
    return isQuiet;
  }, { intervals: [400] }).toBe(true);
}

// writing returns where the line being written ends, and the lowest point
// the page shows of it: the visual viewport's foot, or a clipping box's.
function writing(): { line: number; shown: number; isAtEnd: boolean } {
  const viewport = window.visualViewport!;
  const shownBelow = (element: Element) => {
    let shown = viewport.offsetTop + viewport.height;
    for (let parent = element.parentElement; parent; parent = parent.parentElement) {
      const style = getComputedStyle(parent);
      if (style.overflowX !== "visible" || style.overflowY !== "visible") shown = Math.min(shown, parent.getBoundingClientRect().bottom);
    }
    return shown;
  };
  const field = [...document.querySelectorAll<HTMLTextAreaElement>(".composer textarea")].find((element) => element.checkVisibility());
  if (field) {
    const box = field.getBoundingClientRect();
    const line = box.top + field.clientTop + field.scrollHeight - field.scrollTop - parseFloat(getComputedStyle(field).paddingBottom);
    return { line, shown: Math.min(shownBelow(field), box.bottom), isAtEnd: field.scrollTop + field.clientHeight >= field.scrollHeight - 1 };
  }
  const cursor = document.querySelector<HTMLElement>(".terminal-surface:not(.away) .terminal-view:not(.staged) .xterm-cursor")!;
  return { line: cursor.getBoundingClientRect().bottom, shown: shownBelow(cursor), isAtEnd: true };
}

for (const [view, style] of [["the terminal view", "classic"], ["Modern's box", "modern"]] as const) {
  test(`on an iPhone with the keyboard up, the line being written in ${view} stays in sight as the message grows`, async ({ page }, testInfo) => {
    // Arrange: WebKit draws the terminal again for every word.
    test.slow();
    const sizes = await open(page, style);
    const header = page.locator(".goblin-panel > .panel-header");
    if (style === "classic") {
      const terminal = page.getByRole("region", { name: "Goblin terminal" });
      await expect(terminal.locator(".xterm-rows")).toContainText("for shortcuts");
      await terminal.locator(".xterm").first().click();
    } else {
      await page.locator(".composer textarea:visible").click();
    }
    await expect(header).toBeVisible();

    // Act: the keyboard opens and the message grows past the room.
    await keyboard(page, KEYBOARD);
    await settled(sizes);
    const seen: { line: number; shown: number; isAtEnd: boolean }[] = [];
    for (const [index, word] of MESSAGE.entries()) {
      await page.keyboard.insertText(word + " ");
      if (index % 8 !== 7) continue;
      await expect.poll(() => page.evaluate(writing)).toMatchObject({ isAtEnd: true });
      seen.push(await page.evaluate(writing));
    }
    await page.screenshot({ path: testInfo.outputPath("writing.png") });

    // Assert: the line being written is above the keyboard and inside the
    // panel at every length, under the header's one row, which stays with
    // Back and the view switch.
    for (const { line, shown } of seen) expect(line).toBeLessThanOrEqual(shown + 1);
    await expect(header).toBeInViewport();
    await expect(header.getByRole("button", { name: "Back to Board", exact: true })).toBeInViewport();
    await expect(header.getByRole("group", { name: "Panel view" })).toBeInViewport();
  });
}
