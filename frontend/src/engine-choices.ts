import type { HarnessPanel } from "./terminalStream.ts";

export type EngineSetting = "model" | "effort";

// The effort levels the harnesses name, from the least to the most: pi's off
// and minimal, Codex's minimal, and Claude Code's max above every other.
const EFFORT_RANKS = ["off", "minimal", "low", "medium", "high", "xhigh", "max", "auto"];

// effortTone is how a level is coloured in Code Goblins' palette, as Claude
// Code colours its own (the Overlord, 2026-10-27: "max being rainbow, and as
// it goes lower it just becomes a more dull color"): the lowest dull, then
// light, rising through green to purple, and max a rainbow. A level
// no harness named here, and auto, which picks for itself, stay plain.
export function effortTone(level: string): string {
  return { off: "dull", minimal: "dull", low: "light", medium: "green", high: "teal", xhigh: "purple", max: "max" }[level] ?? "plain";
}

// effortsInOrder sorts a harness's levels from the least to the most.
export function effortsInOrder(levels: string[]): string[] {
  return [...levels].sort((first, second) => EFFORT_RANKS.indexOf(first) - EFFORT_RANKS.indexOf(second));
}

// engineCommand is the line that sets a session's model or effort in its
// harness at once, with the value after the command: Claude Code's /model and
// /effort, and pi's /model and /thinking (pi 1.4.0's interactive mode takes
// the value after either). Codex's /model takes no value and only opens its
// picker, so it has none: the box answers that picker instead (codexChoice).
export function engineCommand(harness: string, setting: EngineSetting, value: string): string | null {
  if (harness === "claude") return (setting === "model" ? "/model " : "/effort ") + value;
  if (harness === "pi") return (setting === "model" ? "/model " : "/thinking ") + value;
  return null;
}

// settingCommand is the setting a line he typed asks to change, when its
// command is one the box has a control for: /model, Claude Code's /effort
// and pi's /thinking, with the value typed after it, if any. Modern Chat
// opens its own pane for these instead of the harness's terminal menu.
export function settingCommand(harness: string, text: string): { setting: EngineSetting; value: string } | null {
  const match = /^\/(\S+)(?:\s+(.*))?$/.exec(text.trim());
  if (!match) return null;
  const value = (match[2] ?? "").trim();
  if (match[1] === "model" && ["claude", "codex", "pi"].includes(harness)) return { setting: "model", value };
  if (match[1] === "effort" && harness === "claude" || match[1] === "thinking" && harness === "pi") return { setting: "effort", value };
  return null;
}

// heads says whether a choice's text begins with name as a whole: followed
// by nothing, a space or what Codex adds in brackets, such as "(current)", so
// gpt-6-sol never picks gpt-6-sol-mini.
const heads = (text: string, name: string) => {
  const said = text.trim().toLowerCase(), head = name.trim().toLowerCase();
  return !!head && said.startsWith(head) && (said.length === head.length || /^[\s(]/.test(said.slice(head.length)));
};

// Codex names xhigh "Extra high" in its picker.
const CODEX_EFFORT_WORDS: Record<string, string> = { xhigh: "extra high" };

// codexChoice is the index of the choice in Codex's open /model picker that
// is the model or the effort the box set out to pick, or -1 while the picker
// shows neither: its model list names each model by its slug or its display
// name, and its reasoning list each level by its word.
export function codexChoice(panel: HarnessPanel, setting: EngineSetting, value: string, name = ""): number {
  return panel.choices.findIndex((choice) => setting === "model"
    ? heads(choice.text, value) || heads(choice.text, name)
    : heads(choice.text, CODEX_EFFORT_WORDS[value] ?? value) || heads(choice.text, value));
}
