import type { Session, Snapshot, Task } from "./types.ts";
import { sessionEnd } from "./session-end.ts";

// The terminal deck: every goblin terminal the Overlord opened stays live
// while the board is open, so switching only shows another terminal. The
// switch keys go through the CFO first, then each goblin that has a terminal.

export const CFO_KEY = "cfo";
// Herdr's supervisor serves eight screen streams at once; the deck keeps three
// Herdr views live, and each may briefly hold a second while it switches to or
// from sizing its pane, so another window with its own views still gets some.
const HERDR_LIVE = 3;
// The panel and the board each keep a usable width beside the divider.
const MIN_PANE = 360;
const MIN_BOARD = 280;
const DIVIDER = 10;

export interface DeckEntry { key: string; task?: Task }

export function switchOrder(tasks: Task[]): DeckEntry[] {
  return [{ key: CFO_KEY }, ...tasks.filter((task) => !!task.generation && !task.archived && !sessionEnd(task)).map((task) => ({ key: task.id, task }))];
}

export type SwitchKey = { step: 1 | -1 } | { index: number };

// Ctrl+Alt with Up or Down cycles through the terminals and with 1 to 9 jumps
// to that entry; the keys are matched by position, so a layout's characters
// never change them. Windows reports AltGr as Ctrl+Alt, so a key typed with
// AltGr, such as a brace on a German layout, stays the terminal's.
export function switchKey(event: { code: string; ctrlKey: boolean; altKey: boolean; shiftKey: boolean; metaKey: boolean; getModifierState: (key: string) => boolean }): SwitchKey | null {
  if (!event.ctrlKey || !event.altKey || event.shiftKey || event.metaKey || event.getModifierState("AltGraph")) return null;
  if (event.code === "ArrowDown") return { step: 1 };
  if (event.code === "ArrowUp") return { step: -1 };
  const digit = /^(?:Digit|Numpad)([1-9])$/.exec(event.code);
  return digit ? { index: Number(digit[1]) - 1 } : null;
}

export function switchTarget(order: DeckEntry[], current: string, key: SwitchKey): DeckEntry | undefined {
  if ("index" in key) return order[key.index];
  const at = order.findIndex((entry) => entry.key === current);
  if (at < 0) return order[0];
  return order[(at + key.step + order.length) % order.length];
}

// A deck slot shows a native terminal from its host, Herdr's view of a
// terminal still in Herdr, or an empty state that belongs to no backend.
export type DeckView = { kind: "host"; query: string } | { kind: "herdr" } | { kind: "empty"; text: string };

// A registered CFO with no native terminal is the live CFO still in Herdr.
export function cfoView(snapshot: Pick<Snapshot, "cfo_terminal" | "cfo_runs">): DeckView {
  if (snapshot.cfo_terminal) return { kind: "host", query: "cfo=" + encodeURIComponent(snapshot.cfo_terminal) };
  return snapshot.cfo_runs ? { kind: "herdr" } : { kind: "empty", text: "No CFO is running." };
}

export function goblinView(task: Task): DeckView {
  if (task.phase === "resuming" || task.phase === "stopping") return { kind: "empty", text: task.phase === "resuming" ? "Resuming session..." : "Stopping session..." };
  if (task.lifecycle?.action === "resume" && task.lifecycle.phase === "failed") return { kind: "empty", text: "Resume failed. See Task for details." };
  return task.backend === "native" ? { kind: "host", query: new URLSearchParams({ task: task.id, generation: task.generation }).toString() } : { kind: "herdr" };
}

// A queued task or a child session has no terminal of its own.
export function idleView(task?: Task, node?: Session): Extract<DeckView, { kind: "empty" }> {
  if (task && !task.generation) return { kind: "empty", text: "This task has not started yet." };
  return { kind: "empty", text: node ? "This child has no separate terminal." : "Select a goblin to see its terminal." };
}

// keepLive puts key first among the live terminals, most recent first, and
// lets the oldest Herdr views go past Herdr's limit.
export function keepLive(open: string[], key: string, herdr: (key: string) => boolean): string[] {
  let kept = 0;
  return [key, ...open.filter((other) => other !== key)].filter((entry) => !herdr(entry) || ++kept <= HERDR_LIVE);
}

// The panel opens beside the board and is maximized per view: the Board's
// terminal view and its task view each keep the Overlord's last choice.
// Orchestration follows the task view's choice, so its graph, where goblins
// are picked, stays beside the panel.
export const MAXIMIZED_KEYS = { task: "cfo-pane-maximized", terminal: "cfo-terminal-maximized" } as const;

export function maximizedView(workspace: "Board" | "Orchestration", panel: "task" | "terminal"): "task" | "terminal" {
  return workspace === "Board" ? panel : "task";
}

export function maximizedFor(stored: string | null): boolean {
  return stored === "true";
}

// firstOpen says this browser has never shown the board: nothing records a
// first open, and it keeps no panel layout from before that record existed.
export function firstOpen(recorded: string | null, kept: (string | null)[]): boolean {
  return recorded === null && kept.every((value) => value === null);
}

export function paneWidth(requested: number, workspace: number): number {
  const wanted = Number.isFinite(requested) ? requested : workspace / 2;
  return Math.round(Math.min(Math.max(wanted, MIN_PANE), Math.max(MIN_PANE, workspace - MIN_BOARD - DIVIDER)));
}

// paneTrack is the panel's grid column for a saved width, held to the same
// bounds in whatever window the board opens, since the width may have been
// saved on a wider one.
export function paneTrack(width: number): string {
  return `clamp(${MIN_PANE}px, ${width}px, calc(100% - ${MIN_BOARD + DIVIDER}px))`;
}
