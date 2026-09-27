export const maxInputBytes = 64 * 1024;
export const inputBytes = (text: string) => new TextEncoder().encode(text).byteLength;

// A command for a view of a pane: typing, or, for a view that sizes the pane,
// a new size or a turn of the wheel.
export type PaneCommand =
  | { type: "terminal.input"; text: string }
  | { type: "terminal.resize"; cols: number; rows: number }
  | { type: "terminal.scroll"; direction: "up" | "down"; lines: number; source: "wheel" };

// The most lines one turn of the wheel scrolls a pane that scrolls itself.
export const MAX_WHEEL_LINES = 50;

// The only thing the wheel ever sends a pane: Herdr's own wheel scroll, which
// Herdr hands the program as a Herdr window does. It carries no text, so it
// can never type, edit or submit anything. Lines up are negative.
export function wheelScroll(lines: number): PaneCommand | null {
  if (lines === 0) return null;
  return { type: "terminal.scroll", direction: lines < 0 ? "up" : "down", lines: Math.min(Math.abs(lines), MAX_WHEEL_LINES), source: "wheel" };
}

// Claude Code's fullscreen interface draws in the terminal's alternate
// screen, so its pane keeps no scrollback: Herdr's history holds no more
// lines than the screen. Such a pane scrolls its own transcript on the wheel;
// every other pane's history is read.
export function scrollsItself(history: string, rows: number, agent: string): boolean {
  return agent === "claude" && history.replace(/(\r?\n)+$/, "").split(/\r?\n/).length <= rows;
}

// A Claude Code pane with no scrollback now may build some later, as one not
// drawn fullscreen does once its output passes a screen, so the view trusts
// that a pane scrolls itself only this long after it last read its history.
export const SELF_SCROLL_FRESH_MS = 30_000;
export function selfScrollFresh(checkedAt: number, now: number): boolean {
  return now - checkedAt < SELF_SCROLL_FRESH_MS;
}

// What a turn of the wheel does to a pane that scrolls itself: a view that
// sizes the pane scrolls it, and one that does not takes the pane first,
// unless its last take was refused, when it says why instead of asking again.
export function wheelTurn(view: { sized: boolean; refused: boolean }): "scroll" | "take" | "refused" {
  if (view.sized) return "scroll";
  return view.refused ? "refused" : "take";
}

// Adjacent printable keystrokes travel as one input; control keys, escape
// sequences and pastes stay inputs of their own, in order.
export function queueInput(queue: PaneCommand[], text: string) {
  const plain = (input: string) => !!input && [...input].every((character) => character.charCodeAt(0) >= 32 && character.charCodeAt(0) !== 127);
  const prior = queue[queue.length - 1];
  if (prior?.type === "terminal.input" && plain(prior.text) && plain(text) && inputBytes(prior.text + text) <= 4096) queue[queue.length - 1] = { type: "terminal.input", text: prior.text + text };
  else queue.push({ type: "terminal.input", text });
}

// The wheel scrolls whole lines. A delta in pixels (mode 0) adds to what
// earlier small moves left over, which a touchpad sends; one in lines (1) or
// pages (2) counts as such. Positive is down, toward the pane's bottom.
export function wheelLines(rest: number, delta: number, mode: number, rowHeight: number, rows: number): { lines: number; rest: number } {
  if (mode === 1) return { lines: Math.trunc(delta), rest: 0 };
  if (mode === 2) return { lines: Math.trunc(delta) * rows, rest: 0 };
  const total = rest + delta;
  const lines = Math.trunc(total / rowHeight);
  return { lines, rest: total - lines * rowHeight };
}

// How many lines of a pane's history the board reads when the Overlord
// scrolls back.
export const HISTORY_LINES = 3000;

// What scrolling does in a live pane view. The live screen always follows the
// pane's bottom, and Herdr never sends it history, so scrolling up opens the
// history the board reads; the history scrolls by itself, and scrolling down
// at its bottom returns to the live screen. Lines up are negative.
export function scrollAction(inHistory: boolean, atBottom: boolean, lines: number): "open" | "close" | "history" | "none" {
  if (!inHistory) return lines < 0 ? "open" : "none";
  return lines > 0 && atBottom ? "close" : "history";
}

// A pane's history as Herdr reads it, lines joined by newlines with their
// colors, written as a terminal draws it: each line from its first column,
// and no color left on after the last.
export function historyText(text: string): string {
  return text.replace(/\r?\n/g, "\r\n") + "\x1b[0m";
}

export function bracketedPaste(text: string): string {
  const input = "\x1b[200~" + text.replaceAll("\x1b[200~", "").replaceAll("\x1b[201~", "") + "\x1b[201~";
  if (inputBytes(input) > maxInputBytes) throw new Error("Paste exceeds 64 KiB. Paste a smaller selection; nothing was sent.");
  return input;
}

// A cell's size in em, before the terminal has drawn one to measure: a
// monospace cell is 0.6 em wide, and a row is about the font's own height.
export const ESTIMATED_CELL = { width: 0.6, height: 1.3 };
const MAX_FITTED_FONT = 28;

// A pane's frame keeps the pane's own columns and rows, so the font is sized
// to show the whole screen in the panel, bound by whichever of its width or
// height runs out first: nothing scrolls and nothing is cropped. The cell is
// the size in em the terminal measured. Null keeps the current font, for a
// panel or pane that has no size yet.
export function fittedFontSize(width: number, height: number, cols: number, rows: number, cell: { width: number; height: number }): number | null {
  if (width <= 0 || height <= 0 || cols <= 0 || rows <= 0) return null;
  const fits = Math.min(width / (cols * cell.width), height / (rows * cell.height));
  return Math.min(MAX_FITTED_FONT, Math.floor(fits * 2) / 2);
}

// The columns and rows that fill a panel at a font size, for a view that sizes
// its pane, within what Herdr accepts; null for a panel too small to hold a
// usable terminal, such as a hidden one. The cell is the size in em the
// terminal measured.
export function panelGrid(width: number, height: number, fontSize: number, cell: { width: number; height: number }): { cols: number; rows: number } | null {
  if (width <= 0 || height <= 0 || fontSize <= 0) return null;
  const cols = Math.min(400, Math.floor(width / (cell.width * fontSize)));
  const rows = Math.min(160, Math.floor(height / (cell.height * fontSize)));
  return cols >= 20 && rows >= 5 ? { cols, rows } : null;
}

// The grid a sized view asks for: the panel's grid whenever it differs from
// the size the pane will have, which is the size last asked for once one has
// been, since its frame may still be on its way, and else the pane's size.
// Null asks nothing.
type Grid = { cols: number; rows: number };
export function gridToAsk(panel: Grid | null, current: Grid, asked: Grid | null): Grid | null {
  const target = asked || current;
  return !panel || panel.cols === target.cols && panel.rows === target.rows ? null : panel;
}

// What happens to a pane's size in the board. A view that is open keeps the
// pane sized to its panel and live, whether or not the board's window has the
// focus: a Herdr window shows the pane at the board's size, and only closing
// the view hands the size back. A view takes the size once it is shown. Once
// another client takes the pane (held), the board waits for the Overlord to
// come back to it or type in it before taking the pane again.
export type SizeEvent = "live" | "focus" | "shown" | "typed";
export function sizeStep(event: SizeEvent, view: { sized: boolean; shown: boolean; held: boolean }): "take" | "stay" {
  if (view.sized || !view.shown) return "stay";
  if (event === "focus" || event === "typed") return "take";
  return view.held ? "stay" : "take";
}

// What a view does when one of its connections ends on its own: stop with the
// reason, show the pane afresh at its own size (observe), or keep the screen as
// it is. The connection on screen ending hands the screen on when it sized the
// pane or Herdr resized the pane, and stops the view otherwise. A take that
// fails keeps the screen. A give that fails still gives: the sized connection
// on screen ends too, so the pane gets its own size back.
export function endStep(ended: { sized: boolean; onScreen: boolean; resized: boolean }, screenSized: boolean | null): "stop" | "observe" | "keep" {
  if (ended.onScreen) return ended.sized || ended.resized ? "observe" : "stop";
  if (screenSized === null) return ended.sized ? "observe" : "stop";
  return screenSized && !ended.sized ? "observe" : "keep";
}

// Why an input was refused, in the Overlord's words.
const GATE_CUSTODY = /pipeline owns this task|pipeline custody has not been returned/i;
export function typingHeldReason(raw: string): string {
  if (GATE_CUSTODY.test(raw)) return "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen.";
  return raw;
}

// Why the wheel cannot scroll a pane whose take was refused, in the
// Overlord's words.
export function scrollHeldReason(raw: string): string {
  if (GATE_CUSTODY.test(raw)) return "A review gate owns this goblin's pane now; scroll it in Herdr.";
  return "The wheel cannot scroll this pane: " + raw;
}
