import type { Terminal } from "@xterm/xterm";

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

// What a clipboard paste types: its text, or for a Ctrl+V with no text, such as
// an image, the key the terminal's harness attaches the clipboard's image on.
// In a Windows pseudo console Claude Code and pi take that as Alt+V only, and
// Codex as Ctrl+V; any other program gets the SYN that xterm sends for Ctrl+V.
export function clipboardInput(event: ClipboardEvent, harness: string): { text: string } | { key: string } | null {
  const text = event.clipboardData?.getData("text/plain");
  const isKey = isPasteKey;
  isPasteKey = false;
  if (text) return { text };
  if (!isKey) return null;
  return { key: harness === "claude" || harness === "pi" ? "\x1bv" : "\x16" };
}
