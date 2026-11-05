import type { AfkToggleState } from "../../src/useAfkToggle";

// The board's AFK toggle at rest, for a page that draws a surface without the
// board that keeps it and never presses it.
export const RESTING_AFK_TOGGLE: AfkToggleState = { pending: false, problem: "", pressedOn: false, isAsking: false, clear: () => {}, press: () => {}, turnOn: () => {}, closeQuestion: () => {} };
