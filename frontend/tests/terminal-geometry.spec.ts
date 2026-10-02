import { expect, test, type Page } from "./site";

// relay stands in for the board's relay: it records what the view sends and
// sends the view the terminal's messages.
interface Relay {
  typed: string[];
  resizes: { cols: number; rows: number }[];
  send: (...messages: (string | Buffer)[]) => void;
}

// The terminal's size when the view connects, as its history starts.
const HOST_SIZE = { cols: 60, rows: 20 };

async function openTerminal(page: Page): Promise<Relay> {
  const relay: Relay = { typed: [], resizes: [], send: () => {} };
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    relay.send = (...messages) => { for (const message of messages) socket.send(message); };
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(JSON.stringify({ type: "size", ...HOST_SIZE }));
    socket.onMessage((data) => {
      if (typeof data !== "string") { relay.typed.push(data.toString("utf8")); return; }
      const message: unknown = JSON.parse(data);
      if (typeof message === "object" && message !== null && "type" in message && message.type === "resize" && "cols" in message && "rows" in message) {
        relay.resizes.push({ cols: Number(message.cols), rows: Number(message.rows) });
      }
    });
  });
  await page.goto("/tests/fixtures/terminal-geometry.html");
  await expect.poll(() => relay.resizes.length).toBeGreaterThan(0);
  await settled(relay);
  return relay;
}

// settled waits until the view stops claiming sizes, as it measures its cell
// again once its font loads, and returns the size it claimed last.
async function settled(relay: Relay): Promise<{ cols: number; rows: number }> {
  let seen = -1;
  await expect.poll(() => {
    const isQuiet = relay.resizes.length === seen;
    seen = relay.resizes.length;
    return isQuiet;
  }, { intervals: [500] }).toBe(true);
  return relay.resizes.at(-1)!;
}

// widthProbe moves the cursor as far right as the grid goes and asks where it
// is; xterm answers as typed input, so each answer is the width of the grid
// that output was drawn on.
const widthProbe = () => Buffer.from("\x1b[999C\x1b[6n");
const size = (cols: number, rows: number) => JSON.stringify({ type: "size", cols, rows });

// Each answer is ESC [ row ; column R.
function probedWidths(relay: Relay): number[] {
  return relay.typed.join("").split("\x1b[").flatMap((part) => /^\d+;(\d+)R/.exec(part)?.[1] ?? []).map(Number);
}

test("output before a size is drawn at the old size and output after it at the new one", async ({ page }) => {
  const relay = await openTerminal(page);
  const claimed = relay.resizes.at(-1)!;
  expect(claimed.cols).not.toBe(HOST_SIZE.cols);

  relay.send(widthProbe(), size(claimed.cols, claimed.rows), widthProbe());

  await expect.poll(() => probedWidths(relay)).toEqual([HOST_SIZE.cols, claimed.cols]);
});

test("a resized panel changes the grid only when the terminal takes its size", async ({ page }) => {
  const relay = await openTerminal(page);
  const claimed = relay.resizes.at(-1)!;
  relay.send(size(claimed.cols, claimed.rows), widthProbe());
  await expect.poll(() => probedWidths(relay)).toEqual([claimed.cols]);

  const viewport = page.viewportSize()!;
  await page.setViewportSize({ width: Math.round(viewport.width * 0.6), height: viewport.height });
  await expect.poll(() => relay.resizes.at(-1)!.cols).toBeLessThan(claimed.cols);
  const smaller = relay.resizes.at(-1)!;
  relay.send(widthProbe());
  await expect.poll(() => probedWidths(relay)).toEqual([claimed.cols, claimed.cols]);

  relay.send(size(smaller.cols, smaller.rows), widthProbe());
  await expect.poll(() => probedWidths(relay)).toEqual([claimed.cols, claimed.cols, smaller.cols]);
});

// A view that reconnects replays a history whose first size is the one it
// claims; that is not the terminal taking its claim, so live output before
// the terminal takes it leaves the view out of sight until its fallback.
test("a size replayed from the history does not show a view before its repaint", async ({ page }) => {
  const relay: Relay = { typed: [], resizes: [], send: () => {} };
  let connections = 0;
  let drop = () => {};
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    const connection = ++connections;
    let isReplayed = false;
    if (connection === 1) {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(JSON.stringify({ type: "size", ...HOST_SIZE }));
      drop = () => socket.close({ code: 1001 });
    }
    socket.onMessage((data) => {
      if (typeof data !== "string") { relay.typed.push(data.toString("utf8")); return; }
      const message: unknown = JSON.parse(data);
      if (typeof message !== "object" || message === null || !("type" in message) || message.type !== "resize" || !("cols" in message) || !("rows" in message)) return;
      const claim = { cols: Number(message.cols), rows: Number(message.rows) };
      relay.resizes.push(claim);
      if (connection === 2 && !isReplayed) {
        isReplayed = true;
        const history = Buffer.from("hist!");
        socket.send(JSON.stringify({ type: "history", bytes: history.length }));
        socket.send(size(claim.cols, claim.rows));
        socket.send(history);
        socket.send(widthProbe());
      }
    });
  });
  await page.goto("/tests/fixtures/terminal-geometry.html");
  await expect.poll(() => relay.resizes.length).toBeGreaterThan(0);
  await settled(relay);
  await expect(page.locator(".terminal-view.staged")).toHaveCount(0);
  const shownAfter = page.evaluate(() => new Promise<number>((resolve) => {
    const container = document.querySelector(".terminal-view")!.parentElement!;
    let stagedAt = -1;
    new MutationObserver(() => {
      const isStaged = container.querySelector(".terminal-view.staged") !== null;
      if (isStaged && stagedAt < 0) stagedAt = performance.now();
      if (!isStaged && stagedAt >= 0) resolve(performance.now() - stagedAt);
    }).observe(container, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] });
  }));

  drop();

  await expect.poll(() => probedWidths(relay), { intervals: [10] }).toHaveLength(1);
  expect(await shownAfter, "the view was shown before the terminal took its claim").toBeGreaterThanOrEqual(600);
});

// The probe's answer arrives as typing, so this test does not probe after the
// other window's size.
test("typing takes the terminal back from a size another window gave it", async ({ page }) => {
  const relay = await openTerminal(page);
  const claimed = relay.resizes.at(-1)!;
  relay.send(size(claimed.cols, claimed.rows));

  relay.send(size(90, 30));
  const resizes = relay.resizes.length;
  await page.waitForTimeout(500);
  expect(relay.resizes.slice(resizes), "the view claimed the terminal without being typed into").toEqual([]);
  await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
  await page.keyboard.type("x");

  await expect.poll(() => relay.typed.join("")).toContain("x");
  await expect.poll(() => relay.resizes.slice(resizes)).toContainEqual(claimed);
});
