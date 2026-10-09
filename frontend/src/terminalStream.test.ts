import { test, type TestContext } from "node:test";
import assert from "node:assert/strict";
import { ackDue, ACK_STEP, closedReason, DEFAULT_FONT_SIZE, type FitEvent, followFontSize, fontSizeFor, INPUT_MESSAGE, inputMessages, MAX_FONT_SIZE, MIN_FONT_SIZE, nextFit, panelFit, parseHistory, parseSize, parseUnread, reconnects, storedFontSize, storeFontSize, usableSize } from "./terminalStream.ts";

test("output is acknowledged in steps, and at once when the terminal has caught up", () => {
  const cases: [number, number, number, boolean][] = [
    [100, 0, 5000, false],
    [ACK_STEP, 0, ACK_STEP * 3, true],
    [5000, 0, 5000, true],
    [5000, 5000, 5000, false],
    [ACK_STEP + 10, 10, ACK_STEP * 2, true],
    [ACK_STEP + 9, 10, ACK_STEP * 2, false],
  ];
  for (const [consumed, acknowledged, written, due] of cases) assert.equal(ackDue(consumed, acknowledged, written), due, `${consumed} consumed, ${acknowledged} acknowledged, ${written} written`);
});

test("a long paste travels in ordered pieces the relay accepts, and nothing is lost", () => {
  const bytes = new Uint8Array(INPUT_MESSAGE * 2 + 17).map((_, i) => i % 251);
  const pieces = inputMessages(bytes);
  assert.equal(pieces.length, 3);
  assert.ok(pieces.every((piece) => piece.length <= INPUT_MESSAGE));
  assert.deepEqual(Uint8Array.from(pieces.flatMap((piece) => [...piece])), bytes);
  assert.deepEqual(inputMessages(new TextEncoder().encode("x")), [new TextEncoder().encode("x")]);
  assert.deepEqual(inputMessages(new Uint8Array()), []);
});

test("Ctrl with plus, minus or zero sizes the font within bounds, and other keys leave it", () => {
  assert.equal(fontSizeFor("=", 20), 21);
  assert.equal(fontSizeFor("+", MAX_FONT_SIZE), MAX_FONT_SIZE);
  assert.equal(fontSizeFor("-", 20), 19);
  assert.equal(fontSizeFor("-", MIN_FONT_SIZE), MIN_FONT_SIZE);
  assert.equal(fontSizeFor("0", 22), DEFAULT_FONT_SIZE);
  assert.equal(fontSizeFor("c", 16), null);
  assert.equal(fontSizeFor("_", 16), null);
  assert.equal(DEFAULT_FONT_SIZE, 20);
});

test("only the relay's size messages are read as sizes", () => {
  assert.deepEqual(parseSize('{"type":"size","cols":100,"rows":30}'), { cols: 100, rows: 30 });
  for (const text of ['{"type":"resize","cols":100,"rows":30}', '{"type":"size","cols":"100","rows":30}', "not json", '{"type":"size","cols":0,"rows":30}']) assert.equal(parseSize(text), null, text);
});

test("a panel measured while hidden never resizes the terminal", () => {
  assert.equal(usableSize(120, 40), true);
  assert.equal(usableSize(19, 40), false);
  assert.equal(usableSize(120, 4), false);
  assert.equal(usableSize(Number.NaN, 40), false);
});

test("a view reconnects on its own only when the terminal is still there to reach", () => {
  const cases: [number, boolean][] = [[1013, true], [1001, true], [1006, true], [1000, false], [1008, false], [1003, false]];
  for (const [code, again] of cases) assert.equal(reconnects(code), again, String(code));
});

test("a closed view says why in plain words", () => {
  assert.equal(closedReason(1000, "The terminal ended with exit code 0."), "The terminal ended with exit code 0.");
  assert.equal(closedReason(1006, ""), "The connection to the board dropped.");
  assert.equal(closedReason(1008, "pipeline owns this task"), "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen.");
});

test("the relay's history header names how many bytes replay the history", () => {
  assert.equal(parseHistory('{"type":"history","bytes":5208}'), 5208);
  assert.equal(parseHistory('{"type":"history","bytes":0}'), 0);
  for (const text of ['{"type":"size","cols":1,"rows":1}', '{"type":"history","bytes":-1}', '{"type":"history","bytes":1.5}', '{"type":"history"}', "nope"]) assert.equal(parseHistory(text), null, text);
});

test("the relay says when key presses sit unread in a busy program's input and when they were read", () => {
  assert.equal(parseUnread('{"type":"unread","unread":true}'), true);
  assert.equal(parseUnread('{"type":"unread","unread":false}'), false);
  for (const text of ['{"type":"unread"}', '{"type":"unread","unread":1}', '{"type":"unread","unread":"true"}', '{"type":"size","cols":100,"rows":30}', '{"type":"history","bytes":0}', "null", "nope"]) assert.equal(parseUnread(text), null, text);
});

test("a terminal fills its panel with the same padding left, right and below, and its last row at the bottom", () => {
  const cases: { width: number; height: number; cell: { width: number; height: number } }[] = [
    { width: 1574, height: 749.8, cell: { width: 12.04, height: 31 } },
    { width: 1574, height: 749.8, cell: { width: 12.6, height: 32 } },
    { width: 1574, height: 749.8, cell: { width: 13.25, height: 34 } },
    { width: 800, height: 600, cell: { width: 10, height: 24 } },
    { width: 641.5, height: 403.2, cell: { width: 9.6, height: 19 } },
  ];
  for (const { width, height, cell } of cases) {
    const fit = panelFit(width, height, cell, 10);
    assert.ok(fit, `${width}x${height}`);
    assert.equal(fit.cols, Math.floor((width - 20) / cell.width));
    assert.ok(fit.left >= 10 && fit.left < 10 + cell.width / 2 + 1e-9, `the sides ${fit.left} hold the padding and half a spare cell at most`);
    assert.equal(fit.right, fit.left, "left and right are even");
    assert.equal(fit.bottom, fit.left, "the input line sits the same distance from the bottom");
    assert.ok(fit.top >= fit.left && fit.top < fit.left + cell.height, `the spare height ${fit.top} is above the grid, under a row`);
    assert.ok(Math.abs(fit.left + fit.cols * cell.width + fit.right - width) < 1e-6, "the columns and padding fill the width");
    assert.ok(Math.abs(fit.top + fit.rows * cell.height + fit.bottom - height) < 1e-6, "the rows and padding fill the height");
  }
});

test("a claim waits for xterm to draw a resized grid, and a resize alone claims nothing", () => {
  const cases: { name: string; events: FitEvent[]; claims: boolean[] }[] = [
    { name: "another view's size, then its draw", events: ["resize", "draw"], claims: [false, false] },
    { name: "shown while not drawn, then the draw", events: ["resize", "claim", "draw"], claims: [false, false, true] },
    { name: "a refit while drawn", events: ["claim"], claims: [true] },
    { name: "claims held for one draw are made once", events: ["resize", "claim", "claim", "draw", "draw"], claims: [false, false, false, true, false] },
  ];
  for (const { name, events, claims } of cases) {
    let state = { isDrawn: true, isClaimPending: false };
    const made = events.map((event) => {
      const { isClaim, ...next } = nextFit(state, event);
      state = next;
      return isClaim;
    });
    assert.deepEqual(made, claims, name);
  }
});

test("a panel too small for a usable grid, or an unmeasured cell, gives no fit", () => {
  assert.equal(panelFit(150, 600, { width: 10, height: 24 }, 10), null);
  assert.equal(panelFit(800, 100, { width: 10, height: 24 }, 10), null);
  assert.equal(panelFit(800, 600, { width: 0, height: 24 }, 10), null);
  assert.equal(panelFit(800, 600, { width: 10, height: 0 }, 10), null);
});

// browser gives a test this device's storage, which can refuse writes as a
// private window does, and the page's window.
function browser(t: TestContext, items = new Map<string, string>(), isRefused = false) {
  const storage = { getItem: (key: string) => items.get(key) ?? null, setItem: (key: string, value: string) => { if (isRefused) throw new Error("storage refused"); items.set(key, value); } };
  const view = new EventTarget();
  Object.defineProperty(globalThis, "localStorage", { value: storage, configurable: true });
  Object.defineProperty(globalThis, "window", { value: view, configurable: true });
  t.after(() => { Reflect.deleteProperty(globalThis, "localStorage"); Reflect.deleteProperty(globalThis, "window"); });
  return { items, view };
}

test("a size chosen in one terminal reaches every open terminal at once and every one opened later", (t) => {
  // Arrange
  browser(t);
  const first: number[] = [], second: number[] = [];
  const closeFirst = followFontSize((size) => first.push(size));
  followFontSize((size) => second.push(size));

  // Act
  storeFontSize(24);

  // Assert
  assert.deepEqual(first, [24]);
  assert.deepEqual(second, [24]);
  assert.equal(storedFontSize(), 24);

  // Act: a closed terminal follows no more.
  closeFirst();
  storeFontSize(16);

  // Assert
  assert.deepEqual(first, [24]);
  assert.deepEqual(second, [24, 16]);
  assert.equal(storedFontSize(), 16);
});

test("a size chosen in another page of the board on this device reaches the terminals here", (t) => {
  // Arrange
  const { items, view } = browser(t);
  const sizes: number[] = [];
  followFontSize((size) => sizes.push(size));

  // Act
  items.set("cfo-terminal-font-size", "26");
  view.dispatchEvent(Object.assign(new Event("storage"), { key: "cfo-terminal-font-size" }));
  view.dispatchEvent(Object.assign(new Event("storage"), { key: "cfo-panel-width" }));

  // Assert
  assert.deepEqual(sizes, [26]);
});

test("a size the browser refuses to keep still reaches the open terminals", (t) => {
  // Arrange
  browser(t, new Map(), true);
  const sizes: number[] = [];
  followFontSize((size) => sizes.push(size));

  // Act
  storeFontSize(22);

  // Assert
  assert.deepEqual(sizes, [22]);
});

test("a kept size outside 12 to 28 px, or none, draws at 20 px", (t) => {
  const cases: [string | null, number][] = [[null, 20], ["11", 20], ["29", 20], ["abc", 20], ["12", 12], ["28", 28], ["23", 23]];
  for (const [kept, size] of cases) {
    // Arrange
    browser(t, new Map(kept === null ? [] : [["cfo-terminal-font-size", kept]]));

    // Act
    const drawn = storedFontSize();

    // Assert
    assert.equal(drawn, size, String(kept));
  }
});
