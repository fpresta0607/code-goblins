import type { Snapshot } from "./types.ts";
import { waitingItems } from "./commandQueue.ts";
import { afkLine } from "./afk.ts";

// The pinned CFO bar says how many goblins the CFO supervises, and how many
// items wait on the Overlord, since every question and review reaches him
// through the CFO. It names nothing of what waits: its Open Command Center
// glows instead, and the Command Center says what each item is.
//
// While AFK mode is on the bar says so instead, and nothing waits by its
// count: nothing on the board prompts the Overlord while he is away, and what
// waits on him is held under the bar.
export function cfoSummary(snapshot: Snapshot, now = Date.now()): { waiting: number; line: string } {
  const away = afkLine(snapshot.afk, now);
  if (away) return { waiting: 0, line: away };
  const waiting = waitingItems(snapshot).length;
  const goblins = snapshot.tasks.filter((task) => !!task.generation && !task.archived).length;
  const fleet = goblins ? "The CFO supervises " + goblins + (goblins === 1 ? " goblin." : " goblins.") : "No goblins are at work.";
  return { waiting, line: waiting ? fleet : "All quiet. " + fleet };
}
