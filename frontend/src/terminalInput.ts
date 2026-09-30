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

// Herdr hands the program one wheel notch for each scroll, so turns cannot be
// joined into one. A wheel faster than the board can send its scrolls keeps at
// most MAX_WAITING_SCROLLS waiting behind the one on its way and drops the
// rest, so the pane stops soon after the wheel does; a turn the other way
// replaces the waiting ones. Typing and sizes keep their places, and so do the
// scrolls before them.
export const MAX_WAITING_SCROLLS = 2;
export function queueScroll(queue: PaneCommand[], command: PaneCommand) {
  let first = queue.length;
  while (first > 0 && queue[first - 1].type === "terminal.scroll") first--;
  const last = queue[queue.length - 1];
  if (last?.type === "terminal.scroll" && command.type === "terminal.scroll" && last.direction !== command.direction) queue.splice(first);
  if (queue.length - first < MAX_WAITING_SCROLLS) queue.push(command);
}

// Ctrl+End, the key Claude Code's fullscreen interface jumps to its newest
// output on, as its own "Jump to bottom" note says.
export const JUMP_TO_BOTTOM = "\x1b[1;5F";

// How many notches the board has scrolled a pane that scrolls itself up from
// its bottom. Herdr hands the program one wheel notch for each scroll, however
// many lines it names, so a scroll up adds one, a scroll down takes one away,
// and Ctrl+End clears them.
export function scrolledUp(prior: number, command: PaneCommand): number {
  if (command.type === "terminal.scroll") return Math.max(0, prior + (command.direction === "up" ? 1 : -1));
  if (command.type === "terminal.input" && command.text.includes(JUMP_TO_BOTTOM)) return 0;
  return prior;
}

// The screen of a pane that scrolls itself is drawn from Herdr's frames, which
// carry no mouse modes, so a click never reaches the program and its "Jump to
// bottom" note cannot be pressed. A plain click on a pane the board scrolled up
// jumps it to the bottom instead; a drag, a selection or another button does
// not, and neither does a view that could not scroll the pane itself now, such
// as one whose pane a review gate took, since typing there would ask for it.
// The count can run ahead of the pane, which stops at its top, and a jump at
// the bottom only moves Claude Code's cursor to the end of its input.
const CLICK_SLOP = 4;
export function clickJumps(scrolled: number, click: { button: number; moved: number; selected: boolean }, view: { sized: boolean; refused: boolean }): boolean {
  return scrolled > 0 && click.button === 0 && click.moved < CLICK_SLOP && !click.selected && view.sized && !view.refused;
}

// A click's jump waits out the double-click interval and a further press
// cancels it, so a double or triple click selects the word or line it aimed at
// before the pane moves. Whether it jumps is decided when it would go.
export const CLICK_JUMP_WAIT_MS = 300;
export function clickJumper(jump: () => void) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const cancel = () => clearTimeout(timer);
  return {
    cancel,
    released(jumps: () => boolean) {
      cancel();
      timer = setTimeout(() => { if (jumps()) jump(); }, CLICK_JUMP_WAIT_MS);
    },
  };
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

// The lines a view reads to judge whether a pane scrolls itself: two screens
// and one line tell a pane with no scrollback from one with some exactly as its
// whole history does, blank rows below the screen's last text included, and
// read in a moment.
export function judgeLines(rows: number): number {
  return Math.min(HISTORY_LINES, 2 * rows + 1);
}

// What a turn of the wheel over the live screen does with what the view last
// judged of the pane (at is when, 0 for not yet, and rows the grid it was
// judged against): a pane that scrolls itself scrolls at once, and one with
// history opens it on a turn up, which reads it whole and judges again. A
// stale judgment is looked at again beside the scroll, never before it; only
// turns before the first judgment at the screen's grid wait for it, since a
// pane asked for a new grid as it was read may have answered at that one.
export function liveWheel(judged: { at: number; selfScrolls: boolean; rows: number }, screen: { now: number; rows: number }, lines: number): { action: "scroll" | "open" | "wait" | "none"; look: boolean } {
  if (!judged.at || judged.rows !== screen.rows) return { action: "wait", look: true };
  if (judged.selfScrolls) return { action: "scroll", look: !selfScrollFresh(judged.at, screen.now) };
  return { action: lines < 0 ? "open" : "none", look: false };
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
  const bytes = new TextEncoder().encode(text);
  if (bytes.length > maxInputBytes) {
    const decoder = new TextDecoder();
    const last = text.endsWith("\x1b[201~") ? bytes.length - 6 : bytes.length;
    for (let start = 0; start < bytes.length;) {
      let end = Math.min(start + maxInputBytes, bytes.length);
      if (end > last && end < bytes.length) end = last;
      while (end < bytes.length && (bytes[end] & 0xc0) === 0x80) end--;
      queue.push({ type: "terminal.input", text: decoder.decode(bytes.subarray(start, end)) });
      start = end;
    }
    return;
  }
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

// While the panel's divider is dragged a terminal keeps its grid and shows its
// screen scaled whole into the panel, so a move costs no refit and no resize
// of the pane; it refits once when the drag ends (PANEL_RESIZED).
export function previewScale(room: { width: number; height: number }, screen: { width: number; height: number }): number {
  if (room.width <= 0 || room.height <= 0 || screen.width <= 0 || screen.height <= 0) return 1;
  return Math.min(room.width / screen.width, room.height / screen.height);
}

// The window event the panel's divider sends once a drag ends.
export const PANEL_RESIZED = "board-panel-resized";

// Pasted text keeps no escape, so it cannot end its paste early and type the
// rest as keys.
export function stripPasteEscapes(text: string): string {
  // eslint-disable-next-line no-control-regex
  return text.replace(/\x1b(?:\[20[01]~)?/g, "");
}

export function bracketedPaste(text: string): string {
  const paste = "\x1b[200~" + stripPasteEscapes(text).replace(/\r?\n/g, "\r") + "\x1b[201~";
  // Match Go's JSON escaping and leave room for Herdr's request envelope.
  const encoded = JSON.stringify(paste).replace(/[<>&\u2028\u2029]/g, (character) => "\\u" + character.charCodeAt(0).toString(16).padStart(4, "0"));
  if (inputBytes(encoded) > 1024 * 1024 - 1024) throw new Error("Paste exceeds Herdr's encoded request limit. Paste a smaller selection; nothing was sent.");
  return paste;
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
