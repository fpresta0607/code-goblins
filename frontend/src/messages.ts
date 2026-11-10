import type { SessionMessage } from "./session-messages.ts";
import type { Action, Snapshot, Task } from "./types.ts";

// The Overlord's messages to a goblin, in the order he sent them, never one
// that names another goblin or none. One a paused goblin's resume note keeps
// is not among them: the box shows what the note itself will carry, which its
// lifecycle names and which outlasts the message's own action.
export function messagesTo(snapshot: Snapshot, task: string): Action[] {
  return snapshot.actions.filter((action) => action.kind === "message" && action.task_id === task && action.message !== "Kept for its resume.");
}

// isPendingMessage says a message still waits: in the board's queue, as its
// delivery is under way, or in its harness, which has not taken it into the
// turn yet. A delivery the harness was not seen taking still names what it
// awaited, so a late take can settle it, but it waits nowhere.
export function isPendingMessage(action: Action): boolean {
  return action.kind === "message" && (action.status === "queued" || action.status === "running");
}

// isUndelivered says a message's delivery ended with no take seen: it failed,
// or it was typed and its session was never seen taking it (#2242). One its
// goblin could not take that the CFO has was handed on, not lost.
export function isUndelivered(action: Action): boolean {
  return action.kind === "message" && (action.status === "failed" && !action.message.includes("the CFO has it") || action.status === "uncertain");
}

// STARTING opens the supervisor's reason for a message that waits on a start
// of its session (startingFor in internal/supervisor).
const STARTING = /^Starting /;

// Where a message he typed is: Queued while it waits for its goblin, Starting
// while its session starts for it, Sent once it is typed into the terminal,
// and With the CFO when its goblin could not take it and the supervisor gave
// it to the CFO.
export function messageState(action: Action): string {
  if (action.status === "queued" || action.status === "running" && !action.awaiting) return STARTING.test(action.message) ? "Starting" : "Queued";
  if (action.status === "failed") return action.message.includes("the CFO has it") ? "With the CFO" : "Not sent";
  if (action.status === "uncertain") return "Not confirmed";
  return "Sent";
}

// Whether a goblin's panel offers a message box in place of its terminal:
// one paused or resuming, or whose Start is on its way, has no terminal that
// takes typing now.
export function takesMessages(task: Task): boolean {
  return task.phase === "paused" || task.phase === "resuming" || task.phase === "queued" && task.starting;
}

// UNRECORDED_COMMANDS are the harnesses that keep no record of a slash
// command they run themselves (#2193).
const UNRECORDED_COMMANDS = new Set(["codex", "pi"]);

// withTakenCommands is messages with a row for each slash command the board
// saw harness take where harness records nothing of it: Codex and pi run one
// with no turn and write no row, so the board's own message is its place in
// the conversation, where it was taken, rather than a bubble waiting under it
// (#2193). One the conversation holds a row of its own words for, written
// since it was sent, already has its row, and one taken before the first
// loaded row is never drawn, so an empty conversation draws none: it may
// belong to an older page or to an earlier session of the same harness.
export function withTakenCommands(messages: SessionMessage[], sent: Action[], harness: string): SessionMessage[] {
  if (!UNRECORDED_COMMANDS.has(harness) || !messages.length) return messages;
  const first = Date.parse(messages[0].at);
  const taken = sent.filter((action) => action.status === "succeeded" && action.text.trim().startsWith("/") && Date.parse(action.updated_at) >= first
    && !messages.some((message) => message.kind === "prompt" && Date.parse(message.at) >= Date.parse(action.created_at) && sameWords(message.text, action.text)));
  if (!taken.length) return messages;
  const rows = [...messages];
  for (const action of taken.sort((one, other) => Date.parse(one.updated_at) - Date.parse(other.updated_at))) {
    const at = Date.parse(action.updated_at);
    const later = rows.findIndex((message) => Date.parse(message.at) > at);
    rows.splice(later < 0 ? rows.length : later, 0, { id: "board-" + action.id, seq: -1, kind: "prompt", at: action.updated_at, source: "slash", queued: false, text: action.text.trim(), output: "", errors: "", trigger: "", blocks: [], pictures: [] });
  }
  return rows;
}

// sameWords says two texts are the same words, however each is spaced.
export function sameWords(one: string, other: string): boolean {
  return one.trim().split(/\s+/).join(" ") === other.trim().split(/\s+/).join(" ");
}
