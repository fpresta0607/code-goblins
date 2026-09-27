import { test } from "node:test";
import assert from "node:assert/strict";
import { bracketedPaste, endStep, fittedFontSize, historyText, inputBytes, MAX_WHEEL_LINES, maxInputBytes, panelGrid, queueInput, scrollAction, scrollHeldReason, scrollsItself, SELF_SCROLL_FRESH_MS, selfScrollFresh, sizeStep, typingHeldReason, wheelLines, wheelScroll, wheelTurn, type PaneCommand, type SizeEvent } from "./terminalInput.ts";

test("Unicode paste uses UTF-8 bytes including one complete bracketed wrapper", () => {
  const limit = Math.floor((maxInputBytes - 12) / 3);
  const accepted = bracketedPaste("界".repeat(limit));
  assert.ok(inputBytes(accepted) <= maxInputBytes);
  assert.ok(accepted.startsWith("\x1b[200~") && accepted.endsWith("\x1b[201~"));
  assert.throws(() => bracketedPaste("界".repeat(limit + 1)), /nothing was sent/);
  assert.throws(() => bracketedPaste("🙂".repeat(16382)), /nothing was sent/);
  assert.equal(bracketedPaste("one\n\x1b[201~two"), "\x1b[200~one\ntwo\x1b[201~");
});

const typed = (text: string): PaneCommand => ({ type: "terminal.input", text });

test("typing coalesces adjacent text, preserving Unicode, and keeps control keys and pastes apart", () => {
  const queue: PaneCommand[] = [];
  for (const text of ["c", "a", "f", "é", "日本", "🙂", "é"]) queueInput(queue, text);
  queueInput(queue, "\r");
  const paste = bracketedPaste("one\ntwo");
  queueInput(queue, paste);
  queueInput(queue, "after");
  queueInput(queue, "\x1b[A");
  assert.deepEqual(queue, [typed("café日本🙂é"), typed("\r"), typed(paste), typed("after"), typed("\x1b[A")]);
  const bounded: PaneCommand[] = [];
  for (let i = 0; i < 5000; i++) queueInput(bounded, "x");
  assert.equal(bounded.length, 2);
  assert.equal(bounded.map((command) => command.type === "terminal.input" ? command.text : "").join("").length, 5000);
});

test("scrolling up opens the pane's history, and scrolling down at its bottom follows the live screen again", () => {
  const cases: [string, boolean, boolean, number, ReturnType<typeof scrollAction>][] = [
    ["the wheel up on the live screen opens the history", false, true, -3, "open"],
    ["the wheel down on the live screen keeps following the bottom", false, true, 3, "none"],
    ["the wheel up in the history scrolls it", true, false, -3, "history"],
    ["the wheel down above the history's bottom scrolls it", true, false, 3, "history"],
    ["the wheel down at the history's bottom returns to the live screen", true, true, 3, "close"],
    ["the wheel up at the history's bottom scrolls it", true, true, -3, "history"],
  ];
  for (const [name, inHistory, atBottom, lines, action] of cases) assert.equal(scrollAction(inHistory, atBottom, lines), action, name);
});

test("a pane's history is drawn line by line from the first column, with no color left on", () => {
  assert.equal(historyText("one\n\x1b[31mtwo\x1b[0m\r\nthree"), "one\r\n\x1b[31mtwo\x1b[0m\r\nthree\x1b[0m");
  assert.equal(historyText(""), "\x1b[0m");
});

test("the wheel scrolls whole lines, keeping what a touchpad has not yet made a line", () => {
  const row = 18;
  const cases: [string, number, number, number, { lines: number; rest: number }][] = [
    ["a mouse notch in pixels", 0, 100, 0, { lines: 5, rest: 10 }],
    ["a notch the other way", 0, -100, 0, { lines: -5, rest: -10 }],
    ["a touchpad's small moves add up", 10, 10, 0, { lines: 1, rest: 2 }],
    ["less than a line waits", 0, 10, 0, { lines: 0, rest: 10 }],
    ["a wheel in lines", 0, 3, 1, { lines: 3, rest: 0 }],
    ["a wheel in pages scrolls the screen", 0, 1, 2, { lines: 40, rest: 0 }],
  ];
  for (const [name, rest, delta, mode, want] of cases) assert.deepEqual(wheelLines(rest, delta, mode, row, 40), want, name);
});

test("the wheel sends a pane that scrolls itself only Herdr's wheel scroll, never a key that could type, edit or submit", () => {
  for (let lines = -250; lines <= 250; lines++) {
    const command = wheelScroll(lines);
    if (lines === 0) {
      assert.equal(command, null);
      continue;
    }
    assert.deepEqual(command, { type: "terminal.scroll", direction: lines < 0 ? "up" : "down", lines: Math.min(Math.abs(lines), MAX_WHEEL_LINES), source: "wheel" }, `${lines} lines`);
    assert.ok(command && !("text" in command), `${lines} lines carry no text`);
  }
});

test("only a Claude Code pane whose history is no taller than its screen scrolls itself", () => {
  const lines = (count: number) => Array.from({ length: count }, (_, index) => `line ${index}`).join("\n");
  const cases: [string, string, number, string, boolean][] = [
    ["a fullscreen Claude Code screen", lines(28), 28, "claude", true],
    ["trailing blank lines are not history", lines(28) + "\n\n", 28, "claude", true],
    ["one line above the screen is history", lines(29), 28, "claude", false],
    ["a Codex pane reads its history", lines(12), 28, "codex", false],
    ["a pane with no known agent reads its history", lines(12), 28, "", false],
  ];
  for (const [name, history, rows, agent, want] of cases) assert.equal(scrollsItself(history, rows, agent), want, name);
});

test("a pane found to scroll itself is trusted only until its history is due a fresh look", () => {
  const checkedAt = 1_000_000;
  const cases: [string, number, number, boolean][] = [
    ["just checked", checkedAt, checkedAt, true],
    ["a moment later", checkedAt, checkedAt + SELF_SCROLL_FRESH_MS - 1, true],
    ["thirty seconds later, the history is read again", checkedAt, checkedAt + SELF_SCROLL_FRESH_MS, false],
    ["long after, as a pane builds scrollback", checkedAt, checkedAt + 10 * 60_000, false],
    ["never checked, as a new connection is", 0, checkedAt, false],
  ];
  for (const [name, at, now, want] of cases) assert.equal(selfScrollFresh(at, now), want, name);
});

test("the wheel over a pane that scrolls itself never asks again for a take that was refused", () => {
  const cases: [string, Parameters<typeof wheelTurn>[0], ReturnType<typeof wheelTurn>][] = [
    ["a view that sizes the pane scrolls it", { sized: true, refused: false }, "scroll"],
    ["a view that does not takes the pane first", { sized: false, refused: false }, "take"],
    ["a view whose take was refused says why instead", { sized: false, refused: true }, "refused"],
  ];
  for (const [name, view, want] of cases) assert.equal(wheelTurn(view), want, name);
});

test("a wheel that cannot scroll a pane explains itself in plain words", () => {
  const cases: [string, string][] = [
    ["pipeline owns this task; use cfo pipeline respond or inspect its delivery evidence", "A review gate owns this goblin's pane now; scroll it in Herdr."],
    ["pipeline custody has not been returned; use cfo pipeline recover", "A review gate owns this goblin's pane now; scroll it in Herdr."],
    ["Native terminal disconnected.", "The wheel cannot scroll this pane: Native terminal disconnected."],
  ];
  for (const [raw, plain] of cases) assert.equal(scrollHeldReason(raw), plain, raw);
});

test("a pane's screen is fitted to the panel whole, by its width or its height, with no floor", () => {
  const cell = { width: 0.6, height: 1.2 };
  const cases: [string, number, number, number, number, number | null][] = [
    ["maximized, bound by height", 1800, 1000, 132, 43, 19],
    ["the side panel, bound by width and below 12 px", 900, 800, 132, 43, 11],
    ["a small pane in a large panel stops at 28 px", 2000, 1200, 80, 24, 28],
    ["a hidden panel keeps the font it has", 0, 800, 132, 43, null],
    ["a pane with no size yet keeps the font it has", 900, 800, 0, 0, null],
  ];
  for (const [name, width, height, cols, rows, size] of cases) assert.equal(fittedFontSize(width, height, cols, rows, cell), size, name);
});

test("the fit follows the cell size the terminal measured, not an assumed one", () => {
  assert.equal(fittedFontSize(900, 800, 132, 43, { width: 0.5, height: 1.2 }), 13.5);
  assert.equal(fittedFontSize(1800, 1000, 132, 43, { width: 0.6, height: 1.5 }), 15.5);
});

test("typing never merges into a size the view asked for", () => {
  const queue: PaneCommand[] = [];
  queueInput(queue, "a");
  queue.push({ type: "terminal.resize", cols: 100, rows: 30 });
  queueInput(queue, "b");
  assert.deepEqual(queue, [typed("a"), { type: "terminal.resize", cols: 100, rows: 30 }, typed("b")]);
});

test("a view that sizes its pane fills the panel with whole cells, within what Herdr accepts", () => {
  const cell = { width: 0.6, height: 1.2 };
  const cases: [string, number, number, number, { cols: number; rows: number } | null][] = [
    ["a maximized panel at 20 px", 1574, 750, 20, { cols: 131, rows: 31 }],
    ["the side panel at 20 px", 700, 750, 20, { cols: 58, rows: 31 }],
    ["a larger font holds fewer cells", 1574, 750, 28, { cols: 93, rows: 22 }],
    ["a vast panel stops at Herdr's largest pane", 20000, 20000, 12, { cols: 400, rows: 160 }],
    ["a panel too narrow for a terminal", 200, 750, 20, null],
    ["a hidden panel", 0, 0, 20, null],
  ];
  for (const [name, width, height, font, grid] of cases) assert.deepEqual(panelGrid(width, height, font, cell), grid, name);
});

test("an open view keeps the pane live at the board's size, focused or not, until it closes", () => {
  const view = { sized: false, focused: true, shown: true, held: false };
  const cases: [string, SizeEvent, typeof view, ReturnType<typeof sizeStep>][] = [
    ["opening the terminal in the focused board takes the size", "live", view, "take"],
    ["opening it while he is in another window takes the size too", "live", { ...view, focused: false }, "take"],
    ["showing a view again takes the size, focused or not", "shown", { ...view, focused: false }, "take"],
    ["opening it after another client took the pane leaves the size", "live", { ...view, held: true }, "stay"],
    ["coming back to the board takes the size, even from another client", "focus", { ...view, held: true }, "take"],
    ["typing in the board takes the size, even from another client", "typed", { ...view, held: true }, "take"],
    ["leaving the board for a Herdr window keeps the size", "blur", { ...view, sized: true }, "stay"],
    ["moving to another view keeps the size", "hidden", { ...view, sized: true }, "stay"],
    ["a view out of sight never takes the size", "focus", { ...view, shown: false }, "stay"],
    ["a view that has the size keeps it", "typed", { ...view, sized: true }, "stay"],
    ["leaving a view that does not have the size changes nothing", "blur", view, "stay"],
  ];
  for (const [name, event, state, action] of cases) assert.equal(sizeStep(event, state), action, name);
});

test("a connection that ends on its own stops the view, shows the pane afresh at its own size, or keeps the screen", () => {
  const cases: [string, Parameters<typeof endStep>[0], boolean | null, ReturnType<typeof endStep>][] = [
    ["the pane's own view ending stops the view with the reason", { sized: false, onScreen: true, resized: false }, false, "stop"],
    ["the pane's own view ending as Herdr resized the pane shows it afresh", { sized: false, onScreen: true, resized: true }, false, "observe"],
    ["a sized view ending, as another client takes the pane, shows it afresh", { sized: true, onScreen: true, resized: false }, true, "observe"],
    ["a take refused keeps the screen", { sized: true, onScreen: false, resized: false }, false, "keep"],
    ["a take refused after the screen's view ended shows the pane afresh", { sized: true, onScreen: false, resized: false }, null, "observe"],
    ["a give that fails still lets the sized view go and shows the pane afresh", { sized: false, onScreen: false, resized: false }, true, "observe"],
    ["the view's first connection failing stops the view with the reason", { sized: false, onScreen: false, resized: false }, null, "stop"],
  ];
  for (const [name, ended, screenSized, action] of cases) assert.equal(endStep(ended, screenSized), action, name);
});

test("a refused input explains itself in plain words", () => {
  const cases: [string, string][] = [
    ["pipeline owns this task; use cfo pipeline respond or inspect its delivery evidence", "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen."],
    ["pipeline custody has not been returned; use cfo pipeline recover", "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen."],
    ["cannot verify pipeline custody", "cannot verify pipeline custody"],
    ["Native terminal identity changed. Select the current session; input was discarded.", "Native terminal identity changed. Select the current session; input was discarded."],
  ];
  for (const [raw, plain] of cases) assert.equal(typingHeldReason(raw), plain, raw);
});
