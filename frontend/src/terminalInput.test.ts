import { test } from "node:test";
import assert from "node:assert/strict";
import { bracketedPaste, inputBytes, maxInputBytes } from "./terminalInput.ts";

test("Unicode paste uses UTF-8 bytes including one complete bracketed wrapper", () => {
  const limit = Math.floor((maxInputBytes - 12) / 3);
  const accepted = bracketedPaste("界".repeat(limit));
  assert.ok(inputBytes(accepted) <= maxInputBytes);
  assert.ok(accepted.startsWith("\x1b[200~") && accepted.endsWith("\x1b[201~"));
  assert.throws(() => bracketedPaste("界".repeat(limit + 1)), /nothing was sent/);
  assert.throws(() => bracketedPaste("🙂".repeat(16382)), /nothing was sent/);
  assert.equal(bracketedPaste("one\n\x1b[201~two"), "\x1b[200~one\ntwo\x1b[201~");
});
