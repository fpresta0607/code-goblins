import type { CrossingView } from "./types.ts";

// CrossingLine is the board's one notice about the home's move to goblins'
// names: text says what changed for the person and what restarts, undo the
// command that moves the home back, and isDone that nothing waits to restart,
// so the notice can be dismissed.
export interface CrossingLine { text: string; undo: string; isDone: boolean }

// crossingLine is what the board says once an update or an install moved the
// home to goblins' names, before any terminal restarts and after: what
// changed, what restarts and when, what did not go as it should, and the way
// back. A home that made no move says nothing.
export function crossingLine(crossing: CrossingView | undefined): CrossingLine | null {
  if (!crossing) return null;
  if (!crossing.undo) return crossing.problem ? { text: crossing.problem, undo: "", isDone: false } : null;
  const parts = ["Code Goblins updated, and its command is now goblins and its settings begin GOBLINS_. The command and settings it used before are gone, so a terminal you opened before the update needs opening again."];
  parts.push(crossing.waiting.length > 0
    ? "These restart under the new names, each once it is idle with nothing typed on its prompt line, on its own conversation: " + crossing.waiting.join(", ") + "."
    : "Nothing waits to restart under the new names.");
  if (crossing.problem) parts.push(crossing.problem.endsWith(".") ? crossing.problem : crossing.problem + ".");
  parts.push("The way back, with the fleet paused:");
  return { text: parts.join(" "), undo: crossing.undo, isDone: crossing.waiting.length === 0 };
}
