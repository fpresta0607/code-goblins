import type { Action, Snapshot, Task } from "./types.ts";

// The Overlord's messages to a goblin, or to the CFO for an empty task, in
// the order he sent them. One a paused goblin's resume note keeps is not
// among them: the box shows what the note itself will carry, which its
// lifecycle names and which outlasts the message's own action.
export function messagesTo(snapshot: Snapshot, task: string): Action[] {
  return snapshot.actions.filter((action) => action.kind === "message" && action.task_id === task && action.message !== "Kept for its resume.");
}

// Where a message he typed is: Queued while it waits for its goblin or the
// CFO, Sent once it is typed into the terminal, and With the CFO when its
// goblin could not take it and the supervisor gave it to the CFO.
export function messageState(action: Action): string {
  if (action.status === "queued" || action.status === "running" && !action.awaiting) return "Queued";
  if (action.status === "failed") return action.message.includes("the CFO has it") ? "With the CFO" : "Not sent";
  return "Sent";
}

// Whether a goblin's panel offers a message box in place of its terminal:
// one paused or resuming, or whose Start is on its way, has no terminal that
// takes typing now.
export function takesMessages(task: Task): boolean {
  return task.phase === "paused" || task.phase === "resuming" || task.phase === "queued" && task.starting;
}
