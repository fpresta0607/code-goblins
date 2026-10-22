import type { Terminal } from "@xterm/xterm";
import { onThisMachine } from "./credentials.ts";

let isPasteKey = false;

export function terminalKey(event: KeyboardEvent, terminal: Terminal, copy: () => void): boolean | null {
  if (event.type === "keydown") isPasteKey = event.ctrlKey && !event.shiftKey && !event.altKey && !event.metaKey && event.key.toLowerCase() === "v";
  if (event.ctrlKey && !event.altKey && !event.metaKey) {
    // Keep the browser's paste event; xterm would send Ctrl+V as SYN.
    if (event.key.toLowerCase() === "v") return false;
    if (event.key.toLowerCase() === "c" && (event.shiftKey || terminal.hasSelection())) {
      event.preventDefault();
      if (event.type === "keydown") {
        copy();
        if (!event.shiftKey) terminal.clearSelection();
      }
      return false;
    }
  }
  return null;
}

// The key the terminal's harness attaches this PC's clipboard image on. In a
// Windows pseudo console Claude Code and pi take that as Alt+V only, and Codex
// as Ctrl+V; any other program gets the SYN that xterm sends for Ctrl+V.
export function imageKey(harness: string): string {
  return harness === "claude" || harness === "pi" ? "\x1bv" : "\x16";
}

// What a clipboard paste types: its text, or for a Ctrl+V with no text, such as
// an image, the key the terminal's harness attaches the clipboard's image on.
// A board opened on another device, such as his phone, holds a pasted image in
// that device's clipboard, not this PC's, so the image itself is handed on, to
// be put on this PC's clipboard before the key.
export function clipboardInput(event: ClipboardEvent, harness: string): { text: string } | { key: string } | { image: File } | null {
  const text = event.clipboardData?.getData("text/plain");
  const isKey = isPasteKey;
  isPasteKey = false;
  if (text) return { text };
  const image = Array.from(event.clipboardData?.files ?? []).find((file) => file.type.startsWith("image/"));
  if (image && !onThisMachine(location.hostname)) return { image };
  if (!isKey) return null;
  return { key: imageKey(harness) };
}
