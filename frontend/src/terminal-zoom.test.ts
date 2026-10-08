import { test } from "node:test";
import assert from "node:assert/strict";
import { attachZoomGesture } from "./terminal-zoom.ts";
import { MAX_FONT_SIZE, MIN_FONT_SIZE } from "./terminalStream.ts";

// A terminal's element and its window, with the size the terminal draws at;
// each zoom the gesture asks for is recorded and becomes the size.
function gestureAt(start: number) {
  const element = new EventTarget(), view = new EventTarget();
  const zooms: number[] = [];
  let size = start;
  const detach = attachZoomGesture(element, view, () => size, (next) => { zooms.push(next); size = next; });
  const dispatch = (target: EventTarget, type: string, fields: Record<string, number> = {}) => {
    const event = Object.assign(new Event(type, { cancelable: true }), fields);
    target.dispatchEvent(event);
    return event;
  };
  return {
    zooms,
    detach,
    press: (button = 2) => dispatch(element, "mousedown", { button }),
    release: (button = 2) => dispatch(view, "mouseup", { button }),
    wheel: (deltaY: number, deltaMode = 0) => dispatch(element, "wheel", { deltaY, deltaMode }),
    menu: () => dispatch(element, "contextmenu"),
    blur: () => dispatch(view, "blur"),
  };
}

test("holding the right button and turning the wheel steps the text size as Ctrl+Plus and Ctrl+Minus do", () => {
  // Arrange
  const gesture = gestureAt(20);

  // Act
  gesture.press();
  const up = gesture.wheel(-100);
  gesture.wheel(-100);
  const down = gesture.wheel(100);

  // Assert: up grows the text a step a notch, down shrinks it, and the wheel
  // neither scrolls the terminal nor reaches its program.
  assert.deepEqual(gesture.zooms, [21, 22, 21]);
  assert.equal(up.defaultPrevented, true);
  assert.equal(up.cancelBubble, true);
  assert.equal(down.defaultPrevented, true);
});

test("the wheel zooms only within 12 to 28 px", () => {
  const cases: [number, number][] = [[MAX_FONT_SIZE, -100], [MIN_FONT_SIZE, 100]];
  for (const [start, deltaY] of cases) {
    // Arrange
    const gesture = gestureAt(start);

    // Act
    gesture.press();
    const turn = gesture.wheel(deltaY);

    // Assert
    assert.deepEqual(gesture.zooms, [], `${start} px turned ${deltaY}`);
    assert.equal(turn.defaultPrevented, true, `${start} px turned ${deltaY}`);
  }
});

test("a smooth wheel's small turns add up to one step", () => {
  // Arrange
  const gesture = gestureAt(20);
  gesture.press();

  // Act
  gesture.wheel(-20);
  gesture.wheel(-20);
  const before = [...gesture.zooms];
  gesture.wheel(-20);

  // Assert
  assert.deepEqual(before, []);
  assert.deepEqual(gesture.zooms, [21]);
});

test("a wheel that counts in lines steps once a turn", () => {
  // Arrange
  const gesture = gestureAt(20);
  gesture.press();

  // Act
  gesture.wheel(-3, 1);
  gesture.wheel(3, 1);
  gesture.wheel(3, 1);

  // Assert
  assert.deepEqual(gesture.zooms, [21, 20, 19]);
});

test("a press that zoomed opens no menu, so nothing is pasted from it", () => {
  // Arrange
  const gesture = gestureAt(20);
  gesture.press();
  gesture.wheel(-100);

  // Act: Windows opens the menu once the button is released.
  gesture.release();
  const menu = gesture.menu();

  // Assert: the browser's menu, which offers Paste, never opens, and xterm
  // never readies its input under the pointer for that menu's Paste.
  assert.equal(menu.defaultPrevented, true);
  assert.equal(menu.cancelBubble, true);
});

test("a right click that never turned the wheel does what it did before", () => {
  // Arrange
  const gesture = gestureAt(20);
  gesture.press();
  gesture.wheel(-100);
  gesture.release();
  gesture.menu();

  // Act: a plain right click after a zoom.
  const press = gesture.press();
  gesture.release();
  const menu = gesture.menu();

  // Assert
  assert.equal(press.defaultPrevented, false);
  assert.equal(menu.defaultPrevented, false);
  assert.equal(menu.cancelBubble, false);
  assert.deepEqual(gesture.zooms, [21]);
});

test("the wheel without the right button scrolls as before", () => {
  const cases: [string, (gesture: ReturnType<typeof gestureAt>) => void][] = [
    ["never pressed", () => {}],
    ["released", (gesture) => { gesture.press(); gesture.release(); }],
    ["left the window", (gesture) => { gesture.press(); gesture.blur(); }],
    ["the left button held", (gesture) => { gesture.press(0); }],
  ];
  for (const [name, arrange] of cases) {
    // Arrange
    const gesture = gestureAt(20);
    arrange(gesture);

    // Act
    const turn = gesture.wheel(-100);

    // Assert
    assert.equal(turn.defaultPrevented, false, name);
    assert.deepEqual(gesture.zooms, [], name);
  }
});

test("a detached gesture zooms nothing", () => {
  // Arrange
  const gesture = gestureAt(20);
  gesture.detach();

  // Act
  gesture.press();
  const turn = gesture.wheel(-100);

  // Assert
  assert.equal(turn.defaultPrevented, false);
  assert.deepEqual(gesture.zooms, []);
});
