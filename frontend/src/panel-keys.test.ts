import { test } from "node:test";
import assert from "node:assert/strict";
import { chooseKeys, PANEL_KEYS, panelKey } from "./panel-keys.ts";
import type { HarnessPanel } from "./terminalStream.ts";

const numbered: HarnessPanel = { rows: ["Do you want to create nine.txt?"], hints: [], choices: [
  { text: "Yes", number: 1, isFocused: false }, { text: "Yes, and switch to accept edits", number: 2, isFocused: true }, { text: "No", number: 3, isFocused: false },
] };
const unfocused: HarnessPanel = { ...numbered, choices: numbered.choices.map((choice) => ({ ...choice, isFocused: false })) };
const marked: HarnessPanel = { rows: [], hints: [], choices: [{ text: "Trust", number: 0, isFocused: true }, { text: "Do not trust", number: 0, isFocused: false }] };
const press = (key: string, modifiers: Partial<{ shiftKey: boolean; ctrlKey: boolean; altKey: boolean; metaKey: boolean }> = {}) => ({ key, shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, ...modifiers });

test("a choice is picked by moving the harness's focus to it and pressing Enter, or by its number where no focus shows", () => {
  const cases: [HarnessPanel, number, string | null][] = [
    [numbered, 0, PANEL_KEYS.up + PANEL_KEYS.enter],
    [numbered, 1, PANEL_KEYS.enter],
    [numbered, 2, PANEL_KEYS.down + PANEL_KEYS.enter],
    [marked, 1, PANEL_KEYS.down + PANEL_KEYS.enter],
    [unfocused, 2, "3"],
    [{ ...marked, choices: marked.choices.map((choice) => ({ ...choice, isFocused: false })) }, 1, null],
    [numbered, 5, null],
  ];
  for (const [panel, at, keys] of cases) assert.equal(chooseKeys(panel, at), keys, panel.choices.map((choice) => choice.text).join("|") + " at " + at);
});

test("an empty box hands the open panel the terminal's own keys and keeps every other key", () => {
  const cases: [ReturnType<typeof press>, string | null][] = [
    [press("ArrowUp"), PANEL_KEYS.up], [press("ArrowDown"), PANEL_KEYS.down], [press("ArrowLeft"), PANEL_KEYS.left], [press("ArrowRight"), PANEL_KEYS.right],
    [press("Enter"), PANEL_KEYS.enter], [press("Escape"), PANEL_KEYS.escape], [press("Tab"), PANEL_KEYS.tab], [press("Tab", { shiftKey: true }), PANEL_KEYS.backTab], [press(" "), PANEL_KEYS.space],
    [press("3"), PANEL_KEYS.down + PANEL_KEYS.enter], [press("1"), PANEL_KEYS.up + PANEL_KEYS.enter], [press("4"), null], [press("0"), null],
    [press("a"), null], [press("Enter", { shiftKey: true }), null], [press("ArrowUp", { ctrlKey: true }), null], [press("v", { altKey: true }), null], [press("c", { metaKey: true }), null],
  ];
  for (const [pressed, keys] of cases) assert.equal(panelKey(pressed, numbered), keys, JSON.stringify(pressed));
});
