import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { test } from "node:test";
import ts from "typescript";

// The Overlord, 2026-10-07, on a CFO status line: "not easy to read and
// honestly hate how you print semi colons". Every sentence the board shows him
// is short sentences with no semicolon, so this reads every string and every
// piece of JSX text in the board's source and names each one that joins two
// clauses with a semicolon. A semicolon that is code, such as one in a CSS
// declaration or a character escape, is not prose and is left alone.
const SOURCE = path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1"));

// A semicolon between words: prose, not code.
const PROSE_SEMICOLON = /[\p{L}\p{N})"'”’]; +[\p{L}\p{N}"'“‘(]/u;
// A CSS declaration list, such as an inline style a test page adds.
const CSS = /^\s*[-a-z]+\s*:[^;]+;(\s*[-a-z]+\s*:[^;]+;?)*\s*$/;

const isProse = (text: string) => PROSE_SEMICOLON.test("a" + text + "a") && !CSS.test(text);

function copyIn(file: string): { line: number; text: string }[] {
  const source = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
  const found: { line: number; text: string }[] = [];
  const visit = (node: ts.Node) => {
    const text = ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node) ? node.text
      : ts.isJsxText(node) ? node.getText(source) : null;
    // A piece of a template or of JSX text can start or end at a value put
    // into the sentence, so it is read as if a word stood there.
    if (text !== null && isProse(text)) found.push({ line: source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1, text: text.trim() });
    ts.forEachChild(node, visit);
  };
  visit(source);
  return found;
}

test("no sentence the board shows joins two clauses with a semicolon", () => {
  // Arrange
  const files = readdirSync(SOURCE).filter((name) => /\.tsx?$/.test(name) && !name.endsWith(".test.ts")).map((name) => path.join(SOURCE, name));

  // Act
  const found = files.flatMap((file) => copyIn(file).map(({ line, text }) => `${path.basename(file)}:${line}: ${text}`));

  // Assert
  assert.ok(files.length > 100, "the board's source was read");
  assert.deepEqual(found, []);
});

test("the check finds a semicolon in a sentence and leaves code alone", () => {
  for (const [text, expected] of [
    ["The CFO has not answered 1 question; the oldest has waited 17 minutes.", true],
    ["Paged pool 15.6 GB: memory no goblin can use; restarting the PC frees it.", true],
    ["1 question waits for the CFO (17 min).", false],
    [".tip { pointer-events: auto; }", false],
    ["width: 10px; height: 4px;", false],
    ["&amp;", false],
    ["; the oldest has waited ", true],
  ] as const) assert.equal(isProse(text), expected, text);
});
