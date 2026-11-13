import { test } from "node:test";
import assert from "node:assert/strict";
import { commandLines, exitResult, plainTerminalText, softBreaks, terminalLines, type TerminalSpan } from "./terminal-text.ts";

const ESC = "\x1b";
const texts = (text: string) => terminalLines(text).map((line) => line.spans.map((span) => span.text).join(""));
const tones = (text: string) => terminalLines(text).map((line) => line.tone);

test("a long line stays one line, and a long word in it may wrap only after one of its separators", () => {
  // Arrange
  const path = "D:\\work\\repository\\internal\\supervisor\\messages_windows_test.go";
  const line = "panic: open " + path + ": The system cannot find the path specified, after 3 tries of the same read and one more try of its folder";

  // Act
  const lines = terminalLines(line);
  const pieces = softBreaks(path);

  // Assert
  assert.equal(lines.length, 1);
  assert.equal(lines[0].spans.map((span) => span.text).join(""), line);
  assert.equal(pieces.join(""), path, "the pieces are the word, whole");
  assert.ok(pieces.length > 4, "a path offers a break after each folder");
  for (const piece of pieces.slice(0, -1)) assert.match(piece, /[\\/_.:]$/, piece + " ends in a separator");
});

for (const [name, word] of [["a short word", "internal/spawn"], ["a long word with no separator", "0123456789abcdef0123456789abcdef0123456789abcdef"]]) {
  test(name + " offers no break inside it", () => {
    // Act and Assert
    assert.deepEqual(softBreaks("see " + word + " there"), ["see " + word + " there"]);
  });
}

test("a long word among others keeps the words around it in its pieces", () => {
  // Arrange
  const text = "wrote frontend/src/components/terminal/terminal-block.tsx and left";

  // Act
  const pieces = softBreaks(text);

  // Assert
  assert.equal(pieces.join(""), text);
  assert.equal(pieces[0], "wrote frontend/");
  assert.equal(pieces.at(-1), "tsx and left");
});

const coloured: [string, string, TerminalSpan[]][] = [
  ["a basic colour until its reset", `${ESC}[31mFAIL${ESC}[0m done`, [{ text: "FAIL", color: "red" }, { text: " done" }]],
  ["bold with a colour in one code", `${ESC}[1;32m3 passed${ESC}[m`, [{ text: "3 passed", color: "green", isBold: true }]],
  ["a bright colour as its own hue", `${ESC}[93mwarn${ESC}[39m ok`, [{ text: "warn", color: "yellow" }, { text: " ok" }]],
  ["black and bright black as gray", `${ESC}[30ma${ESC}[90mb${ESC}[0m`, [{ text: "ab", color: "gray" }]],
  ["white as the text's own colour", `${ESC}[37ma${ESC}[97mb${ESC}[0m`, [{ text: "ab" }]],
  ["one of the 256 colours by its hue", `${ESC}[38;5;196mred${ESC}[38;5;46mgreen${ESC}[38;5;244mgray${ESC}[0m`, [{ text: "red", color: "red" }, { text: "green", color: "green" }, { text: "gray", color: "gray" }]],
  ["a true colour by its hue", `${ESC}[38;2;90;160;255mblue${ESC}[38;2;240;200;60myellow${ESC}[0m`, [{ text: "blue", color: "blue" }, { text: "yellow", color: "yellow" }]],
  ["dim, italic and underlined text", `${ESC}[2ma${ESC}[22;3mb${ESC}[23;4mc${ESC}[24md`, [{ text: "a", isDim: true }, { text: "b", isItalic: true }, { text: "c", isUnderlined: true }, { text: "d" }]],
  ["a background colour as no colour", `${ESC}[41;97m ERROR ${ESC}[0m`, [{ text: " ERROR " }]],
];
for (const [name, text, spans] of coloured) {
  test("colour codes draw " + name + " and leave no escape behind", () => {
    // Act
    const lines = terminalLines(text);

    // Assert
    assert.deepEqual(lines.map((line) => line.spans), [spans]);
  });
}

test("a window title, a cursor move and a control character are dropped, and Copy takes the text without them", () => {
  // Arrange
  const text = `${ESC}]0;npm test${"\x07"}${ESC}[?25l${ESC}[2Kbuilding${ESC}[1A\x00\n${ESC}]8;;https://example.com${ESC}\\link${ESC}]8;;${ESC}\\ ${ESC}[32mdone${ESC}[0m\n`;

  // Act and Assert
  assert.deepEqual(texts(text), ["building", "link done"]);
  assert.equal(plainTerminalText(text), "building\nlink done");
});

const redrawn: [string, string, string[]][] = [
  ["a progress line", "Downloading 10%\rDownloading 50%\rDownloading 100%\n", ["Downloading 100%"]],
  ["a shorter redraw over a longer line", "abcdef\rXY", ["XYcdef"]],
  ["a redraw that erases the rest", `resolving packages\rdone${ESC}[K\n`, ["done"]],
  ["a Windows line ending", "one\r\ntwo\r\n", ["one", "two"]],
  ["a backspace", "ab\bc", ["ac"]],
];
for (const [name, text, lines] of redrawn) {
  test("a line its program redraws shows as its terminal left it: " + name, () => {
    // Act and Assert
    assert.deepEqual(texts(text), lines);
  });
}

test("a blank line between two lines is kept, and the line break that ends the text adds none", () => {
  // Act and Assert
  assert.deepEqual(texts("one\n\ntwo\n"), ["one", "", "two"]);
  assert.deepEqual(terminalLines(""), []);
});

test("a multi-line command reads as its lines in order, each with its own indent, without the blank lines at its ends", () => {
  // Arrange
  const command = "\r\ngh release create v0.5.2 `\r\n    --notes-file notes.md `\r\n    --title \"v0.5.2\"\r\nif ($LASTEXITCODE) { exit 1 }\r\n\r\n";

  // Act
  const lines = commandLines(command);

  // Assert
  assert.deepEqual(lines, ["gh release create v0.5.2 `", "    --notes-file notes.md `", "    --title \"v0.5.2\"", "if ($LASTEXITCODE) { exit 1 }"]);
  assert.deepEqual(commandLines("npm test"), ["npm test"]);
});

// The card of a command he ran never shows its exit code (2026-10-08: "just
// have the same complete notification"); History keeps it.
test("an error exit reads Failed as an error, a clean exit Completed as a success, neither with its code, and a command that never exited says nothing", () => {
  // Act and Assert
  assert.deepEqual(exitResult(2), { tone: "error", text: "Failed" });
  assert.deepEqual(exitResult(0), { tone: "success", text: "Completed" });
  assert.equal(exitResult(null), null);
});

test("the lines that say why a command failed read as errors, and the lines around them stay plain", () => {
  // Arrange
  const output = "Uploading assets\ngh: release v0.5.2 already exists\nError: Process completed with exit code 2.\n";

  // Act and Assert
  assert.deepEqual(tones(output), ["", "", "error"]);
});

const toned: [string, string][] = [
  ["error: could not compile", "error"], ["ERROR in ./src/app.ts", "error"], ["fatal: not a git repository", "error"], ["--- FAIL: TestSendNow (0.02s)", "error"],
  ["FAIL\tgithub.com/example/project/internal/spawn\t0.4s", "error"], ["panic: runtime error: index out of range", "error"], ["npm ERR! code ELIFECYCLE", "error"],
  ["npm error code 1", "error"], ["Traceback (most recent call last):", "error"], ["  ✗ a queued command stays held", "error"], ["Failed to connect to the host", "error"],
  ["warning: unused variable", "warning"], ["WARN deprecated package", "warning"], ["npm warn config", "warning"],
  ["ok  \tgithub.com/example/project/internal/spawn\t6.6s", "success"], ["--- PASS: TestSendNow (0.02s)", "success"], ["PASS", "success"], ["  ✓ a queued command runs", "success"],
  ["Success: the release is published", "success"], ["Done in 4.2s", "success"],
  ["⚠️ the notes have no heading", "warning"], ["✔️ published", "success"], ["✖️ not published", "error"],
  ["0 failed, 12 passed", ""], ["errors: 0", ""], ["the error was handled", ""], ["okay then", ""], ["passing 12", ""], ["", ""],
  // A line of source a command printed is no outcome: its word is a name.
  ["panic(err)", ""], ["error.message = text", ""], ["  fail(\"never reached\")", ""], ["warn(message)", ""], ["ok, err := run()", ""], ["done()", ""],
];
for (const [line, tone] of toned) {
  test(`a line that starts "${line.trim().slice(0, 28)}" reads as ${tone || "plain text"}`, () => {
    // Act and Assert
    assert.deepEqual(tones(line), line ? [tone] : []);
  });
}

test("a line its program coloured keeps those colours and takes no tone of the board's", () => {
  // Act and Assert
  assert.deepEqual(tones(`${ESC}[33merror${ESC}[0m: only a notice`), [""]);
});

test("a question with options reads as the question, the option the terminal points at, and the others", () => {
  // Arrange
  const prompt = "Bash command\n\n  npm test\n\nDo you want to proceed?\n❯ 1. Yes\n  2. Yes, and don't ask again for npm commands\n  3. No, and tell it what to do differently (esc)\n";

  // Act
  const lines = terminalLines(prompt);

  // Assert
  assert.deepEqual(lines.map((line) => line.tone), ["", "", "", "", "question", "chosen", "option", "option"]);
  assert.equal(lines[5].spans.map((span) => span.text).join(""), "❯ 1. Yes");
});

const asked: [string, string, string[]][] = [
  ["a yes or no question", "Overwrite notes.md? [y/N] ", ["question"]],
  ["options under a question that a blank line follows", "Which account?\n\n> 1) Work\n  2) Personal\n", ["question", "", "chosen", "option"]],
  ["a numbered list that no question leads", "Steps:\n1. build\n2. test\n", ["", "option", "option"]],
  ["one numbered line alone", "1. only this\n", [""]],
  ["numbers that do not count from one", "3. three\n4. four\n", ["", ""]],
  ["a question no options follow", "Is it done?\nyes\n", ["", ""]],
];
for (const [name, text, expected] of asked) {
  test("a prompt reads by its shape: " + name, () => {
    // Act and Assert
    assert.deepEqual(tones(text), expected);
  });
}

test("a path and a web address in plain text are told apart from the words around them", () => {
  // Arrange
  const line = "see internal/spawn/native_control.go:42:7 and C:\\work\\notes.md, then https://example.com/a/b.";

  // Act
  const spans = terminalLines(line)[0].spans;

  // Assert
  assert.deepEqual(spans, [
    { text: "see " }, { text: "internal/spawn/native_control.go:42:7", kind: "path" }, { text: " and " }, { text: "C:\\work\\notes.md", kind: "path" },
    { text: ", then " }, { text: "https://example.com/a/b", kind: "link" }, { text: "." },
  ]);
});

for (const text of ["and/or", "12/24 done", "use /compact now", "version 1.2.3", "TCP/IP"]) {
  test(`"${text}" names no path`, () => {
    // Act and Assert
    assert.deepEqual(terminalLines(text)[0].spans, [{ text }]);
  });
}
