import { devices, expect, test, type Page } from "./site";

// The Overlord, 2026-10-22, on his iPhone over the private board: "scroll in
// terminal on mobile remote doesnt work, it scrolls the screen". xterm reads
// the wheel and nothing of a finger, so a swipe over a terminal moved the page
// under it. A swipe now scrolls the terminal's history as the wheel does and
// leaves the page where it is. Chromium as a Pixel 7 takes real touches here,
// which move a page as a finger does, on a page with room to move.

// xterm draws in the DOM, where a test reads it, without WebGL.
test.use({ ...devices["Pixel 7"], launchOptions: { args: ["--disable-webgl"] } });

const PROMPT = "$ ";
const PRINTED = Array.from({ length: 300 }, (_, index) => "line " + (index + 1)).join("\r\n") + "\r\n" + PROMPT;
// The page scrolled to the terminal, with this much page above and below it.
const ROOM = 1500;

// swipe drags one finger down the terminal's screen, from 30% of its height
// to 80%, as reading back up its history does.
async function swipe(page: Page) {
  const box = (await page.locator(".terminal-surface:not(.away) .xterm-screen").first().boundingBox())!;
  const x = box.x + box.width / 2, from = box.y + box.height * 0.3, to = box.y + box.height * 0.8;
  const touch = await page.context().newCDPSession(page);
  await touch.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y: from }] });
  for (let step = 1; step <= 10; step++) await touch.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y: from + (to - from) * step / 10 }] });
  await touch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript((room) => document.addEventListener("DOMContentLoaded", () => {
    for (const end of ["prepend", "append"] as const) document.body[end](Object.assign(document.createElement("div"), { className: "room" }));
    const style = document.createElement("style");
    style.textContent = `.room { height: ${room}px; }`;
    document.head.append(style);
  }), ROOM);
});

// openNative opens a native terminal whose program printed 300 lines, in
// view, and returns its rows.
async function openNative(page: Page) {
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    let isPrinted = false;
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.onMessage((data) => {
      if (typeof data !== "string") return;
      const control: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
      if (control.type !== "resize") return;
      socket.send(JSON.stringify({ type: "size", cols: control.cols, rows: control.rows }));
      if (!isPrinted) { isPrinted = true; socket.send(Buffer.from(PRINTED)); }
    });
  });
  await page.goto("/tests/fixtures/terminal-input.html#native");
  const rows = page.locator(".terminal-view > .xterm .xterm-rows");
  await expect(rows).toContainText("line 300");
  await page.evaluate((room) => scrollTo(0, room), ROOM);
  return rows;
}

test("a swipe down a native terminal scrolls its history and not the page", async ({ page }) => {
  // Arrange
  const rows = await openNative(page);

  // Act
  await swipe(page);

  // Assert: the page is still, and the screen shows lines from further up.
  expect(await page.evaluate(() => scrollY)).toBe(ROOM);
  await expect(rows).not.toContainText("line 300");
});

test("after a swipe back through a native terminal's history a tap on Jump to bottom returns to the live end", async ({ page }) => {
  // Arrange: a program that printed 300 lines, read back by a swipe. On a
  // touch screen the terminal's text box lies over its whole screen, and
  // until 22 October 2026 it lay over the button too, which took no tap.
  const rows = await openNative(page);
  await swipe(page);
  await expect(rows).not.toContainText("line 300");

  // Act
  await page.getByRole("button", { name: "Jump to bottom" }).tap({ timeout: 5000 });

  // Assert
  await expect(rows).toContainText("line 300");
});

test("a swipe down a Herdr pane opens its history and not the page", async ({ page }) => {
  // Arrange: a pane whose history Herdr holds, its live screen in view.
  await page.addInitScript(() => {
    const originalFetch = window.fetch;
    window.fetch = (resource, options) => {
      if (resource !== "/api/terminal/stream") return originalFetch(resource, options);
      const body = JSON.parse(String(options?.body));
      const encoder = new TextEncoder();
      return Promise.resolve(new Response(new ReadableStream({ start(controller) {
        controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.ready", lease: body.control ? "sized" : "observed", identity: "proof" }) + "\n"));
        controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.frame", encoding: "ansi", seq: 1, full: true, width: 40, height: 20, bytes: btoa("\x1b[2J\x1b[H$ ") }) + "\n"));
        options?.signal?.addEventListener("abort", () => controller.close(), { once: true });
      } })));
    };
  });
  await page.route("**/api/terminal/input", async (route) => route.fulfill({ json: { seq: route.request().postDataJSON().seq } }));
  await page.route("**/api/terminal/history", (route) => route.fulfill({ json: { text: Array.from({ length: 300 }, (_, index) => "line " + (index + 1)).join("\n") + "\n$", agent: "codex" } }));
  await page.goto("/tests/fixtures/terminal-input.html#herdr");
  await expect(page.getByText("Connecting", { exact: true })).toHaveCount(0);
  await page.evaluate((room) => scrollTo(0, room), ROOM);

  // Act
  await swipe(page);

  // Assert: the page is still, and the history is open over the live screen.
  expect(await page.evaluate(() => scrollY)).toBe(ROOM);
  await expect(page.locator(".terminal-history:not(.away) .xterm-rows")).toContainText("line ");
  await expect(page.getByText("History: scroll down or press Esc for live")).toBeVisible();
});
