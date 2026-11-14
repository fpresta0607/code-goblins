import { test } from "node:test";
import assert from "node:assert/strict";
import { isHarnessHeld, isNotSent, isPendingMessage, isUndelivered, messageState, messagesTo, sameWords, takesMessages, withTakenCommands, withUndelivered } from "./messages.ts";
import { turnEnds, type SessionMessage } from "./session-messages.ts";
import { parseSnapshot, type Snapshot, type Task } from "./types.ts";

const action = (fields: Record<string, unknown>) => ({ id: "m1", kind: "message", task_id: "goblin-one", generation: "", status: "queued", message: "", text: "use staging", updated_at: "2026-10-08T12:00:00Z", ...fields });
const snapshot = (actions: Record<string, unknown>[]): Snapshot => parseSnapshot({ healthy: true, actions });
const task = (fields: Partial<Task>): Task => parseSnapshot({ healthy: true, tasks: [{ id: "goblin-one", phase: "working", generation: "s1", verified: false, ...fields }] }).tasks[0];

// The Overlord, 2026-10-08: "message queued to chief no matter whats running
// smoothly". What he typed shows with where it is, in a word or two, never
// as an error line.
test("a message says where it is: queued, sent or with the CEO", () => {
  assert.equal(messageState(snapshot([action({})]).actions[0]), "Queued");
  assert.equal(messageState(snapshot([action({ status: "running" })]).actions[0]), "Queued");
  assert.equal(messageState(snapshot([action({ message: "Starting goblin-one. The message goes in once it runs." })]).actions[0]), "Starting");
  assert.equal(messageState(snapshot([action({ status: "running", message: "Starting the CEO. The message goes in once it runs." })]).actions[0]), "Starting");
  assert.equal(messageState(snapshot([action({ status: "succeeded", message: "Accepted by the goblin in its native terminal." })]).actions[0]), "Sent");
  assert.equal(messageState(snapshot([action({ status: "running", awaiting: { task: "goblin-one" } })]).actions[0]), "Sent");
  assert.equal(messageState(snapshot([action({ status: "failed", message: "goblin-one was stopped before it could take it; the CEO has it" })]).actions[0]), "With the CEO");
  assert.equal(messageState(snapshot([action({ status: "failed", message: "The terminal is closed." })]).actions[0]), "Not sent");
  assert.equal(messageState(snapshot([action({ status: "uncertain", message: "Typed, and not picked up." })]).actions[0]), "Not confirmed");
});

// The Overlord, 2026-11-10 (#2332): a message the harness still holds in its
// own queue read Sent. Sent means the harness has taken it into a turn, so
// one typed into a turn under way reads Queued until its harness takes it.
test("a message typed into a turn under way reads Queued while its harness holds it", () => {
  const typed = (fields: Record<string, unknown>) => snapshot([action({ status: "running", ...fields })]).actions[0];
  const held = typed({ awaiting: { task: "goblin-one", is_held: true } });
  assert.equal(isHarnessHeld(held), true);
  assert.equal(messageState(held), "Queued");
  assert.equal(messageState(typed({ awaiting: { host: "ceo", is_held: true } })), "Queued");
  for (const other of [typed({ awaiting: { task: "goblin-one" } }), typed({}), typed({ status: "uncertain", awaiting: { task: "goblin-one", is_held: true } }), typed({ status: "succeeded" })]) {
    assert.equal(isHarnessHeld(other), false);
  }
  assert.equal(messageState(typed({ awaiting: { task: "goblin-one" } })), "Sent");
});

// The Overlord, 2026-10-29: a message he typed for the CEO the day before
// still showed as queued, and its Cancel said "that queued message is no
// longer waiting, so nothing was deleted". A delivery the board typed and
// still watches for a late take, whatever its outcome, waits nowhere: only
// one the board holds, or is typing, or that the harness holds untaken does.
test("a message waits only while the board or its harness holds it", () => {
  const waits = (fields: Record<string, unknown>) => isPendingMessage(snapshot([action(fields)]).actions[0]);
  const awaiting = { host: "ceo", since: "2026-10-28T03:04:16Z" };
  assert.equal(waits({ status: "queued" }), true);
  assert.equal(waits({ status: "running" }), true);
  assert.equal(waits({ status: "running", awaiting }), true);
  assert.equal(waits({ status: "uncertain", awaiting }), false);
  assert.equal(waits({ status: "succeeded" }), false);
  assert.equal(waits({ status: "failed" }), false);
});

// A message that failed with its words given to nobody is still the board's,
// so Send now and Cancel act on it (#2306); one handed to the CEO was
// delivered there, and one typed and never seen taken may have arrived.
for (const [what, fields, isUnsent, isEnded] of [
  ["a message that failed with nobody given its words", { status: "failed", message: "Not sent. It was taken back off its prompt line." }, true, true],
  ["a message handed to the CEO", { status: "failed", message: "goblin-one was stopped before it could take it; the CEO has it" }, false, false],
  ["a message typed and never seen taken", { status: "uncertain", message: "Typed, and not picked up." }, false, true],
  ["a message still queued", { status: "queued" }, false, false],
  ["an answer that failed", { kind: "goblin_answer", status: "failed", message: "The terminal is closed." }, false, false],
] as const) {
  test(`${what} is ${isUnsent ? "" : "not "}one that was not sent`, () => {
    const sent = snapshot([action(fields)]).actions[0];
    assert.equal(isNotSent(sent), isUnsent);
    assert.equal(isUndelivered(sent), isEnded);
  });
}

test("a goblin's messages are its own, never one that names no goblin", () => {
  const shown = snapshot([action({ id: "a" }), action({ id: "b", task_id: "" }), action({ id: "c", kind: "goblin_answer" })]);
  assert.deepEqual(messagesTo(shown, "goblin-one").map((sent) => sent.id), ["a"]);
});

// The Overlord, 2026-10-09, of Bernie's paused panel: "why is there no delete
// button for paused queued messages?". What a paused goblin's resume will
// carry is read from its resume note, which outlasts each message's action
// in the board's history, so a kept message's action is not listed too.
test("a message kept for a resume shows from the resume note, never from its action", () => {
  const shown = snapshot([action({ id: "a", status: "succeeded", message: "Kept for its resume." }), action({ id: "b", status: "succeeded", message: "Accepted by the goblin in its native terminal." })]);
  assert.deepEqual(messagesTo(shown, "goblin-one").map((sent) => sent.id), ["b"]);
  const lifecycle = { phase: "paused", action: "pause", at: "", kept: [], stopped: [], problems: [], handoff_saved: false, validation_kept: false };
  assert.deepEqual(task({ phase: "paused", lifecycle: { ...lifecycle, kept_messages: ["i can message?", "go on\nand rebase"] } }).lifecycle?.kept_messages, ["i can message?", "go on\nand rebase"]);
  assert.equal(task({ phase: "paused", lifecycle }).lifecycle?.kept_messages, undefined, "a lifecycle with none names none");
});

// A goblin whose terminal cannot take typing now takes a message: paused,
// resuming, starting, or a Resume or Start that waits its turn.
test("a goblin paused, resuming or starting takes a message, and one at work or stopped does not", () => {
  assert.equal(takesMessages(task({ phase: "paused" })), true);
  assert.equal(takesMessages(task({ phase: "resuming" })), true);
  assert.equal(takesMessages(task({ phase: "queued", generation: "", starting: true })), true);
  assert.equal(takesMessages(task({ phase: "queued", generation: "" })), false, "a queued task not started has nobody to read it");
  assert.equal(takesMessages(task({ phase: "working" })), false, "its live terminal takes typing");
  assert.equal(takesMessages(task({ phase: "stopping" })), false);
});

const row = (id: string, at: string, text: string, kind: SessionMessage["kind"] = "prompt"): SessionMessage => ({ id, seq: 0, kind, at, source: "overlord", queued: false, text, output: "", errors: "", trigger: "", blocks: [], pictures: [] });

// Codex and pi run a slash command themselves and record nothing of it, so
// a command the board saw taken is drawn where it was taken. Claude Code
// records its own row, one the conversation already holds keeps that row,
// and one taken before the first loaded row, as from an older page or an
// earlier session, is not drawn, nor any in an empty conversation (#2193).
test("a slash command a harness records nothing of is drawn where it was taken", () => {
  const conversation = [row("m1", "2026-11-09T14:00:00Z", "check the build"), row("m2", "2026-11-09T14:00:05Z", "It builds.", "reply"), row("m3", "2026-11-09T14:02:00Z", "now the tests")];
  const taken = action({ id: "a1", status: "succeeded", text: "/status", created_at: "2026-11-09T14:01:00Z", updated_at: "2026-11-09T14:01:02Z" });
  const drawn = (actions: Record<string, unknown>[], harness: string, messages = conversation) => withTakenCommands(messages, snapshot(actions).actions, harness).map((message) => message.id + " " + message.text);

  for (const harness of ["codex", "pi"]) {
    assert.deepEqual(drawn([taken], harness), ["m1 check the build", "m2 It builds.", "board-a1 /status", "m3 now the tests"], harness);
  }
  assert.deepEqual(drawn([taken], "claude"), ["m1 check the build", "m2 It builds.", "m3 now the tests"]);
  for (const status of ["queued", "running", "failed", "uncertain"]) {
    assert.deepEqual(drawn([{ ...taken, status }], "codex"), ["m1 check the build", "m2 It builds.", "m3 now the tests"], status);
  }
  assert.deepEqual(drawn([{ ...taken, text: "check the logs" }], "codex"), ["m1 check the build", "m2 It builds.", "m3 now the tests"]);
  assert.deepEqual(drawn([taken], "pi", [...conversation, row("m4", "2026-11-09T14:01:01Z", "/status")]).filter((each) => each.endsWith("/status")), ["m4 /status"]);
  assert.deepEqual(drawn([{ ...taken, created_at: "2026-11-09T13:00:00Z", updated_at: "2026-11-09T13:00:02Z" }], "codex"), ["m1 check the build", "m2 It builds.", "m3 now the tests"]);
  assert.deepEqual(drawn([taken], "pi", []), []);
});

// The Overlord, 2026-11-13, of two messages of his drawn Not confirmed under
// the newest turn hours after he sent them: "Stale old messages that appear
// randomly when memory gets low" (#2541). A message whose delivery ended with
// no take seen is drawn where he sent it, before the first row written after
// it, or after every row when none was, and one sent before the first loaded
// row only once no older page is left, since it belongs to that page. Drawn
// among the rows, it ends no turn.
test("a message that ended with no take seen is drawn where he sent it", () => {
  // Arrange
  const conversation = [row("r0", "2026-11-13T18:00:00Z", "Merged it.", "reply"), row("r1", "2026-11-13T19:30:00Z", "Rebased.", "reply")];
  const ended = (id: string, at: string) => action({ id, status: "uncertain", text: "words of " + id, created_at: at, updated_at: at });
  const old = ended("a0", "2026-11-13T17:00:00Z"), middle = ended("a1", "2026-11-13T19:00:00Z"), newest = ended("a2", "2026-11-13T20:00:00Z");
  const placed = (actions: Record<string, unknown>[], hasOlder: boolean, messages = conversation) => withUndelivered(messages, snapshot(actions).actions, hasOlder);
  const drawn = (actions: Record<string, unknown>[], hasOlder: boolean, messages = conversation) => placed(actions, hasOlder, messages).map((message) => message.id + " " + message.text);

  // Act and Assert
  assert.deepEqual(drawn([newest, middle], true), ["r0 Merged it.", "undelivered-a1 words of a1", "r1 Rebased.", "undelivered-a2 words of a2"]);
  assert.deepEqual(drawn([old], true), ["r0 Merged it.", "r1 Rebased."], "one sent before the rows loaded belongs to an older page");
  assert.deepEqual(drawn([old, middle], false), ["undelivered-a0 words of a0", "r0 Merged it.", "undelivered-a1 words of a1", "r1 Rebased."]);
  assert.deepEqual(drawn([middle], true, []), ["undelivered-a1 words of a1"]);
  assert.deepEqual(turnEnds(placed([middle], true), false), turnEnds(conversation, false), "drawn among the rows, it ends no turn");
  assert.equal(withUndelivered(conversation, [], true), conversation, "with none, the rows are the same rows");
});

test("a ! command is the same words with or without a space after its !, and never its words alone", () => {
  // Arrange
  const cases = [
    { one: "! claude doctor", other: "!claude doctor", isSame: true },
    { one: "!  claude   doctor ", other: "!claude doctor", isSame: true },
    { one: "! claude doctor", other: "claude doctor", isSame: false },
    { one: "say ! loudly", other: "say !loudly", isSame: false },
  ];

  // Act and Assert
  for (const { one, other, isSame } of cases) assert.equal(sameWords(one, other), isSame, `${one} | ${other}`);
});
