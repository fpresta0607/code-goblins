import { MAX_FONT_SIZE, MIN_FONT_SIZE } from "./terminalStream.ts";

// A wheel notch turns about 100 px; a smooth wheel or a touchpad sends smaller
// turns, which add up to a step at half a notch. A wheel that counts in lines
// or pages, any delta mode but pixels, steps once a turn.
const STEP_PX = 50;
const PIXEL_MODE = 0;
const RIGHT_BUTTON = 2;

// The right mouse button held while the wheel turns zooms a terminal's text,
// as the Overlord asked on 2026-10-08: "in terminals there should be a right
// click hold +scroll to zoom font". Up grows the text a step and down shrinks
// it, the steps Ctrl+Plus and Ctrl+Minus take, and the wheel then neither
// scrolls the terminal nor reaches its program. A press that zoomed opens no
// menu when it ends, so the menu's Paste is never offered, and a right click
// that never met the wheel is left exactly as it was. The press listens on the
// terminal's element ahead of xterm, and its release on the window, since the
// button can come up anywhere. It returns what detaches it.
export function attachZoomGesture(element: EventTarget, view: EventTarget, size: () => number, zoom: (size: number) => void): () => void {
  let isHeld = false, hasZoomed = false, rest = 0;
  const press = (event: Event) => {
    if ((event as MouseEvent).button !== RIGHT_BUTTON) return;
    isHeld = true;
    hasZoomed = false;
    rest = 0;
  };
  const release = (event: Event) => { if ((event as MouseEvent).button === RIGHT_BUTTON) isHeld = false; };
  const leave = () => { isHeld = false; };
  const wheel = (event: Event) => {
    if (!isHeld) return;
    event.preventDefault();
    event.stopPropagation();
    hasZoomed = true;
    const { deltaY, deltaMode } = event as WheelEvent;
    const turn = deltaMode === PIXEL_MODE ? deltaY : Math.sign(deltaY) * STEP_PX;
    rest = Math.sign(rest) === -Math.sign(turn) ? turn : rest + turn;
    if (Math.abs(rest) < STEP_PX) return;
    const current = size();
    const next = Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, current - Math.sign(rest)));
    rest = 0;
    if (next !== current) zoom(next);
  };
  const menu = (event: Event) => {
    if (!hasZoomed) return;
    hasZoomed = false;
    event.preventDefault();
    event.stopPropagation();
  };
  const listening = new AbortController();
  const { signal } = listening;
  element.addEventListener("mousedown", press, { capture: true, signal });
  element.addEventListener("wheel", wheel, { capture: true, passive: false, signal });
  element.addEventListener("contextmenu", menu, { capture: true, signal });
  view.addEventListener("mouseup", release, { capture: true, signal });
  view.addEventListener("blur", leave, { signal });
  return () => listening.abort();
}
