import { test } from "node:test";
import assert from "node:assert/strict";
import { isLeaveKey, isPasteKey, keyMessage, mouseMessage, pagePoint, wheelMessage, type KeyLike } from "./screen-input.ts";

const key = (name: string, modifiers: Partial<KeyLike> = {}): KeyLike => ({ key: name, code: "", keyCode: 0, altKey: false, ctrlKey: false, metaKey: false, shiftKey: false, ...modifiers });
const still = { altKey: false, ctrlKey: false, metaKey: false, shiftKey: false };

test("a point on the drawn frame falls on the page in the page's own pixels, kept inside it", () => {
  const box = { left: 100, top: 50, width: 640, height: 400 };
  const frame = { width: 1280, height: 800 };
  const cases: [number, number, { x: number; y: number }][] = [
    [100, 50, { x: 0, y: 0 }],
    [420, 250, { x: 640, y: 400 }],
    [740, 450, { x: 1280, y: 800 }],
    [20, 900, { x: 0, y: 800 }],
  ];
  for (const [x, y, want] of cases) assert.deepEqual(pagePoint(x, y, box, frame), want, x + "," + y);
  assert.deepEqual(pagePoint(10, 10, { left: 0, top: 0, width: 0, height: 0 }, frame), { x: 0, y: 0 }, "a frame not drawn yet");
});

test("a key that types a character carries it, Enter a carriage return, and a command or a named key nothing", () => {
  const cases: [KeyLike, string, string][] = [
    [key("a"), "a", "a letter"],
    [key("A", { shiftKey: true }), "A", "a shifted letter"],
    [key("@", { ctrlKey: true, altKey: true }), "@", "AltGr's character"],
    [key("é"), "é", "an accented letter"],
    [key("😀"), "😀", "a character of two code units"],
    [key("Enter"), "\r", "Enter"],
    [key("a", { ctrlKey: true }), "", "Ctrl+A, a command"],
    [key("Tab"), "", "Tab"],
    [key("Backspace"), "", "Backspace"],
    [key("ArrowLeft"), "", "an arrow"],
  ];
  for (const [event, text, what] of cases) assert.equal(keyMessage(event, "keyDown").text, text, what);
  assert.equal(keyMessage(key("a"), "keyUp").text, "", "a key's release types nothing");
  assert.equal(keyMessage(key("a", { ctrlKey: true, shiftKey: true }), "keyDown").modifiers, 10, "Ctrl and Shift by the Input domain's bits");
  assert.equal(keyMessage({ ...key("Unidentified"), keyCode: 999 }, "keyDown").key_code, 255, "a key code beyond a byte is held to one");
});

test("F6 leaves the screen and the paste key pastes, so neither reaches the page as a key", () => {
  assert.equal(isLeaveKey(key("F6")), true);
  assert.equal(isLeaveKey(key("F5")), false);
  assert.equal(isPasteKey(key("v", { ctrlKey: true })), true);
  assert.equal(isPasteKey(key("V", { ctrlKey: true, shiftKey: true })), true);
  assert.equal(isPasteKey(key("v", { metaKey: true })), true);
  assert.equal(isPasteKey(key("v")), false, "a plain v types");
  assert.equal(isPasteKey(key("v", { ctrlKey: true, altKey: true })), false, "AltGr+V types");
});

test("a press names its button and a move or a wheel names none", () => {
  const point = { x: 5, y: 6 };
  assert.equal(mouseMessage("mousePressed", point, { ...still, button: 0, buttons: 1 }, 1).button, "left");
  assert.equal(mouseMessage("mousePressed", point, { ...still, button: 2, buttons: 2 }, 1).button, "right");
  assert.equal(mouseMessage("mousePressed", point, { ...still, button: 9, buttons: 0 }, 1).button, "none", "a button the page has no name for");
  assert.equal(mouseMessage("mouseMoved", point, { ...still, button: 0, buttons: 1 }, 0).button, "none");
  assert.equal(mouseMessage("mouseMoved", point, { ...still, button: 0, buttons: 255 }, 0).buttons, 31, "only the five buttons' bits");
});

test("a wheel's lines and pages scroll the page by pixels", () => {
  const point = { x: 0, y: 0 }, frame = { width: 1280, height: 800 };
  const cases: [number, number, number][] = [[0, 120, 120], [1, 3, 120], [2, 1, 800]];
  for (const [mode, delta, want] of cases) assert.equal(wheelMessage(point, { ...still, deltaX: 0, deltaY: delta, deltaMode: mode, buttons: 0 }, frame).delta_y, want, "mode " + mode);
});
