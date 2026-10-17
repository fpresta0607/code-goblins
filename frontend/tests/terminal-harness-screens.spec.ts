import { readFileSync } from "node:fs";
import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-17: "when you scroll on like Pi and Codex terminals, it
// um it doesn't keep like the bottom banner", "Codex terminals have like that
// when it's not a long message, it's that the input line is at the top", and
// keys that did not reach a permission menu or a list. Every harness now
// draws on its own full screen, Codex and pi as Claude Code does, so each
// keeps its input line and footer on its bottom rows and scrolls its own
// transcript under the wheel. These are each harness's own screens, recorded
// from real Claude Code, Codex and pi in native terminals at the panel's size
// in his window, replayed into the board's terminal.

// xterm draws in the DOM, where a test reads it, without WebGL.
test.use({ launchOptions: { args: ["--disable-webgl"] }, viewport: { width: 1707, height: 912 }, deviceScaleFactor: 1.5 });

const ESC = "\x1b";

interface Recording { recorded: string; cols: number; rows: number; output: string }

const recording = (name: string): Recording => JSON.parse(readFileSync(new URL("./fixtures/harness-screens/" + name + ".json", import.meta.url), "utf8"));

// Each harness at its input line, and on a screen of choices it asks him to
// move through: Claude Code's permission prompt, Codex's and pi's model lists.
const HARNESSES = [
  { name: "Claude Code", harness: "claude", idle: "claude-idle", choices: "claude-permission", inputLine: "❯", footer: "manual mode on", asks: "Do you want to create" },
  { name: "Codex", harness: "codex", idle: "codex-idle", choices: "codex-list", inputLine: "› Ask Codex to do anything", footer: "? for shortcuts", asks: "Select Model and Effort" },
  { name: "pi", harness: "pi", idle: "pi-idle", choices: "pi-list", inputLine: "", footer: "0.8%/1.0M (auto)", asks: "Only showing models from configured providers" },
];

// The keys his footers and lists name, Enter, Escape, Tab and Shift+Tab, the
// arrows and Ctrl chords among them, each as a plain terminal sends it.
const KEYS: [key: string, sent: string][] = [
  ["ArrowDown", ESC + "[B"], ["ArrowUp", ESC + "[A"], ["ArrowRight", ESC + "[C"], ["ArrowLeft", ESC + "[D"],
  ["Enter", "\r"], ["Escape", ESC], ["Tab", "\t"], ["Shift+Tab", ESC + "[Z"], ["Space", " "],
  ["Home", ESC + "[H"], ["End", ESC + "[F"], ["PageUp", ESC + "[5~"], ["PageDown", ESC + "[6~"],
  ["1", "1"], ["2", "2"], ["y", "y"], ["n", "n"], ["r", "r"],
  ["Control+c", "\x03"], ["Control+d", "\x04"], ["Control+l", "\x0c"], ["Control+o", "\x0f"], ["Control+r", "\x12"], ["Control+s", "\x13"], ["Control+t", "\x14"], ["Control+u", "\x15"],
  ["Control+End", ESC + "[1;5F"], ["Alt+p", ESC + "p"], ["Alt+Enter", ESC + "\r"],
];

// The line break of a harness's composer is the one key sent per harness.
const SHIFT_ENTER: Record<string, string> = { claude: "\n", codex: ESC + "[74;36;10;1;8;1_" + ESC + "[74;36;10;0;8;1_", pi: "\r" };

interface Relay { typed: () => string; clear: () => void; write: (text: string) => void }

// replay stands in for the board's relay: the terminal takes the size the
// harness drew at and sends its screen, as the terminal's repaint does.
async function replay(page: Page, screen: Recording): Promise<Relay> {
  const typed: Buffer[] = [];
  let write: (text: string) => void = () => {};
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    let isDrawn = false;
    write = (text) => socket.send(Buffer.from(text));
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.onMessage((data) => {
      if (typeof data !== "string") { typed.push(data); return; }
      if (JSON.parse(data).type !== "resize" || isDrawn) return;
      isDrawn = true;
      socket.send(JSON.stringify({ type: "size", cols: screen.cols, rows: screen.rows }));
      socket.send(Buffer.from(screen.output));
    });
  });
  return { typed: () => Buffer.concat(typed).toString("latin1"), clear: () => typed.splice(0), write: (text) => write(text) };
}

const rows = (page: Page) => page.locator(".terminal-view:not(.staged) > .xterm .xterm-rows > div");
const pin = (page: Page) => page.locator(".terminal-pin");
const jump = (page: Page) => page.getByRole("button", { name: "Jump to bottom" });

// lastRowWith is the index of the screen's last row that holds text.
async function lastRowWith(page: Page, text: string): Promise<number> {
  const texts = await rows(page).allTextContents();
  return texts.map((row) => row.includes(text)).lastIndexOf(true);
}

for (const { name, harness, idle, choices, inputLine, footer, asks } of HARNESSES) {
  test(name + " keeps its input line and footer on its bottom rows, and the wheel scrolls " + name + ", never the board", async ({ page }) => {
    // Arrange
    const screen = recording(idle);
    const relay = await replay(page, screen);
    await page.goto("/tests/fixtures/terminal-geometry.html");
    await expect(rows(page)).toHaveCount(screen.rows);
    await expect(rows(page).filter({ hasText: footer })).toHaveCount(1);
    const before = await rows(page).allTextContents();

    // Assert: the footer is on the last two rows, and the input line, drawn
    // between rules for pi, just above it.
    expect(await lastRowWith(page, footer)).toBeGreaterThanOrEqual(screen.rows - 2);
    if (inputLine) expect(await lastRowWith(page, inputLine)).toBeGreaterThanOrEqual(screen.rows - 5);

    // Act
    await page.locator(".terminal-view:not(.staged) .xterm-screen").hover();
    relay.clear();
    await page.mouse.wheel(0, -400);

    // Assert: the wheel went to the harness as mouse wheel reports, which is
    // what scrolls its transcript, and the board scrolled and pinned nothing.
    await expect.poll(() => relay.typed()).toContain(ESC + "[<64;");
    await page.waitForTimeout(300);
    await expect(pin(page)).toBeHidden();
    await expect(jump(page)).toHaveCount(0);
    expect(await rows(page).allTextContents()).toEqual(before);
  });

  test("a drag in " + name + " goes to " + name + ", which selects and copies, and Shift and a drag selects in the board's terminal", async ({ page, context }) => {
    // Arrange
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    const relay = await replay(page, recording(idle));
    await page.goto("/tests/fixtures/terminal-geometry.html");
    await expect(rows(page).filter({ hasText: footer })).toHaveCount(1);
    const box = (await page.locator(".terminal-view:not(.staged) .xterm-screen").boundingBox())!;
    relay.clear();

    // Act: a plain drag across the screen.
    await page.mouse.move(box.x + 40, box.y + 20);
    await page.mouse.down();
    await page.mouse.move(box.x + 400, box.y + 80, { steps: 4 });
    await page.mouse.up();

    // Assert: the harness was sent the press, the motion and the release.
    await expect.poll(() => relay.typed()).toContain(ESC + "[<0;");
    expect(relay.typed()).toContain(ESC + "[<32;");
    expect(relay.typed()).toMatch(new RegExp(ESC + String.raw`\[<0;\d+;\d+m`));
    await expect(page.getByRole("status").filter({ hasText: "Copied" })).toHaveCount(0);

    // Act: the same drag with Shift held.
    relay.clear();
    await page.keyboard.down("Shift");
    await page.mouse.move(box.x + 40, box.y + 20);
    await page.mouse.down();
    await page.mouse.move(box.x + 400, box.y + 80, { steps: 4 });
    await page.mouse.up();
    await page.keyboard.up("Shift");

    // Assert: the board's terminal selected and copied it, and the harness
    // was sent no press of the button.
    await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
    expect(relay.typed()).not.toContain(ESC + "[<0;");
  });

  for (const role of ["goblin", "cfo"]) {
    test("every key reaches " + name + "'s " + (harness === "claude" ? "permission prompt" : "model list") + " in the " + role + "'s terminal as a plain terminal sends it", async ({ page }) => {
      // Arrange
      const relay = await replay(page, recording(choices));
      await page.goto("/tests/fixtures/terminal-input.html?" + new URLSearchParams({ harness, role }) + "#native");
      await expect(page.locator(".terminal-view:not(.staged) > .xterm .xterm-rows")).toContainText(asks);
      await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
      // The focus report the harness asked for, then nothing until a key.
      relay.write(ESC + "[5n");
      await expect.poll(() => relay.typed()).toContain(ESC + "[0n");
      relay.clear();

      // Act
      const sent: Record<string, string> = {};
      for (const [key] of KEYS) {
        const before = relay.typed().length;
        await page.keyboard.press(key);
        await expect.poll(() => relay.typed().length, { message: key + " sent nothing" }).toBeGreaterThan(before);
        sent[key] = JSON.stringify(relay.typed().slice(before));
      }
      const shiftEnter = relay.typed().length;
      await page.keyboard.press("Shift+Enter");
      await expect.poll(() => relay.typed().length, { message: "Shift+Enter sent nothing" }).toBeGreaterThan(shiftEnter);
      sent["Shift+Enter"] = JSON.stringify(relay.typed().slice(shiftEnter));

      // Assert
      expect(sent).toEqual(Object.fromEntries([...KEYS, ["Shift+Enter", SHIFT_ENTER[harness]]].map(([key, bytes]) => [key, JSON.stringify(bytes)])));
    });
  }
}
