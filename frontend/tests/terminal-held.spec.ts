import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-22: "every time I open up some of my goblins in the
// task panel it always says reconnect even though it's connected". A goblin a
// review gate owned showed only Reconnect, with no screen and no words: the
// supervisor closed its view on the first input, and xterm sends some by
// itself as a view opens (#1304). The supervisor now keeps the view open and
// says typing is held; the view shows the live screen with that notice, and a
// view that truly cannot connect says why beside Reconnect.

// xterm draws in the DOM, where a test reads it, without WebGL.
test.use({ launchOptions: { args: ["--disable-webgl"] } });

const SCREEN = "\x1b[2J\x1b[HGate review running\r\n$ ";
const NOTICE = "Typing is paused.";

// open plays the terminal's relay with play, which is handed each socket.
async function open(page: Page, play: (socket: { send: (message: string | Buffer) => void; close: (code: number, reason: string) => void }) => void) {
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => play({ send: (message) => socket.send(message), close: (code, reason) => socket.close({ code, reason }) }));
  await page.goto("/tests/fixtures/terminal-input.html#native");
}

test("a view whose typing a gate holds shows the live screen and says so, with no Reconnect", async ({ page }) => {
  // Arrange
  let send: (message: string | Buffer) => void = () => {};
  await open(page, (socket) => {
    send = socket.send;
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(JSON.stringify({ type: "held", reason: "pipeline owns this task; use cfo pipeline respond or inspect its delivery evidence" }));
    socket.send(Buffer.from(SCREEN));
  });
  const terminal = page.getByRole("region", { name: "Goblin terminal" });

  // Act: the program goes on writing while the gate holds its typing.
  await expect(terminal.locator(".xterm-rows")).toContainText("Gate review running");
  send(Buffer.from("step 2 of 5"));

  // Assert
  await expect(terminal.getByRole("status").filter({ hasText: NOTICE })).toBeVisible();
  await expect(terminal.locator(".xterm-rows")).toContainText("step 2 of 5");
  await expect(terminal.getByRole("button", { name: "Reconnect" })).toHaveCount(0);
});

test("the notice goes once the gate lets typing go", async ({ page }) => {
  // Arrange
  let send: (message: string | Buffer) => void = () => {};
  await open(page, (socket) => {
    send = socket.send;
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(JSON.stringify({ type: "held", reason: "pipeline custody has not been returned; use cfo pipeline recover" }));
    socket.send(Buffer.from(SCREEN));
  });
  const terminal = page.getByRole("region", { name: "Goblin terminal" });
  await expect(terminal.getByText(NOTICE)).toBeVisible();

  // Act
  send(JSON.stringify({ type: "held", reason: "" }));

  // Assert
  await expect(terminal.getByText(NOTICE)).toHaveCount(0);
});

test("a view the relay refuses says why beside Reconnect", async ({ page }) => {
  // Arrange
  await open(page, (socket) => socket.close(1008, "Values are typed only on the board's own page on this PC."));
  const terminal = page.getByRole("region", { name: "Goblin terminal" });

  // Assert: the reason and the button, where there was the button alone.
  await expect(terminal.getByText("Values are typed only on the board's own page on this PC.")).toBeVisible();
  await expect(terminal.getByRole("button", { name: "Reconnect" })).toBeVisible();
});

test("a host that does not answer is tried again by itself, and the view connects without Reconnect", async ({ page }) => {
  // Arrange: the host answers on the third try.
  let tries = 0;
  await open(page, (socket) => {
    if (++tries < 3) { socket.close(1013, "The terminal of this task did not answer."); return; }
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from(SCREEN));
  });
  const terminal = page.getByRole("region", { name: "Goblin terminal" });

  // Assert
  await expect(terminal.locator(".xterm-rows")).toContainText("Gate review running", { timeout: 15_000 });
  await expect(terminal.getByRole("button", { name: "Reconnect" })).toHaveCount(0);
  expect(tries).toBe(3);
});
