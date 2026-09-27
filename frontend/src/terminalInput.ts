export const maxInputBytes = 64 * 1024;
export const inputBytes = (text: string) => new TextEncoder().encode(text).byteLength;

// A command for a live pane view, which only types.
export type PaneCommand = { type: "terminal.input"; text: string };

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

// Why an input was refused, in the Overlord's words.
export function typingHeldReason(raw: string): string {
  if (/pipeline owns this task|pipeline custody has not been returned/i.test(raw)) return "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen.";
  return raw;
}
