import { test, mock } from "node:test";
import assert from "node:assert/strict";
import { FrameWriter, StreamTracker, SYNC_END, SYNC_START } from "./terminalFrames.ts";

const bytes = (text: string) => new TextEncoder().encode(text);
// A chunk as large as one host read: part of a redraw that goes on.
const full = (text: string) => bytes(text.padEnd(32 * 1024, "."));
const fed = (...chunks: (string | Uint8Array)[]) => {
  const tracker = new StreamTracker();
  for (const chunk of chunks) tracker.feed(typeof chunk === "string" ? bytes(chunk) : chunk);
  return tracker;
};

test("the tracker knows when the stream sits between sequences and characters", () => {
  assert.equal(fed("plain text\r\n").between, true);
  assert.equal(fed("before \x1b[38;2;1").between, false, "inside a CSI");
  assert.equal(fed("before \x1b[38;2;1", "10;2m after").between, true);
  assert.equal(fed("\x1b").between, false, "a lone escape");
  assert.equal(fed("\x1b(", "B").between, true, "a charset designation");
  assert.equal(fed("\x1b]0;window title").between, false, "inside an OSC");
  assert.equal(fed("\x1b]0;title\x07done").between, true, "an OSC ended by BEL");
  assert.equal(fed("\x1b]8;;https://example.com\x1b\\link").between, true, "an OSC ended by ST");
  const e = bytes("é");
  assert.equal(fed(e.subarray(0, 1)).between, false, "half a character");
  assert.equal(fed(e.subarray(0, 1), e.subarray(1)).between, true);
  assert.equal(fed("\x1b[2", "\x18").between, true, "CAN cancels a sequence");
});

test("the tracker follows the program's own synchronized updates", () => {
  assert.equal(fed("\x1b[?2026h").programSync, true);
  assert.equal(fed("\x1b[?2026h frame \x1b[?2026l").programSync, false);
  assert.equal(fed("\x1b[?25;2026h").programSync, true, "set among other modes");
  assert.equal(fed("\x1b[2026h").programSync, false, "not a private mode");
  assert.equal(fed("\x1b[1;2026h").programSync, false, "not a private mode, among other modes");
  assert.equal(fed("\x1b[?20", "26h").programSync, true, "split across chunks");
});

function writer(ended: () => void = () => {}) {
  const writes: string[] = [];
  const decoder = new TextDecoder();
  const frames = new FrameWriter((data, done) => { writes.push(typeof data === "string" ? data : decoder.decode(data)); done?.(); }, 8, ended);
  return { writes, frames };
}

test("a chunk smaller than one host read is a whole redraw and is written as it is", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const { writes, frames } = writer();
    frames.push(bytes("x"), () => {});
    mock.timers.tick(20);
    assert.deepEqual(writes, ["x"], "an echoed key is painted without any update around it");
    assert.equal(frames.updating, false);
  } finally { mock.timers.reset(); }
});

test("a redraw larger than one read is written inside one synchronized update that ends when it is complete", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const { writes, frames } = writer();
    const first = full("\x1b[H\x1b[2Jfirst part ");
    frames.push(first, () => {});
    frames.push(bytes("last part"), () => {});
    assert.deepEqual(writes, [SYNC_START, new TextDecoder().decode(first), "last part"]);
    mock.timers.tick(1);
    assert.equal(writes.at(-1), SYNC_END, "the short tail ends the redraw");
    const next = writer();
    next.frames.push(full("big"), () => {});
    mock.timers.tick(1);
    assert.notEqual(next.writes.at(-1), SYNC_END, "more of the same redraw is on its way");
    mock.timers.tick(8);
    assert.equal(next.writes.at(-1), SYNC_END);
  } finally { mock.timers.reset(); }
});

test("an update never ends inside an escape sequence, and waits for the rest", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const { writes, frames } = writer();
    const opening = full("text").slice(0, 32 * 1024 - 7);
    const head = new Uint8Array([...opening, ...bytes("\x1b[38;2;")]);
    frames.push(head, () => {});
    mock.timers.tick(20);
    assert.equal(writes.length, 2, "no end marker inside the colour code");
    frames.push(bytes("1;2;3m more"), () => {});
    mock.timers.tick(1);
    assert.deepEqual(writes.slice(2), ["1;2;3m more", SYNC_END]);
  } finally { mock.timers.reset(); }
});

test("output inside the program's own update is written as the program framed it", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const { writes, frames } = writer();
    frames.push(full("\x1b[?2026hprogram frame"), () => {});
    mock.timers.tick(20);
    assert.equal(writes.length, 2, "no end marker inside the program's update");
    frames.push(bytes(" rest\x1b[?2026l"), () => {});
    mock.timers.tick(1);
    assert.deepEqual(writes.slice(2), [" rest\x1b[?2026l", SYNC_END]);
    const inside = writer();
    inside.frames.push(bytes("\x1b[?2026hsmall"), () => {});
    inside.frames.push(full("large"), () => {});
    assert.equal(inside.writes.includes(SYNC_START), false, "never an update inside the program's own");
  } finally { mock.timers.reset(); }
});

test("the writer says when an update has ended and been written, so a view is shown only once it is drawn", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const ended: string[] = [];
    const { writes, frames } = writer(() => ended.push("ended"));
    frames.push(full("repaint"), () => {});
    assert.equal(frames.updating, true);
    assert.deepEqual(ended, []);
    mock.timers.tick(8);
    assert.equal(frames.updating, false);
    assert.deepEqual(ended, ["ended"]);
    assert.equal(writes.at(-1), SYNC_END);
  } finally { mock.timers.reset(); }
});

test("every chunk is handed on with its own completion, so acknowledgements count only real output", () => {
  const done: string[] = [];
  const frames = new FrameWriter((data, callback) => { if (typeof data !== "string") callback?.(); }, 8);
  frames.push(full("one"), () => done.push("one"));
  frames.push(bytes("two"), () => done.push("two"));
  frames.dispose();
  assert.deepEqual(done, ["one", "two"]);
});
