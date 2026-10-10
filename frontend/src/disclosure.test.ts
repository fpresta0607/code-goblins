import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { test } from "node:test";
import ts from "typescript";

// The Overlord, 2026-10-09, on the Update card's Output: "when output >
// should match task panel arrows in ui meaning the caret". Whatever opens and
// closes on the board is the Disclosure, which draws the one caret, so this
// reads the board's source and names every details element drawn anywhere
// else. The Command Center's button is a menu behind an icon of its own, not
// a line that unfolds.
const SOURCE = path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1"));
const OWN_ICON = "command-center-menu";

// The class of every details element a file draws, empty for one with none.
function detailsIn(name: string): string[] {
  const file = path.join(SOURCE, name);
  const source = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const found: string[] = [];
  const visit = (node: ts.Node) => {
    if ((ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) && node.tagName.getText(source) === "details") {
      const named = node.attributes.properties.find((property) => ts.isJsxAttribute(property) && property.name.getText(source) === "className");
      found.push(named && ts.isJsxAttribute(named) && named.initializer && ts.isStringLiteral(named.initializer) ? named.initializer.text : "");
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return found;
}

test("whatever opens and closes on the board is the Disclosure, which draws the one caret", () => {
  // Arrange
  const files = readdirSync(SOURCE).filter((name) => name.endsWith(".tsx")).sort();

  // Act
  const drawn = files.flatMap((name) => detailsIn(name).map((kind) => ({ name, kind })));

  // Assert: the reading finds the two that are meant to be there, so it
  // would find another.
  assert.deepEqual(drawn, [{ name: "CommandCenter.tsx", kind: OWN_ICON }, { name: "Disclosure.tsx", kind: "" }]);
  assert.match(readFileSync(path.join(SOURCE, "Disclosure.tsx"), "utf8"), /<summary><Icon name="chevron" \/>/);
});
