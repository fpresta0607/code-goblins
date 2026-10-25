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

test("a saved chat style reads as Classic only when it says Classic", () => {
  assert.equal(chatStyle(null), "modern", "a session the board reads as messages opens on its conversation");
  assert.equal(chatStyle("classic"), "classic");
  assert.equal(chatStyle("anything else"), "modern");
});

test("the board keeps the chosen chat style for this browser", () => {
  // Arrange
  const entries = new Map<string, string>();
  browser(entries);

  // Act and Assert
  assert.equal(storedChatStyle(), "modern", "Modern is the choice before he makes one");
  saveChatStyle("classic");
  assert.equal(entries.get(CHAT_STYLE_KEY), "classic");
  assert.equal(storedChatStyle(), "classic", "the choice survives a reload");
  saveChatStyle("modern");
  assert.equal(storedChatStyle(), "modern");
});

test("a browser that keeps no storage still draws a conversation", () => {
  // Arrange
  browser(null);

  // Act and Assert
  assert.equal(storedChatStyle(), "modern");
  saveChatStyle("classic");
  assert.equal(storedChatStyle(), "modern", "nothing is saved where the browser refuses storage");
});
