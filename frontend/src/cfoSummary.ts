import type { Snapshot } from "./types.ts";
import { waitingItems, waitReason, type Item } from "./commandQueue.ts";
import { messageBlocks } from "./messageText.ts";

// The pinned CFO bar says what the CFO needs from the Overlord, since every
// question and review reaches him through the CFO, and otherwise how many
// goblins it supervises. It is the only place on the board that says Waiting
// on you.

// A question or a review is named by its lead sentence, its first paragraph
// or bullet, without the details or tables of values around it. A goblin's
// wait on the Overlord titles its item Waiting on you, which the bar already
// says, and ends it with its page's link, which its card opens instead.
function lead(text: string): string {
  const first = messageBlocks(text).find((block) => block.kind !== "table");
  const spans = !first ? [] : first.kind === "paragraph" ? first.spans : first.items[0];
  return spans.map((span) => span.text).join("").replace(/\s+/g, " ").trim();
}

function title(item: Item): string {
  if (item.kind === "review") return lead(waitReason(item.review));
  if (item.kind === "run") return item.run.title;
  return lead(item.question.text);
}

export function cfoSummary(snapshot: Snapshot): { asking: boolean; line: string } {
  const waiting = waitingItems(snapshot);
  if (waiting.length) return { asking: true, line: "Waiting on you: " + title(waiting[0]) + (waiting.length > 1 ? " and " + (waiting.length - 1) + " more" : "") };
  const goblins = snapshot.tasks.filter((task) => !!task.generation && !task.archived).length;
  return { asking: false, line: goblins ? "All quiet. The CFO supervises " + goblins + (goblins === 1 ? " goblin." : " goblins.") : "All quiet. No goblins are at work." };
}
