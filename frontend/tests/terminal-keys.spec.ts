import { expect, test, type Page } from "./site";

// The Overlord pressed Enter on a row of Claude Code's permissions screen on
// 2026-10-09 and nothing was approved ("make sure all key controls are
// accessible in the goblin terminal display"). His Enter had reached the
// terminal at once and sat unread while Claude Code was busy, and nothing on
// the board said so. This is the key table of a native terminal: each key and
// chord a harness screen names is sent as xterm, a plain terminal, sends it,
// to the CFO's terminal and a goblin's, whatever the program is doing, and
// the keys the board keeps for itself send nothing.

const ESC = "\x1b";
const LETTERS = "abcdefghijklmnopqrstuvwxyz".split("");

// What each key sends, in the order a screen's footer names them.
const KEYS: [key: string, sent: string][] = [
  ["Enter", "\r"], ["Escape", ESC], ["Tab", "\t"], ["Shift+Tab", ESC + "[Z"], ["Space", " "], ["Backspace", "\x7f"], ["Delete", ESC + "[3~"], ["Insert", ESC + "[2~"],
  ["ArrowUp", ESC + "[A"], ["ArrowDown", ESC + "[B"], ["ArrowRight", ESC + "[C"], ["ArrowLeft", ESC + "[D"],
  ["Home", ESC + "[H"], ["End", ESC + "[F"], ["PageUp", ESC + "[5~"], ["PageDown", ESC + "[6~"],
  ["r", "r"], ["y", "y"], ["n", "n"], ["a", "a"], ["Shift+KeyR", "R"], ["1", "1"], ["2", "2"], ["3", "3"], ["/", "/"], ["?", "?"], ["!", "!"], ["#", "#"], ["@", "@"],
  ["Control+Enter", "\r"], ["Alt+Enter", ESC + "\r"], ["Control+Backspace", "\b"], ["Alt+Backspace", ESC + "\x7f"], ["Control+Delete", ESC + "[3;5~"],
  ["Shift+ArrowUp", ESC + "[1;2A"], ["Shift+ArrowDown", ESC + "[1;2B"], ["Shift+ArrowRight", ESC + "[1;2C"], ["Shift+ArrowLeft", ESC + "[1;2D"],
  ["Alt+ArrowUp", ESC + "[1;3A"], ["Alt+ArrowDown", ESC + "[1;3B"], ["Alt+ArrowRight", ESC + "[1;3C"], ["Alt+ArrowLeft", ESC + "[1;3D"],
  ["Control+ArrowUp", ESC + "[1;5A"], ["Control+ArrowDown", ESC + "[1;5B"], ["Control+ArrowRight", ESC + "[1;5C"], ["Control+ArrowLeft", ESC + "[1;5D"],
  ["Shift+Home", ESC + "[1;2H"], ["Shift+End", ESC + "[1;2F"], ["Control+Home", ESC + "[1;5H"], ["Control+End", ESC + "[1;5F"],
  ["Control+PageUp", ESC + "[5;5~"], ["Control+PageDown", ESC + "[6;5~"],
  ["F1", ESC + "OP"], ["F2", ESC + "OQ"], ["F3", ESC + "OR"], ["F4", ESC + "OS"], ["F5", ESC + "[15~"], ["F6", ESC + "[17~"],
  ["F7", ESC + "[18~"], ["F8", ESC + "[19~"], ["F9", ESC + "[20~"], ["F10", ESC + "[21~"], ["F11", ESC + "[23~"], ["F12", ESC + "[24~"],
  // Ctrl with a letter is that letter's control character. Ctrl+V is the
  // board's paste, which tests/terminal-input.spec.ts covers.
  ...LETTERS.filter((letter) => letter !== "v").map((letter): [string, string] => ["Control+" + letter, String.fromCharCode(letter.charCodeAt(0) - 96)]),
  ["Control+Space", "\x00"], ["Control+Shift+Digit2", "\x00"], ["Control+BracketLeft", ESC], ["Control+Backslash", "\x1c"], ["Control+BracketRight", "\x1d"], ["Control+Shift+Minus", "\x1f"],
  // Alt with a letter is Escape and the letter, as a harness reads Meta.
  ...LETTERS.map((letter): [string, string] => ["Alt+" + letter, ESC + letter]),
];

// The line break of a harness's composer is the one key sent per harness.
const SHIFT_ENTER: Record<string, string> = { claude: "\n", codex: ESC + "[74;36;10;1;8;1_" + ESC + "[74;36;10;0;8;1_", pi: "\r" };

// A program that asked for application cursor keys, as a full-screen one may,
// is sent the arrows, Home and End in that form, as any terminal sends them.
const APPLICATION_KEYS: Record<string, string> = { ArrowUp: ESC + "OA", ArrowDown: ESC + "OB", ArrowRight: ESC + "OC", ArrowLeft: ESC + "OD", Home: ESC + "OH", End: ESC + "OF" };

// The keys the board keeps: the font size, the way out of the terminal to the
// panel, copy, the scroll of the terminal's own history, and dictation. A
// paste is kept too, and tests/terminal-input.spec.ts covers what it types.
const KEPT = ["Control+Equal", "Control+Shift+Equal", "Control+Minus", "Control+Digit0", "Control+Shift+c", "Control+Insert", "Shift+PageUp", "Shift+PageDown", "Control+Shift+Space", "Shift+Escape"];

// The chords xterm has no bytes for, which no harness screen names.
const UNSENT = ["Control+Shift+k", "Control+Slash", "Control+Digit1", "Control+Digit9"];

const NOTICE = "Program busy. Typed keys are waiting.";

// A terminal is what the page's native terminal is connected to: what it was
// sent, and a way to write output and to say whether typed keys sit unread.
interface Terminal {
  inputs: Buffer[];
  heard: () => void;
  write: (text: string) => void;
  unread: (isUnread: boolean) => void;
}

// connect answers the page's native terminal, saying first of all that keys
// sit unread when isUnreadAtOpen.
async function connect(page: Page, isUnreadAtOpen = false): Promise<Terminal> {
  const terminal: Terminal = { inputs: [], heard: () => {}, write: () => {}, unread: () => {} };
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    terminal.write = (text) => socket.send(Buffer.from(text));
    terminal.unread = (isUnread) => socket.send(JSON.stringify({ type: "unread", unread: isUnread }));
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from(ESC + "[2J" + ESC + "[HREADY\r\n"));
    if (isUnreadAtOpen) terminal.unread(true);
    socket.onMessage((data) => {
      if (typeof data === "string") return;
      terminal.inputs.push(data);
      terminal.heard();
    });
  });
  return terminal;
}

async function open(page: Page, role: string, harness: string): Promise<void> {
  await page.goto("/tests/fixtures/terminal-input.html?" + new URLSearchParams({ harness, role }) + "#native");
  await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
  await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
}

// press presses key and returns what the terminal was sent for it: the one
// message a key sends, or nothing when none came within a second.
async function press(page: Page, terminal: Terminal, key: string): Promise<string> {
  const before = terminal.inputs.length;
  const message = new Promise<void>((resolve) => { terminal.heard = resolve; });
  await page.keyboard.press(key);
  await Promise.race([message, new Promise((resolve) => setTimeout(resolve, 1000))]);
  return Buffer.concat(terminal.inputs.slice(before)).toString("latin1");
}

// settle resolves once the terminal has drawn everything written to it so
// far: it answers a status query written after that.
async function settle(terminal: Terminal): Promise<void> {
  const before = terminal.inputs.length;
  terminal.write(ESC + "[5n");
  await expect.poll(() => Buffer.concat(terminal.inputs.slice(before)).toString("latin1")).toBe(ESC + "[0n");
}

// What the program is doing while the table is pressed, and how the terminal
// is put there: nothing, busy with typed keys unread, or on a full screen of
// its own.
const STATES: [state: string, enter: (terminal: Terminal) => void][] = [
  ["idle", () => {}],
  ["busy", (terminal) => terminal.unread(true)],
  ["on a full screen", (terminal) => terminal.write(ESC + "[?1049h" + ESC + "[2J" + ESC + "[HFULL SCREEN")],
];

// pressEach presses every key of the table and returns what each one sent,
// written as JSON so a difference shows its control characters.
async function pressEach(page: Page, terminal: Terminal, keys: [string, string][]): Promise<{ sent: Record<string, string>; want: Record<string, string> }> {
  const sent: Record<string, string> = {}, want: Record<string, string> = {};
  for (const [key, bytes] of keys) {
    sent[key] = JSON.stringify(await press(page, terminal, key));
    want[key] = JSON.stringify(bytes);
  }
  return { sent, want };
}

for (const role of ["cfo", "goblin"]) {
  for (const harness of ["claude", "codex", "pi"]) {
    const keys: [string, string][] = [...KEYS, ["Shift+Enter", SHIFT_ENTER[harness]]];

    for (const [state, enter] of STATES) {
      test("every key reaches the " + role + "'s " + harness + " as a plain terminal sends it, " + state, async ({ page }) => {
        // It presses the whole table, each key waiting for what it sent,
        // which a loaded machine stretches past the default time.
        test.slow();
        const terminal = await connect(page);
        await open(page, role, harness);
        enter(terminal);
        await settle(terminal);

        const { sent, want } = await pressEach(page, terminal, keys);

        expect(sent).toEqual(want);
      });
    }
  }

  test("the " + role + "'s program that asked for application cursor keys is sent them in that form", async ({ page }) => {
    const terminal = await connect(page);
    await open(page, role, "claude");
    terminal.write(ESC + "[?1h");
    await settle(terminal);

    const { sent, want } = await pressEach(page, terminal, KEYS.map(([key, bytes]): [string, string] => [key, APPLICATION_KEYS[key] ?? bytes]));

    expect(sent).toEqual(want);
  });

  for (const [name, chords] of [["the keys the board keeps", KEPT], ["the chords xterm has no bytes for", UNSENT]] as const) {
    test(name + " send the " + role + "'s program nothing", async ({ page, context }) => {
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      const terminal = await connect(page);
      await open(page, role, "claude");

      for (const key of chords) await page.keyboard.press(key);
      await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
      await page.keyboard.press("x");

      await expect.poll(() => Buffer.concat(terminal.inputs).toString("latin1")).toBe("x");
      await page.waitForTimeout(300);
      expect(Buffer.concat(terminal.inputs).toString("latin1")).toBe("x");
    });
  }

  test("a key the " + role + "'s busy program has not read yet is said to wait, until it is read", async ({ page }) => {
    const terminal = await connect(page);
    await open(page, role, "claude");
    const notice = page.getByRole("status").filter({ hasText: NOTICE });
    await expect(notice).toHaveCount(0);

    expect(await press(page, terminal, "Enter")).toBe("\r");
    terminal.unread(true);

    await expect(notice).toBeVisible();
    await expect(notice).toHaveAttribute("data-tip", "The program is busy. It takes what was typed when it is free, so there is no need to press again.");
    terminal.unread(false);
    await expect(notice).toHaveCount(0);
  });

  test("the " + role + "'s terminal opened while keys wait says so once it is whole", async ({ page }) => {
    await connect(page, true);

    await open(page, role, "claude");

    await expect(page.getByRole("status").filter({ hasText: NOTICE })).toBeVisible();
  });
}

// The Overlord's desktop window is 1707 CSS pixels wide at a scale of 1.5.
test.describe("at the Overlord's window size", () => {
  test.use({ viewport: { width: 1707, height: 912 }, deviceScaleFactor: 1.5 });

  test("the waiting notice sits in the terminal's top left corner, clear of its other marks and of the line he types on", async ({ page }) => {
    const terminal = await connect(page);
    await open(page, "cfo", "claude");
    terminal.unread(true);
    const notice = page.getByRole("status").filter({ hasText: NOTICE });
    await expect(notice).toBeVisible();

    const [mark, panel] = [await notice.boundingBox(), await page.locator(".host-terminal").first().boundingBox()];

    if (!mark || !panel) throw new Error("the notice or its terminal has no box");
    expect(mark.x - panel.x).toBeCloseTo(12, 0);
    expect(mark.y - panel.y).toBeCloseTo(10, 0);
    expect(mark.x + mark.width).toBeLessThan(panel.x + panel.width / 2);
    expect(mark.y + mark.height).toBeLessThan(panel.y + 60);
    expect(await notice.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  });
});
