import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { test } from "node:test";
import ts from "typescript";

// The Overlord, 2026-10-08: "again everything error wise goes to cfo and cfo
// decides what to tell me in command center", and of a line saying how and
// when a panel was read: "i dont need descriptive text everywhere like this".
// The board keeps seven kinds of words about trouble: a one or two word status
// on a card, a disabled button's reason in its tip, a few words beside what
// he clicked that go by themselves (ClickFeedback), one short line while the
// supervisor is down, the box that says his AFK switch was refused, in full,
// which he chose on 2026-10-08 over a few words that went by themselves
// ("this yellow text i hate. as an error"), and, under a key card's row, why
// the board refused the value he pasted, so the row stays open, which he
// approved with the key card on 2026-10-12, and a failed Send beside its kept
// draft, which must stay actionable even while input is held (2026-10-27).
// This reads the board's source for
// every other way of showing him an error, and for a line explaining where a
// number came from or how fresh it is.
const SOURCE = path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1"));
const FEEDBACK = "click-feedback.tsx";
const REFUSAL = "afk-refusal.tsx";
const KEY_CARD = "credential-card.tsx";
const KEY_REFUSAL = "credential-refusal";

// The roles and classes the board used for error boxes and warning lines.
const ERROR_CLASSES = /\b(warning-text|task-action-problem|error-box|terminal-error|connections-error|order-error|afk-problem|connection-banner|layout-notice|lineage-warning|card-stalled|windows-teardown|memory-warning|card-silent|window-error)\b/;
// Words that say something went wrong, in more than two words of text.
const TROUBLE = /\b(could not|couldn't|cannot|can't|failed|failure|refused|error|unavailable|did not|didn't|not reach|went wrong|try again later)\b/i;
// A sentence about where a number came from or how fresh it is.
const PROVENANCE = /\b(read|fetched|checked|measured|reported|updated)\b[^.]*\b(ago|just now)\b|\bfrom the [^.]*\b(records|processes|logs?)\b/i;

interface Finding { file: string; line: number; what: string }

// Whether a string sits in what the page shows as text: inside a JSX child
// expression, not an attribute such as a tip or a label.
function isShownText(node: ts.Node): boolean {
  for (let at = node.parent; at; at = at.parent) {
    if (ts.isJsxAttribute(at)) return false;
    if (ts.isJsxExpression(at)) return ts.isJsxElement(at.parent) || ts.isJsxFragment(at.parent);
  }
  return false;
}

function findingsIn(file: string, code: string): Finding[] {
  const source = ts.createSourceFile(file, code, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const found: Finding[] = [];
  const at = (node: ts.Node, what: string) => found.push({ file: path.basename(file), line: source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1, what });
  const visit = (node: ts.Node) => {
    if (path.basename(file) === REFUSAL) return;
    if (ts.isJsxAttribute(node)) {
      const name = node.name.getText(source), value = node.initializer && ts.isStringLiteral(node.initializer) ? node.initializer.text : node.initializer?.getText(source) || "";
      const keyRefusal = path.basename(file) === KEY_CARD && node.parent.properties.some((other) => ts.isJsxAttribute(other) && other.name.getText(source) === "className" && !!other.initializer && ts.isStringLiteral(other.initializer) && other.initializer.text === KEY_REFUSAL);
      const isComposerRefusal = path.basename(file) === "composer.tsx" && node.parent.properties.some((other) => ts.isJsxAttribute(other) && other.name.getText(source) === "className" && !!other.initializer && ts.isStringLiteral(other.initializer) && other.initializer.text === "composer-refusal");
      if (name === "role" && /alert/.test(value) && !keyRefusal && !isComposerRefusal) at(node, 'role="alert"');
      if (name === "className" && ERROR_CLASSES.test(value)) at(node, "className " + value);
    }
    const text = ts.isJsxText(node) ? node.getText(source).trim()
      : (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) && isShownText(node) ? node.text.trim() : "";
    if (text && path.basename(file) !== FEEDBACK) {
      if (TROUBLE.test(text) && text.split(/\s+/).length > 2) at(node, "error text: " + text.slice(0, 80));
      if (PROVENANCE.test(text)) at(node, "explanatory line: " + text.slice(0, 80));
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return found;
}

test("the board shows no error, warning or explanatory line outside its kept action feedback", () => {
  // Arrange
  const files = readdirSync(SOURCE).filter((name) => name.endsWith(".tsx")).map((name) => path.join(SOURCE, name));

  // Act
  const found = files.flatMap((file) => findingsIn(file, readFileSync(file, "utf8"))).map(({ file, line, what }) => `${file}:${line}: ${what}`);

  // Assert
  assert.ok(files.length > 80, "the board's components were read");
  assert.deepEqual(found, []);
});

test("the check finds an error box, an error sentence and an explanatory line, and leaves states and tips alone", () => {
  const cases: [string, number][] = [
    ['export const A = () => <p className="warning-text" role="alert">x</p>;', 2],
    ["export const B = () => <p>The workspace could not be read.</p>;", 1],
    ['export const C = () => <p>{"Your answer did not reach the CFO."}</p>;', 1],
    ["export const D = () => <p>Read 22s ago from the goblin's records and processes.</p>;", 1],
    ['export const E = () => <span className="plain-status">Failed</span>;', 0],
    ['export const F = () => <button data-tip="Resume failed: the goblin could not start">x</button>;', 0],
    ["export const G = () => <span>Checks failed</span>;", 0],
  ];
  for (const [code, want] of cases) assert.equal(findingsIn("case.tsx", code).length, want, code);
  const refusal = 'export const H = () => <p className="credential-refusal" role="alert">x</p>;';
  assert.equal(findingsIn("credential-card.tsx", refusal).length, 0, "a key card row's refusal");
  assert.equal(findingsIn("case.tsx", refusal).length, 1, "the same line anywhere else");
});
