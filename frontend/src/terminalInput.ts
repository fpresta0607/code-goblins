export const maxInputBytes = 64 * 1024;
export const inputBytes = (text: string) => new TextEncoder().encode(text).byteLength;

// Adjacent printable keystrokes travel as one input; control keys, escape
// sequences and pastes stay inputs of their own, in order.
export function queueInput(queue: string[], text: string) {
  const plain = (input: string) => !!input && [...input].every((character) => character.charCodeAt(0) >= 32 && character.charCodeAt(0) !== 127);
  const prior = queue[queue.length - 1];
  if (prior !== undefined && plain(prior) && plain(text) && inputBytes(prior + text) <= 4096) queue[queue.length - 1] = prior + text;
  else queue.push(text);
}

export function bracketedPaste(text: string): string {
  const input = "\x1b[200~" + text.replaceAll("\x1b[200~", "").replaceAll("\x1b[201~", "") + "\x1b[201~";
  if (inputBytes(input) > maxInputBytes) throw new Error("Paste exceeds 64 KiB. Paste a smaller selection; nothing was sent.");
  return input;
}

// A pane's frame keeps the pane's own columns, so the font shrinks to fit them
// across the panel (a monospace cell is 0.6 em wide), but never below 12 px:
// a wider pane scrolls sideways instead of turning unreadable, and is never
// cropped.
export function fittedFontSize(width: number, cols: number): number {
  if (cols <= 0) return 15;
  return Math.max(12, Math.min(15, Math.floor(width / (cols * 0.6) * 2) / 2));
}

// Why an input was refused, in the Overlord's words.
export function typingHeldReason(raw: string): string {
  if (/pipeline owns this task|pipeline custody has not been returned/i.test(raw)) return "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen.";
  return raw;
}
