import type { Snapshot } from "./types.ts";
import { waitingItems } from "./commandQueue.ts";
import { afkLine } from "./afk.ts";

// The pinned CFO bar says how many goblins are at work, and how many
// items wait on the Overlord, since every question and review reaches him
// through the CFO. It names nothing of what waits: its Open Command Center
// glows instead, and the Command Center says what each item is. While the
// CFO has left goblins' questions unanswered for ten minutes the bar says so
// first, with how many and how long the oldest has waited, in place of All
// quiet: that is never an alert, which is only for what asks the Overlord
// himself. Each fact is a short sentence of its own, its number first, as the
// Overlord asked on 2026-10-07 ("honestly hate how you print semi colons").
//
// While AFK mode is on the bar says so instead, and still counts what waits:
// nothing is held for the Overlord while he is away, and Open Command Center,
// unlit, is the bar's one way to whatever is there.
export function cfoSummary(snapshot: Snapshot, now = Date.now()): { waiting: number; line: string } {
  const away = afkLine(snapshot.afk, now);
  const waiting = waitingItems(snapshot).length;
  if (away) return { waiting, line: away };
  const goblins = snapshot.tasks.filter((task) => !!task.generation && !task.archived).length;
  const fleet = goblins ? goblins + (goblins === 1 ? " goblin" : " goblins") + " at work." : "No goblins are at work.";
  const quiet = snapshot.cfo_quiet;
  const behind = quiet ? `${quiet.count} question${quiet.count === 1 ? " waits" : "s wait"} for the CFO (${Math.floor(quiet.oldest_age / 60)} min). ` : "";
  return { waiting, line: behind ? behind + fleet : waiting ? fleet : "All quiet. " + fleet };
}
