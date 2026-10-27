import { test } from "node:test";
import assert from "node:assert/strict";
import { mentionAt, mentionFile, parseFiles } from "./mentions.ts";

test("an @ the caret stands in opens the file list, and one inside a word or after a space does not", () => {
  const cases: [string, number, ReturnType<typeof mentionAt>][] = [
    ["@", 1, { start: 0, query: "" }],
    ["read @src/term", 14, { start: 5, query: "src/term" }],
    ["read @src/term and", 14, { start: 5, query: "src/term" }],
    ["line one\n@app", 13, { start: 9, query: "app" }],
    ["mail me at me@home", 18, null],
    ["read @src ", 10, null],
    ["read @src/term", 9, { start: 5, query: "src" }],
    ["", 0, null],
  ];
  for (const [text, caret, want] of cases) assert.deepEqual(mentionAt(text, caret), want, JSON.stringify([text, caret]));
});

test("a chosen file takes the mention's place as @path and a space, keeping the rest of the draft", () => {
  const cases: [string, number, string, { text: string; caret: number }][] = [
    ["read @term", 10, "frontend/src/terminalView.ts", { text: "read @frontend/src/terminalView.ts ", caret: 35 }],
    ["read @term and fix it", 10, "a.ts", { text: "read @a.ts and fix it", caret: 11 }],
    ["read @te and fix it", 8, "a.ts", { text: "read @a.ts and fix it", caret: 11 }],
    ["@", 1, "b.go", { text: "@b.go ", caret: 6 }],
  ];
  for (const [text, caret, path, want] of cases) {
    const mention = mentionAt(text, caret);
    assert.ok(mention, text);
    assert.deepEqual(mentionFile(text, mention, caret, path), want, text);
  }
});

test("the session's file list parses, and a malformed one is refused", () => {
  assert.deepEqual(parseFiles({ files: ["a.ts", "", "docs/b.md"] }), ["a.ts", "docs/b.md"]);
  assert.deepEqual(parseFiles({}), []);
  for (const value of [{ files: [1] }, { files: "a.ts" }, null]) assert.throws(() => parseFiles(value), JSON.stringify(value));
});
