import test from "node:test";
import assert from "node:assert/strict";

import { CHAT_STYLE_KEY, chatStyle, saveChatStyle, storedChatStyle } from "./chat-style.ts";

// A store standing in for the browser's, so the preference can be read and
// written without one.
function browser(entries: Map<string, string> | null): void {
  (globalThis as unknown as { localStorage?: unknown }).localStorage = entries && {
    getItem: (key: string) => entries.get(key) ?? null,
    setItem: (key: string, value: string) => void entries.set(key, value),
  };
}

test("a saved chat style reads as Modern only when it says Modern", () => {
  assert.equal(chatStyle(null), "classic", "a conversation opens on the harness's own terminal");
  assert.equal(chatStyle("modern"), "modern");
  assert.equal(chatStyle("anything else"), "classic");
});

test("the board keeps the chosen chat style for this browser", () => {
  // Arrange
  const entries = new Map<string, string>();
  browser(entries);

  // Act and Assert
  assert.equal(storedChatStyle(), "classic", "Classic is the choice before he makes one");
  saveChatStyle("modern");
  assert.equal(entries.get(CHAT_STYLE_KEY), "modern");
  assert.equal(storedChatStyle(), "modern", "the choice survives a reload");
  saveChatStyle("classic");
  assert.equal(storedChatStyle(), "classic");
});

test("a browser that keeps no storage still draws a conversation", () => {
  // Arrange
  browser(null);

  // Act and Assert
  assert.equal(storedChatStyle(), "classic");
  saveChatStyle("modern");
  assert.equal(storedChatStyle(), "classic", "nothing is saved where the browser refuses storage");
});
