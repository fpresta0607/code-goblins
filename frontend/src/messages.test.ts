import { test } from "node:test";
import assert from "node:assert/strict";
import { messageState, messagesTo, takesMessages } from "./messages.ts";
import { parseSnapshot, type Snapshot, type Task } from "./types.ts";

const action = (fields: Record<string, unknown>) => ({ id: "m1", kind: "message", task_id: "goblin-one", generation: "", status: "queued", message: "", text: "use staging", updated_at: "2026-10-08T12:00:00Z", ...fields });
const snapshot = (actions: Record<string, unknown>[]): Snapshot => parseSnapshot({ healthy: true, actions });
const task = (fields: Partial<Task>): Task => parseSnapshot({ healthy: true, tasks: [{ id: "goblin-one", phase: "working", generation: "s1", verified: false, ...fields }] }).tasks[0];

// The Overlord, 2026-10-08: "message queued to chief no matter whats running
// smoothly". What he typed shows with where it is, in a word or two, never
// as an error line.
test("a message says where it is: queued, kept for a resume, sent or with the CFO", () => {
  assert.equal(messageState(snapshot([action({})]).actions[0]), "Queued");
  assert.equal(messageState(snapshot([action({ status: "running" })]).actions[0]), "Queued");
  assert.equal(messageState(snapshot([action({ status: "succeeded", message: "Kept for its resume." })]).actions[0]), "Kept for resume");
  assert.equal(messageState(snapshot([action({ status: "succeeded", message: "Accepted by the goblin in its native terminal." })]).actions[0]), "Sent");
  assert.equal(messageState(snapshot([action({ status: "running", awaiting: { task: "goblin-one" } })]).actions[0]), "Sent");
  assert.equal(messageState(snapshot([action({ status: "failed", message: "goblin-one was stopped before it could take it; the CFO has it" })]).actions[0]), "With the CFO");
});

test("a goblin's messages are its own and the CFO's are those that name no goblin", () => {
  const shown = snapshot([action({ id: "a" }), action({ id: "b", task_id: "" }), action({ id: "c", kind: "goblin_answer" })]);
  assert.deepEqual(messagesTo(shown, "goblin-one").map((sent) => sent.id), ["a"]);
  assert.deepEqual(messagesTo(shown, "").map((sent) => sent.id), ["b"]);
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
