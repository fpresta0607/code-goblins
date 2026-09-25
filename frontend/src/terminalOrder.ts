import type { Task } from "./types.ts";

// The terminal deck: every goblin terminal the Overlord opened stays live
// while the board is open, so switching only shows another terminal. The
// switcher lists the CFO first, then each goblin that has a terminal.

export const CFO_KEY = "cfo";
// Herdr's supervisor serves four screen streams at once; the deck keeps three
// Herdr views live so a fourth window still gets one.
const HERDR_LIVE = 3;
// The panel and the board each keep a usable width beside the divider.
const MIN_PANE = 360;
const MIN_BOARD = 280;

export interface DeckEntry { key: string; task?: Task }

export function switchOrder(tasks: Task[]): DeckEntry[] {
  return [{ key: CFO_KEY }, ...tasks.filter((task) => !!task.generation && !task.archived).map((task) => ({ key: task.id, task }))];
}

export type SwitchKey = { step: 1 | -1 } | { index: number };

// Ctrl+Alt with Up or Down cycles through the switcher and with 1 to 9 jumps
// to that entry; the keys are matched by position, so a layout's characters
// never change them.
export function switchKey(event: { code: string; ctrlKey: boolean; altKey: boolean; shiftKey: boolean; metaKey: boolean }): SwitchKey | null {
  if (!event.ctrlKey || !event.altKey || event.shiftKey || event.metaKey) return null;
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

// keepLive puts key first among the live terminals, most recent first, and
// lets the oldest Herdr views go past Herdr's limit.
export function keepLive(open: string[], key: string, herdr: (key: string) => boolean): string[] {
  let kept = 0;
  return [key, ...open.filter((other) => other !== key)].filter((entry) => !herdr(entry) || ++kept <= HERDR_LIVE);
}

export function paneWidth(requested: number, workspace: number): number {
  const wanted = Number.isFinite(requested) ? requested : workspace / 2;
  return Math.round(Math.min(Math.max(wanted, MIN_PANE), Math.max(MIN_PANE, workspace - MIN_BOARD)));
}
