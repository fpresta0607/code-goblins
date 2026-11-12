// What a Screen view sends the supervisor's relay while he drives a goblin's
// browser: the DevTools Input domain's mouse and key events, by the names the
// relay takes. None of it is kept or logged on the board's side.

export interface ScreenMouse {
  type: "mouse";
  event: "mousePressed" | "mouseReleased" | "mouseMoved" | "mouseWheel";
  x: number;
  y: number;
  button: string;
  buttons: number;
  click_count: number;
  delta_x: number;
  delta_y: number;
  modifiers: number;
}

export interface ScreenKey {
  type: "key";
  event: "keyDown" | "keyUp";
  key: string;
  code: string;
  key_code: number;
  text: string;
  modifiers: number;
}

// FrameSize is the page's own size in its pixels, as its frames report it,
// and Box where the frame is drawn on the board.
export interface FrameSize { width: number; height: number }
export interface Box { left: number; top: number; width: number; height: number }

interface Modifiers { altKey: boolean; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean }

export interface KeyLike extends Modifiers { key: string; code: string; keyCode: number }

// The Input domain's own bits: Alt 1, Ctrl 2, Meta 4, Shift 8.
export function modifiersOf(event: Modifiers): number {
  return (event.altKey ? 1 : 0) | (event.ctrlKey ? 2 : 0) | (event.metaKey ? 4 : 0) | (event.shiftKey ? 8 : 0);
}

// pagePoint is where a point on the drawn frame falls on the page, in the
// page's own pixels, kept inside the page.
export function pagePoint(clientX: number, clientY: number, box: Box, frame: FrameSize): { x: number; y: number } {
  const within = (value: number, size: number) => Math.min(Math.max(value, 0), size);
  if (box.width <= 0 || box.height <= 0) return { x: 0, y: 0 };
  return {
    x: within((clientX - box.left) / box.width * frame.width, frame.width),
    y: within((clientY - box.top) / box.height * frame.height, frame.height),
  };
}

// A pointer's button by the DOM's number; its buttons bits are the Input
// domain's already.
const BUTTONS = ["left", "middle", "right", "back", "forward"];

export function mouseMessage(event: ScreenMouse["event"], point: { x: number; y: number }, pointer: Modifiers & { button: number; buttons: number }, clickCount: number): ScreenMouse {
  const button = event === "mouseMoved" || event === "mouseWheel" ? "none" : BUTTONS[pointer.button] ?? "none";
  return { type: "mouse", event, x: point.x, y: point.y, button, buttons: pointer.buttons & 31, click_count: clickCount, delta_x: 0, delta_y: 0, modifiers: modifiersOf(pointer) };
}

// A wheel's lines and pages, in pixels, as the page scrolls by them.
const LINE_PIXELS = 40;

export function wheelMessage(point: { x: number; y: number }, wheel: Modifiers & { deltaX: number; deltaY: number; deltaMode: number; buttons: number }, frame: FrameSize): ScreenMouse {
  const scale = wheel.deltaMode === 1 ? LINE_PIXELS : wheel.deltaMode === 2 ? frame.height : 1;
  return { ...mouseMessage("mouseWheel", point, { ...wheel, button: -1 }, 0), delta_x: wheel.deltaX * scale, delta_y: wheel.deltaY * scale };
}

// isLeaveKey says the key takes the keyboard off the screen, back to the
// board, the way F6 moves between a browser's parts: it never reaches the
// page.
export function isLeaveKey(event: KeyLike): boolean {
  return event.key === "F6";
}

// isPasteKey says the key pastes, whose paste carries his clipboard's text to
// the page, so the key itself goes no further.
export function isPasteKey(event: KeyLike): boolean {
  return (event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === "v";
}

// keyMessage is a key for the page. A key that types a character carries it,
// as AltGr's characters do; Enter carries a carriage return, as a keyboard's
// does, so a form's Enter submits it. Every other key types nothing.
export function keyMessage(event: KeyLike, kind: ScreenKey["event"]): ScreenKey {
  const isCharacter = [...event.key].length === 1;
  const isCommand = (event.ctrlKey || event.metaKey) && !(event.ctrlKey && event.altKey);
  const text = event.key === "Enter" ? "\r" : isCharacter && !isCommand ? event.key : "";
  return { type: "key", event: kind, key: event.key.slice(0, 32), code: event.code.slice(0, 32), key_code: Math.min(Math.max(event.keyCode, 0), 255), text: kind === "keyDown" ? text : "", modifiers: modifiersOf(event) };
}
