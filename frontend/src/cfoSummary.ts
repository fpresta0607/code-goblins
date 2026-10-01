import type { Snapshot } from "./types.ts";
import { waitingItems, type Item } from "./commandQueue.ts";
import { messageBlocks } from "./messageText.ts";

// The pinned CFO bar says what the CFO needs from the Overlord, since every
// question and review reaches him through the CFO, and otherwise how many
// goblins it supervises. It is the only place on the board that says Waiting
// on you.

// A question or a review is named by its lead sentence, its first paragraph
// or bullet, without the details or table that follow. A goblin's wait on the
// Overlord titles its item Waiting on you, which the bar already says.
function lead(text: string): string {
  const [first] = messageBlocks(text);
  const spans = !first ? [] : first.kind === "paragraph" ? first.spans : first.kind === "list" ? first.items[0] : [];
  return spans.map((span) => span.text).join("").replace(/\s+/g, " ").trim();
}

function title(item: Item): string {
  if (item.kind === "review") return lead(item.review.title.replace(/^Waiting on you: /, ""));
  if (item.kind === "run") return item.run.title;
  return lead(item.question.text);
}

export function cfoSummary(snapshot: Snapshot): { asking: boolean; line: string } {
  const waiting = waitingItems(snapshot);
  if (waiting.length) return { asking: true, line: "Waiting on you: " + title(waiting[0]) + (waiting.length > 1 ? " and " + (waiting.length - 1) + " more" : "") };
  const goblins = snapshot.tasks.filter((task) => !!task.generation && !task.archived).length;
  return { asking: false, line: goblins ? "Supervising " + goblins + (goblins === 1 ? " goblin" : " goblins") : "No goblins at work" };
}
