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

// Every place a file draws a caret: the caret icon, as the board's Icon draws
// it, and the tree's Chevron, with whether the button around the Chevron says
// it is expanded.
function caretsIn(name: string): string[] {
  const file = path.join(SOURCE, name);
  const source = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const found: string[] = [];
  const says = (element: ts.JsxOpeningLikeElement, attribute: string) => element.attributes.properties.some((property) => ts.isJsxAttribute(property) && property.name.getText(source) === attribute);
  const visit = (node: ts.Node) => {
    if (ts.isStringLiteral(node) && node.text === "chevron") found.push("icon");
    if (ts.isJsxSelfClosingElement(node) && node.tagName.getText(source) === "Chevron") {
      let around: ts.Node = node.parent;
      while (around && !(ts.isJsxElement(around) && around.openingElement.tagName.getText(source) === "button")) around = around.parent;
      found.push(around && ts.isJsxElement(around) && says(around.openingElement, "aria-expanded") ? "fold" : "Chevron outside a button that says aria-expanded");
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return found;
}

// The Overlord, 2026-10-10, of the caret on a queued task's line that opened
// its edit box: "I get editing pencil a little icon". A caret only ever opens
// or closes what it stands before, so this names every caret the source
// draws: the Disclosure's, a tree's fold on a button that says whether it is
// open, and the pager's two arrows, which turn its pages.
test("every caret opens or closes, but for the pager's arrows", () => {
  // Arrange
  const files = readdirSync(SOURCE).filter((name) => name.endsWith(".tsx")).sort();

  // Act
  const drawn = files.flatMap((name) => caretsIn(name).map((kind) => name + ": " + kind));

  // Assert
  assert.deepEqual(drawn, ["Disclosure.tsx: icon", "Lineage.tsx: fold", "Orchestration.tsx: fold", "Pager.tsx: icon", "Pager.tsx: icon", "TreeCount.tsx: fold"]);
});

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
