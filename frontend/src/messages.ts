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

// isHarnessHeld says a message was typed into a turn under way that has not
// taken it: it waits in its harness's own queue, where Send now, Edit and
// Cancel act on it (#2332).
export function isHarnessHeld(action: Action): boolean {
  return action.kind === "message" && action.status === "running" && !!action.awaiting && action.is_held;
}

// isNotSent says a message's delivery failed with its words given to nobody:
// one its goblin could not take that the CEO has was handed on, not lost. The
// board still holds it, so Send now sends that same message and Cancel takes
// it off the board (#2306).
export function isNotSent(action: Action): boolean {
  return action.kind === "message" && action.status === "failed" && !action.message.includes("the CEO has it");
}

// isUndelivered says a message's delivery ended with no take seen: it was not
// sent, or it was typed and its session was never seen taking it (#2242).
export function isUndelivered(action: Action): boolean {
  return isNotSent(action) || action.kind === "message" && action.status === "uncertain";
}

// STARTING opens the supervisor's reason for a message that waits on a start
// of its session (startingFor in internal/supervisor).
const STARTING = /^Starting /;

// Where a message he typed is: Queued while it waits for its goblin, on the
// board or in its harness's own queue, Starting while its session starts for
// it, Sent once its harness has it for a turn, and With the CEO when its
// goblin could not take it and the supervisor gave it to the CEO.
export function messageState(action: Action): string {
  if (action.status === "queued" || action.status === "running" && !action.awaiting || isHarnessHeld(action)) return STARTING.test(action.message) ? "Starting" : "Queued";
  if (action.status === "failed") return action.message.includes("the CEO has it") ? "With the CEO" : "Not sent";
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

// UNDELIVERED_ROW opens the ID of the row withUndelivered gives a message of
// his whose delivery ended with no take seen.
export const UNDELIVERED_ROW = "undelivered-";

// withUndelivered is messages with a row for each of undelivered, his messages
// whose delivery ended with no take seen, where he sent it: before the first
// row written after it, or after every row when none was, so it is never drawn
// under turns that came after it, nor among what still waits at the end (the
// Overlord, 2026-11-13: "Stale old messages that appear randomly when memory
// gets low", and 2026-11-14, during a compaction: "it renders not confirmed
// old stale messages as if its queued or being sent", #2541). One sent before
// the first loaded row is drawn only once no older page is left, since it
// belongs to that page. Its row is a prompt still queued, which ends no turn.
export function withUndelivered(messages: SessionMessage[], undelivered: Action[], hasOlder: boolean): SessionMessage[] {
  if (!undelivered.length) return messages;
  const rows = [...messages];
  for (const action of [...undelivered].sort((one, other) => Date.parse(one.created_at) - Date.parse(other.created_at))) {
    const later = rows.findIndex((message) => Date.parse(message.at) > Date.parse(action.created_at));
    if (later === 0 && hasOlder) continue;
    rows.splice(later < 0 ? rows.length : later, 0, { id: UNDELIVERED_ROW + action.id, seq: -1, kind: "prompt", at: action.created_at, source: "", queued: true, text: action.text, output: "", errors: "", trigger: "", blocks: [], pictures: [] });
  }
  return rows;
}

// sameWords says two texts are the same words, however each is spaced, a !
// command's ! included: Claude Code runs "! X" and "!X" alike, and its row
// reads "!X" (#2242).
export function sameWords(one: string, other: string): boolean {
  const words = (text: string) => text.trim().replace(/^!\s+/, "!").split(/\s+/).join(" ");
  return words(one) === words(other);
}
