import { test } from "node:test";
import assert from "node:assert/strict";
import type { Terminal } from "@xterm/xterm";
import { clipboardInput, terminalKey } from "./terminal-keys.ts";

const terminal = { hasSelection: () => false, clearSelection: () => {} } as unknown as Terminal;

const press = (key: string, modifiers: { ctrlKey?: boolean; shiftKey?: boolean } = {}) => {
  const event = { type: "keydown", key, ctrlKey: false, shiftKey: false, altKey: false, metaKey: false, preventDefault: () => {}, ...modifiers } as unknown as KeyboardEvent;
  return terminalKey(event, terminal, () => {});
};

const paste = (text: string) => ({ clipboardData: { getData: (type: string) => type === "text/plain" ? text : "" } }) as unknown as ClipboardEvent;

for (const [harness, key] of [
  ["claude", "\x1bv"], ["pi", "\x1bv"],
  ["codex", "\x16"], ["bash", "\x16"], ["powershell", "\x16"], ["", "\x16"],
]) {
  test("Ctrl+V with only an image sends the key " + (harness || "an unknown harness") + " attaches an image on", () => {
    assert.equal(press("v", { ctrlKey: true }), false, "the browser's paste event carries the clipboard");

    const input = clipboardInput(paste(""), harness);

    assert.deepEqual(input, { key });
  });

  test("Ctrl+V with text pastes the text into " + (harness || "an unknown harness"), () => {
    press("v", { ctrlKey: true });

    const input = clipboardInput(paste("one\ntwo"), harness);

    assert.deepEqual(input, { text: "one\ntwo" });
  });
}

test("Ctrl+Shift+V or a menu paste with only an image types nothing", () => {
  press("v", { ctrlKey: true, shiftKey: true });
  assert.equal(clipboardInput(paste(""), "claude"), null, "Ctrl+Shift+V");

  assert.equal(clipboardInput(paste(""), "claude"), null, "a paste from the browser's menu, with no key before it");
});

test("a paste key is spent by the paste it started", () => {
  press("v", { ctrlKey: true });
  clipboardInput(paste(""), "claude");

  assert.equal(clipboardInput(paste(""), "claude"), null);
});

// A paste that holds a picture and no text, on a board at hostname.
const pastePicture = (hostname: string) => {
  Object.assign(globalThis, { location: { hostname } });
  const picture = new File([new Uint8Array([137, 80, 78, 71])], "screenshot.png", { type: "image/png" });
  return { event: { clipboardData: { getData: () => "", files: [picture] } } as unknown as ClipboardEvent, picture };
};

for (const hostname of ["pc.example.ts.net", "192.168.1.20"]) {
  test("a picture pasted on a board opened on another device, at " + hostname + ", is handed on whole, with or without Ctrl+V", () => {
    for (const isKey of [true, false]) {
      // Arrange
      const { event, picture } = pastePicture(hostname);
      if (isKey) press("v", { ctrlKey: true });

      // Act
      const input = clipboardInput(event, "claude");

      // Assert
      assert.deepEqual(input, { image: picture });
    }
  });
}

for (const hostname of ["127.0.0.1", "localhost", "[::1]"]) {
  test("a picture pasted with Ctrl+V on this PC's own board, at " + hostname + ", sends only the image key, as before", () => {
    // Arrange
    const { event } = pastePicture(hostname);
    press("v", { ctrlKey: true });

    // Act
    const input = clipboardInput(event, "claude");

    // Assert
    assert.deepEqual(input, { key: "\x1bv" });
  });
}
