import { typingHeldReason } from "./terminalInput.ts";

// A native terminal's view protocol: the host's output arrives as binary
// messages, which the view acknowledges once xterm has consumed them, and the
// terminal's size as a text message; typing goes back as binary messages.

export const DEFAULT_FONT_SIZE = 20;
export const MIN_FONT_SIZE = 12;
export const MAX_FONT_SIZE = 28;
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
