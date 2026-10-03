import test from "node:test";
import assert from "node:assert/strict";
import { holdClosed, remembered, withItems, type Sent } from "./item-state.ts";
import { waitingItems, type Item } from "./commandQueue.ts";
import { parseItems, parseSnapshot } from "./types.ts";

const question = (id: string, status = "pending", extra: Record<string, unknown> = {}) => ({ id, identity: "i-" + id, task: "", created_at: "2026-10-02T11:00:00Z", status, options: ["Merge it", "Hold it"], ...extra });
const review = (id: string, state = "open", extra: Record<string, unknown> = {}) => ({ id, identity: "r-" + id, task: "", title: "Look at " + id, created_at: "2026-10-02T11:00:00Z", updated_at: "2026-10-02T11:00:00Z", state, ...extra });
const board = (fields: Record<string, unknown>) => parseSnapshot({ healthy: true, instance: "board", revision: 1, ...fields });
const waiting = (snapshot: ReturnType<typeof board>) => waitingItems(snapshot).map((item) => item.key);
const nothing: ReadonlyMap<string, Item> = new Map();
const unsent: ReadonlyMap<string, Sent> = new Map();
const AT = "2026-10-02T11:00:00Z";

test("the Command Center's items alone replace the last snapshot's and nothing else", () => {
  const current = board({ revision: 4, tasks: [{ id: "billing", title: "Billing", phase: "working", generation: "g1", verified: false }], questions: [question("train")] });
  // The event as the supervisor sends it: the items, its instance and its revision, and nothing else.
  const items = parseItems({ instance: "board", revision: 5, questions: [question("train", "succeeded", { answered_by: "cfo" })], reviews: [review("shots")], runs: [], credentials: [], actions: [{ id: "a1", kind: "cfo_answer", status: "queued" }] });

  const merged = withItems(current, items)!;

  assert.equal(merged.revision, 5);
  assert.deepEqual(merged.tasks, current.tasks, "the goblins stay as the last snapshot had them");
  assert.deepEqual(merged.questions, items.questions);
  assert.deepEqual(merged.reviews, items.reviews);
  assert.deepEqual(merged.actions, items.actions);
});

test("items from another supervisor, older items, and items before any snapshot change nothing", () => {
  const current = board({ revision: 4, questions: [question("train")] });
  assert.equal(withItems(current, parseItems({ instance: "another", revision: 9 })), current, "another supervisor's items");
  assert.equal(withItems(current, parseItems({ instance: "board", revision: 3 })), current, "items older than the snapshot");
  assert.equal(withItems(null, parseItems({ instance: "board", revision: 5 })), null, "no snapshot yet");
});

test("an item a snapshot showed closed stays closed when a later snapshot shows it open again", () => {
  const closed = board({ questions: [question("train", "succeeded", { answer: "Merge it", answered_by: "cfo" })], reviews: [review("shots", "cleared", { reason: "Opened" })] });
  const stale = board({ revision: 2, questions: [question("train")], reviews: [review("shots")] });
  const memory = remembered(nothing, closed);

  const held = holdClosed(stale, memory, unsent);

  assert.deepEqual(waiting(stale), ["question:train", "review:shots"], "the stale snapshot alone would bring both back");
  assert.deepEqual(waiting(held), []);
  assert.deepEqual(held.questions, closed.questions, "each shows as the board last saw it closed");
  assert.deepEqual(held.reviews, closed.reviews);
});

test("a snapshot with nothing to hold comes back as it was", () => {
  const snapshot = board({ questions: [question("train")] });
  assert.equal(holdClosed(snapshot, nothing, unsent), snapshot);
  assert.equal(remembered(nothing, snapshot), nothing, "nothing closed, nothing to remember");
});

test("what he sent closes its item before the board hears back, as the supervisor will record it", () => {
  const snapshot = board({ questions: [question("train"), question("strays")], reviews: [review("shots"), review("plan")] });
  const cases: [string, string, Sent, (held: ReturnType<typeof board>) => unknown, unknown][] = [
    ["an answer", "question:train", { kind: "cfo_answer", id: "a1", text: "Merge it", answer_kind: "option", created_at: AT }, (held) => { const q = held.questions!.find((q) => q.id === "train")!; return [q.status, q.answer_id, q.answer, q.answer_kind]; }, ["queued", "a1", "Merge it", "option"]],
    ["a goblin's answer", "question:train", { kind: "goblin_answer", id: "a2", text: "Hold it", answer_kind: "option", created_at: AT }, (held) => held.questions!.find((q) => q.id === "train")!.status, "queued"],
    ["a dismissed question", "question:strays", { kind: "question_clear", id: "a3", text: "", answer_kind: "", created_at: AT }, (held) => { const q = held.questions!.find((q) => q.id === "strays")!; return [q.status, q.message]; }, ["cleared", "You dismissed it: answered elsewhere or no longer needed."]],
    ["a review answer", "review:shots", { kind: "review_answer", id: "a4", text: "They read well", answer_kind: "", created_at: AT }, (held) => { const r = held.reviews!.find((r) => r.id === "shots")!; return [r.state, r.answer, r.answer_id]; }, ["answered", "They read well", "a4"]],
    ["a cleared review", "review:plan", { kind: "review_clear", id: "a5", text: "Opened", answer_kind: "", created_at: AT }, (held) => { const r = held.reviews!.find((r) => r.id === "plan")!; return [r.state, r.reason]; }, ["cleared", "Opened"]],
  ];
  for (const [name, key, sent, read, want] of cases) {
    const held = holdClosed(snapshot, nothing, new Map([[key, sent]]));
    assert.deepEqual(read(held), want, name);
    assert.ok(!waiting(held).includes(key), name + " no longer waits on him");
    assert.equal(waiting(held).length, 3, name + " closes nothing else");
  }
});

test("a command he runs stays on its card while it runs", () => {
  const snapshot = board({ runs: [{ id: "install", identity: "c", title: "Install the build", state: "ready", created_at: "2026-10-02T11:00:00Z" }] });
  const held = holdClosed(snapshot, nothing, new Map([["run:install", { kind: "run", id: "a1", text: "", answer_kind: "", created_at: AT }]]));
  assert.equal(held, snapshot);
});

test("the board remembers the newest closed items and forgets the oldest past its limit", () => {
  let memory: ReadonlyMap<string, Item> = nothing;
  for (let i = 0; i < 520; i++) memory = remembered(memory, board({ questions: [question("q" + i, "cleared")] }));
  assert.equal(memory.size, 500);
  assert.ok(!memory.has("question:q0") && memory.has("question:q519"));
});

test("an ID published again shows open, and an item whose identity moved stays held", () => {
  const run = (state: string, extra: Record<string, unknown> = {}) => ({ id: "install", identity: "u-install", title: "Install the build", state, created_at: AT, ...extra });
  const request = (state: string, extra: Record<string, unknown> = {}) => ({ id: "keys", identity: "k-keys", task: "billing", state, created_at: AT, ...extra });
  const memory = remembered(nothing, board({ questions: [question("train", "succeeded", { answer: "Merge it", answered_by: "cfo" })], reviews: [review("shots", "cleared", { reason: "Opened" })], runs: [run("succeeded")], credentials: [request("saved")] }));
  const sends: ReadonlyMap<string, Sent> = new Map([
    ["question:train", { kind: "cfo_answer", id: "a1", text: "Merge it", answer_kind: "option", created_at: AT }],
    ["review:shots", { kind: "review_clear", id: "a2", text: "Opened", answer_kind: "", created_at: AT }],
  ]);
  const all = ["question:train", "review:shots", "run:install", "credential:keys"];
  const publications: [string, Record<string, unknown>, boolean][] = [
    ["the same publication", {}, true],
    ["a later created_at", { created_at: "2026-10-12T09:00:00Z" }, false],
    ["another identity and the same created_at, as when the CFO registers again", { identity: "another" }, true],
  ];
  const memories: [string, ReadonlyMap<string, Item>, ReadonlyMap<string, Sent>, string[]][] = [
    ["seen closed", memory, unsent, all],
    ["sent for", nothing, sends, [...sends.keys()]],
  ];
  for (const [publication, change, isHeld] of publications) {
    const snapshot = board({ revision: 2, questions: [question("train", "pending", change)], reviews: [review("shots", "open", change)], runs: [run("ready", change)], credentials: [request("open", change)] });
    for (const [how, closed, sent, known] of memories) {
      const held = holdClosed(snapshot, closed, sent);
      assert.deepEqual(waiting(held).sort(), all.filter((key) => !isHeld || !known.includes(key)).sort(), publication + ", " + how);
      if (!isHeld) assert.equal(held, snapshot, publication + ", " + how + ": the snapshot comes back as it was");
    }
  }
});
