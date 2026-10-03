import { typingHeldReason } from "./terminalInput.ts";

// A native terminal's view protocol: the host's output arrives as binary
// messages, which the view acknowledges once xterm has consumed them, and the
// terminal's size as a text message; typing goes back as binary messages.

export const DEFAULT_FONT_SIZE = 20;
export const MIN_FONT_SIZE = 12;
export const MAX_FONT_SIZE = 28;
// Every terminal the board sizes draws at the one text size the Overlord last
// chose, by its keys, the wheel or the panel's buttons, remembered in this
// browser.
const FONT_KEY = "cfo-terminal-font-size";
const followers = new Set<(size: number) => void>();

export function storedFontSize(): number {
  try {
    const size = Number(localStorage.getItem(FONT_KEY));
    return size >= MIN_FONT_SIZE && size <= MAX_FONT_SIZE ? size : DEFAULT_FONT_SIZE;
  } catch { return DEFAULT_FONT_SIZE; }
}

// storeFontSize remembers the size and tells every terminal on the page, so
// they all draw at it at once, wherever it was chosen.
export function storeFontSize(size: number): void {
  try { localStorage.setItem(FONT_KEY, String(size)); } catch { /* the size still applies to this page */ }
  for (const follow of [...followers]) follow(size);
}

// followFontSize calls follow with each size chosen from now on, until the
// function it returns is called.
export function followFontSize(follow: (size: number) => void): () => void {
  followers.add(follow);
  return () => { followers.delete(follow); };
}
// Output is acknowledged in steps of this many bytes, or at once when xterm
// has caught up with everything received.
export const ACK_STEP = 64 * 1024;
// One input message stays far inside the relay's 1 MiB read limit.
export const INPUT_MESSAGE = 64 * 1024;
// The smallest terminal the relay accepts; a smaller panel is a hidden one.
const MIN_COLS = 20;
const MIN_ROWS = 5;

export function ackDue(consumed: number, acknowledged: number, written: number): boolean {
  return consumed > acknowledged && (consumed - acknowledged >= ACK_STEP || consumed === written);
}

export function inputMessages(bytes: Uint8Array): Uint8Array[] {
  const pieces: Uint8Array[] = [];
  for (let start = 0; start < bytes.length; start += INPUT_MESSAGE) pieces.push(bytes.subarray(start, start + INPUT_MESSAGE));
  return pieces;
}

// Ctrl with plus or minus steps the font size, and Ctrl+0 restores it.
export function fontSizeFor(key: string, current: number): number | null {
  if (key === "=" || key === "+") return Math.min(MAX_FONT_SIZE, current + 1);
  if (key === "-") return Math.max(MIN_FONT_SIZE, current - 1);
  if (key === "0") return DEFAULT_FONT_SIZE;
  return null;
}

// How far the wheel travels for one step of the font size: one notch of a
// mouse wheel, in the pixels a browser reports it as, or three of its lines.
const WHEEL_STEP = 100;

// The wheel with Ctrl held steps the font size, away from him larger and
// toward him smaller, and so does a pinch, which a browser reports as that
// wheel in many small moves: rest is what earlier moves left over, so they
// add up to a step. A delta in lines (mode 1) or pages (2) counts as such.
export function wheelFontSize(rest: number, delta: number, mode: number, current: number): { size: number; rest: number } {
  const travel = rest + (mode === 1 ? delta * WHEEL_STEP / 3 : mode === 2 ? delta * WHEEL_STEP : delta);
  const steps = Math.trunc(travel / WHEEL_STEP);
  return { size: Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, current - steps)), rest: travel - steps * WHEEL_STEP };
}

export function parseSize(text: string): { cols: number; rows: number } | null {
  try {
    const value: unknown = JSON.parse(text);
    if (typeof value !== "object" || value === null) return null;
    const { type, cols, rows } = value as Record<string, unknown>;
    return type === "size" && typeof cols === "number" && typeof rows === "number" && cols > 0 && rows > 0 ? { cols, rows } : null;
  } catch {
    return null;
  }
}

// The relay's first message says how many of the output bytes that follow
// replay the terminal's history; what comes after them is live.
export function parseHistory(text: string): number | null {
  try {
    const value: unknown = JSON.parse(text);
    if (typeof value !== "object" || value === null) return null;
    const { type, bytes } = value as Record<string, unknown>;
    return type === "history" && typeof bytes === "number" && Number.isInteger(bytes) && bytes >= 0 ? bytes : null;
  } catch {
    return null;
  }
}

export function usableSize(cols: number, rows: number): boolean {
  return cols >= MIN_COLS && rows >= MIN_ROWS;
}

// A view measures its cell from xterm's screen, which matches the grid only
// once xterm has drawn it after a resize; out of sight, xterm draws, and so
// resizes its screen, only once shown. A claim while the screen is not drawn
// waits for the draw and is made then. A resize alone claims nothing, so a
// view drawing another view's size keeps it until typed into.
export interface FitState { isDrawn: boolean; isClaimPending: boolean }
export type FitEvent = "resize" | "claim" | "draw";

export function nextFit(state: FitState, event: FitEvent): FitState & { isClaim: boolean } {
  if (event === "resize") return { isDrawn: false, isClaimPending: state.isClaimPending, isClaim: false };
  if (event === "claim") return state.isDrawn ? { ...state, isClaim: true } : { isDrawn: false, isClaimPending: true, isClaim: false };
  return { isDrawn: true, isClaimPending: false, isClaim: state.isClaimPending };
}

// The grid a panel holds at a cell size, evenly padded: the spare width is
// split between left and right, the bottom keeps the same space, so the last
// row, the input line, sits that far from the bottom, and the spare height,
// under a row, goes above the grid.
export function panelFit(width: number, height: number, cell: { width: number; height: number }, padding: number): { cols: number; rows: number; left: number; right: number; top: number; bottom: number } | null {
  if (cell.width <= 0 || cell.height <= 0) return null;
  const cols = Math.floor((width - 2 * padding) / cell.width);
  const side = (width - cols * cell.width) / 2;
  const rows = Math.floor((height - 2 * side) / cell.height);
  if (!usableSize(cols, rows)) return null;
  return { cols, rows, left: side, right: side, top: height - side - rows * cell.height, bottom: side };
}

// A view that fell behind, a restarting board and a dropped connection come
// back on their own; an ended terminal, a replaced task and a refusal wait
// for Reconnect.
export function reconnects(code: number): boolean {
  return code === 1013 || code === 1001 || code === 1006;
}

export function closedReason(code: number, reason: string): string {
  if (!reason) return code === 1006 ? "The connection to the board dropped." : "The terminal view closed.";
  return typingHeldReason(reason);
}
