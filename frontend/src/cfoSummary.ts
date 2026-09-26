import type { Snapshot } from "./types.ts";
import { waitingItems, type Item } from "./commandQueue.ts";
import { messageBlocks } from "./messageText.ts";

// The pinned CFO bar says what the CFO needs from the Overlord, since every
// question and review reaches him through the CFO, and otherwise how many
// goblins it supervises. It is the only place on the board that says Waiting
// on you.

// A question is named by its lead sentence, its first paragraph or bullet,
// without the details that follow.
function title(item: Item): string {
  if (item.kind !== "question") return item.kind === "review" ? item.review.title : item.run.title;
  const [lead] = messageBlocks(item.question.text);
  const spans = !lead ? [] : lead.kind === "paragraph" ? lead.spans : lead.items[0];
  return spans.map((span) => span.text).join("").replace(/\s+/g, " ").trim();
}

export function cfoSummary(snapshot: Snapshot): { asking: boolean; line: string } {
  const waiting = waitingItems(snapshot);
  if (waiting.length) return { asking: true, line: "Waiting on you: " + title(waiting[0]) + (waiting.length > 1 ? " and " + (waiting.length - 1) + " more" : "") };
  const goblins = snapshot.tasks.filter((task) => !!task.generation && !task.archived).length;
  return { asking: false, line: goblins ? "Supervising " + goblins + (goblins === 1 ? " goblin" : " goblins") : "No goblins at work" };
}
