import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import type * as MinimatchModule from "minimatch";

// The lint run loads minimatch through ESLint, its config array and
// typescript-eslint's parser. Through 11.0.0, at optimization level 2, each
// "**/.." in a pattern doubled the patterns it compiled to, past
// braceExpandMax, and every pattern was then compared to every other one, so a
// short pattern could take minutes to compile (GHSA-g3hv-75wg-m249, #1887).
const requireFromHere = createRequire(import.meta.url);
const requireFromEslint = createRequire(requireFromHere.resolve("eslint"));
const requireFromTypescriptEslint = createRequire(requireFromHere.resolve("typescript-eslint"));
const LINT_CONSUMERS = [
  { name: "eslint", resolveFrom: requireFromEslint.resolve("eslint") },
  { name: "@eslint/config-array", resolveFrom: requireFromEslint.resolve("@eslint/config-array") },
  { name: "@typescript-eslint/typescript-estree", resolveFrom: requireFromTypescriptEslint.resolve("@typescript-eslint/typescript-estree") },
];
const BRACE_EXPAND_MAX = 64;

for (const consumer of LINT_CONSUMERS) {
  test(`the minimatch ${consumer.name} loads stops splitting **/.. at braceExpandMax`, () => {
    // Arrange
    const { Minimatch }: typeof MinimatchModule = createRequire(consumer.resolveFrom)("minimatch");
    const amplifying = Array.from({ length: 8 }, () => "**/../a/b").join("/");

    // Act
    const compiled = new Minimatch(amplifying, { optimizationLevel: 2, braceExpandMax: BRACE_EXPAND_MAX });

    // Assert
    assert.ok(compiled.set.length <= BRACE_EXPAND_MAX, `8 "**/.." portions compiled to ${compiled.set.length} patterns, past braceExpandMax ${BRACE_EXPAND_MAX}`);
  });
}
