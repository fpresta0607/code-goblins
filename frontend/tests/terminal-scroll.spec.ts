import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-08: "terminals in command center should have sticky
// input line just like cfo with scroll and jump to bottom". Reading a
// terminal's history keeps the line he types on pinned at the bottom of the
// panel, with Jump to bottom above it, as Claude Code's full-screen interface
// does for the CFO's terminal by itself.

// xterm draws in the DOM, where a test reads it, without WebGL.
test.use({ launchOptions: { args: ["--disable-webgl"] } });

const PROMPT = "Your fly.io email:";
// A program that printed 500 lines and waits for input on the line under them.
const PRINTED = Array.from({ length: 500 }, (_, index) => "line " + (index + 1)).join("\r\n") + "\r\n" + PROMPT + " ";

interface Relay { typed: string[]; send: (...messages: (string | Buffer)[]) => void }

// openTerminal stands in for the board's relay: the terminal takes each size
// the view claims, and after the first prints output.
async function openTerminal(page: Page, output: string): Promise<Relay> {
  const relay: Relay = { typed: [], send: () => {} };
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    let isPrinted = false;
    relay.send = (...messages) => { for (const message of messages) socket.send(message); };
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.onMessage((data) => {
      if (typeof data !== "string") { relay.typed.push(data.toString("utf8")); return; }
      const control: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
      if (control.type !== "resize") return;
      socket.send(JSON.stringify({ type: "size", cols: control.cols, rows: control.rows }));
      if (!isPrinted) { isPrinted = true; socket.send(Buffer.from(output)); }
    });
  });
  await page.goto("/tests/fixtures/terminal-geometry.html");
  await expect(page.locator(".terminal-view.staged")).toHaveCount(0);
  await expect(screenRows(page)).toContainText(PROMPT);
  return relay;
}

const screenRows = (page: Page) => page.locator(".terminal-view > .xterm .xterm-rows");
const pin = (page: Page) => page.locator(".terminal-pin");
const jump = (page: Page) => page.getByRole("button", { name: "Jump to bottom" });

async function readHistory(page: Page) {
  await page.locator(".terminal-view > .xterm .xterm-screen").hover();
  await page.mouse.wheel(0, -2400);
  await expect(pin(page)).toBeVisible();
}

test("reading the history keeps the line he types on pinned at the bottom, and new output leaves him where he reads", async ({ page }) => {
  // Arrange
  const relay = await openTerminal(page, PRINTED);

  // Act
  await readHistory(page);

  // Assert: the history scrolled, the prompt is pinned over the last row
  // with Jump to bottom above it, and nothing else is said.
  await expect(screenRows(page)).not.toContainText(PROMPT);
  await expect(pin(page)).toContainText(PROMPT);
  await expect(jump(page)).toBeVisible();
  const screen = (await page.locator(".terminal-view > .xterm .xterm-screen").boundingBox())!;
  const pinned = (await pin(page).boundingBox())!;
  expect(Math.abs(pinned.y + pinned.height - (screen.y + screen.height))).toBeLessThan(2);
  expect(Math.abs(pinned.x - screen.x)).toBeLessThan(2);
  const button = (await jump(page).boundingBox())!;
  expect(button.y + button.height).toBeLessThanOrEqual(pinned.y);
  await expect(page.locator(".host-terminal")).not.toContainText("History");

  // Act: the program prints more while he reads.
  const top = await screenRows(page).locator("> div").first().textContent();
  relay.send(Buffer.from("\r\nstill waiting\r\n" + PROMPT + " "));
  await expect(pin(page)).toContainText(PROMPT);
  await page.waitForTimeout(300);

  // Assert
  await expect(screenRows(page).locator("> div").first()).toHaveText(top!);
  await expect(jump(page)).toBeVisible();
});

test("typing while he reads the history types on the line and returns to the live end", async ({ page }) => {
  // Arrange
  const relay = await openTerminal(page, PRINTED);
  await readHistory(page);

  // Act
  await page.keyboard.type("me");

  // Assert
  await expect.poll(() => relay.typed.join("")).toBe("me");
  await expect(pin(page)).toBeHidden();
  await expect(jump(page)).toHaveCount(0);
  await expect(screenRows(page)).toContainText(PROMPT);
});

test("Jump to bottom returns to the live end with the keyboard in the terminal", async ({ page }) => {
  // Arrange
  await openTerminal(page, PRINTED);
  await readHistory(page);

  // Act
  await jump(page).click();

  // Assert
  await expect(pin(page)).toBeHidden();
  await expect(jump(page)).toHaveCount(0);
  await expect(screenRows(page)).toContainText(PROMPT);
  await expect(screenRows(page).locator("> div").last()).not.toHaveText("");
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeFocused();
});

test("a full-screen program, as the CFO's Claude Code runs, keeps its own scrolling", async ({ page }) => {
  // Arrange: the program draws on the alternate screen and takes the mouse.
  const relay = await openTerminal(page, "\x1b[?1049h\x1b[?1000h\x1b[?1006h" + PRINTED);

  // Act
  await page.locator(".terminal-view > .xterm .xterm-screen").hover();
  await page.mouse.wheel(0, -400);

  // Assert: the wheel reached the program, and the board pinned nothing.
  await expect.poll(() => relay.typed.join("")).toContain("\x1b[<64;");
  await expect(pin(page)).toBeHidden();
  await expect(jump(page)).toHaveCount(0);
});
