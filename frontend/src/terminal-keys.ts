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

// What a clipboard read hands on, with the HTML of the item it came from,
// and why it could not when it could not.
export type ClipboardRead = ({ text: string } | { image: File }) & { html: string } | { error: string };

// readClipboard reads what the clipboard holds for a paste the browser does
// not start itself, such as Alt+V: its text, or its picture, and the HTML
// that holds them, as a selection copied from a conversation holds its
// pictures there (#2543). The browser may refuse the read, and then the
// answer says so rather than dropping the paste (the Overlord, 2026-10-26:
// "alt + v or ctrl + c both should work for paste").
export async function readClipboard(): Promise<ClipboardRead> {
  if (!navigator.clipboard?.read) return { error: "The browser cannot read the clipboard here. Press Ctrl+V to paste." };
  try {
    const items = await navigator.clipboard.read();
    let picture: { image: File; html: string } | null = null;
    for (const item of items) {
      const html = item.types.includes("text/html") ? await (await item.getType("text/html")).text() : "";
      if (item.types.includes("text/plain")) {
        const text = await (await item.getType("text/plain")).text();
        if (text) return { text, html };
      }
      const type = item.types.find((each) => each.startsWith("image/"));
      if (type && !picture) picture = { image: new File([await item.getType(type)], "pasted.png", { type }), html };
    }
    if (picture) return picture;
    return { error: "The clipboard holds nothing to paste." };
  } catch {
    return { error: "The browser refused to read the clipboard. Press Ctrl+V to paste." };
  }
}
