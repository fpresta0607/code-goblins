import type { HarnessPanel } from "./terminalStream.ts";

// The keys a harness's own screen takes, as its terminal sends them.
export const PANEL_KEYS = {
  up: "\x1b[A", down: "\x1b[B", left: "\x1b[D", right: "\x1b[C",
  enter: "\r", escape: "\x1b", tab: "\t", backTab: "\x1b[Z", space: " ",
} as const;

// chooseKeys is what picks a panel's choice at index at, as the terminal
// would: the arrows that move the harness's focus there from the choice it is
// on, then Enter, or the choice's number where no focus can be read. Null
// when neither can reach it.
export function chooseKeys(panel: HarnessPanel, at: number): string | null {
  const choice = panel.choices[at];
  if (!choice) return null;
  const focused = panel.choices.findIndex((each) => each.isFocused);
  if (focused >= 0) return (at > focused ? PANEL_KEYS.down : PANEL_KEYS.up).repeat(Math.abs(at - focused)) + PANEL_KEYS.enter;
  return choice.number ? String(choice.number) : null;
}

interface PressedKey { key: string; shiftKey: boolean; ctrlKey: boolean; altKey: boolean; metaKey: boolean }

// panelKey is what a key he presses in an empty message box sends to the
// panel open in the terminal, so the box answers it with the terminal's own
// keys: the arrows, Enter, Escape, Tab and Space as they are, and a choice's
// number as picking that choice. Null for every other key, which the box
// keeps.
export function panelKey(pressed: PressedKey, panel: HarnessPanel): string | null {
  if (pressed.ctrlKey || pressed.altKey || pressed.metaKey) return null;
  if (pressed.shiftKey) return pressed.key === "Tab" ? PANEL_KEYS.backTab : null;
  switch (pressed.key) {
    case "ArrowUp": return PANEL_KEYS.up;
    case "ArrowDown": return PANEL_KEYS.down;
    case "ArrowLeft": return PANEL_KEYS.left;
    case "ArrowRight": return PANEL_KEYS.right;
    case "Enter": return PANEL_KEYS.enter;
    case "Escape": return PANEL_KEYS.escape;
    case "Tab": return PANEL_KEYS.tab;
    case " ": return PANEL_KEYS.space;
  }
  const at = /^[1-9]$/.test(pressed.key) ? panel.choices.findIndex((choice) => choice.number === Number(pressed.key)) : -1;
  return at >= 0 ? chooseKeys(panel, at) : null;
}
