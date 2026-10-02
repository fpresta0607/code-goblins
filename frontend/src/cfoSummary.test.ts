import { test } from "node:test";
import assert from "node:assert/strict";
import { afkLine } from "./afk.ts";
import { cfoSummary } from "./cfoSummary.ts";
import type { Afk, Question, Review, Run, Snapshot, Task } from "./types.ts";

const task = (id: string, changes: Partial<Task> = {}) => ({ id, generation: "g1", archived: false, ...changes }) as Task;
const question = (id: string, text: string, changes: Partial<Question> = {}) => ({ id, text, status: "pending", task: "", created_at: "2026-09-25T10:00:00Z", ...changes }) as Question;
const review = (id: string, title: string, changes: Partial<Review> = {}) => ({ id, title, state: "open", task: "goblin-a", created_at: "2026-09-25T11:00:00Z", ...changes }) as Review;
const run = (id: string, title: string, changes: Partial<Run> = {}) => ({ id, title, state: "ready", created_at: "2026-09-25T12:00:00Z", ...changes }) as Run;
const afk = (changes: Partial<Afk> = {}): Afk => ({ state: "off", since: "", from: "", decided: 0, held: [], report: "", ended: "", problem: "", ...changes });
const snapshot = (changes: Partial<Snapshot> = {}) => ({ tasks: [], attention: [], questions: [], reviews: [], runs: [], afk: afk(), ...changes }) as unknown as Snapshot;

test("with nothing waiting on the Overlord the CFO says all is quiet and how many goblins it supervises", () => {
  assert.deepEqual(cfoSummary(snapshot({ tasks: [task("a"), task("b"), task("queued", { generation: "" }), task("history", { archived: true })] })), { waiting: 0, line: "All quiet. The CFO supervises 2 goblins." });
  assert.deepEqual(cfoSummary(snapshot({ tasks: [task("a")] })), { waiting: 0, line: "All quiet. The CFO supervises 1 goblin." });
  assert.deepEqual(cfoSummary(snapshot()), { waiting: 0, line: "All quiet. No goblins are at work." });
});

test("while something waits on the Overlord the bar counts it and says nothing of what it is, nor that all is quiet", () => {
  const needs = cfoSummary(snapshot({
    tasks: [task("goblin-a")],
    questions: [question("goblin", "Which **layout** should I use?\n\n- A: stacked", { task: "goblin-a", created_at: "2026-09-25T09:00:00Z" }), question("own", "Merge **PR 91** now?")],
    reviews: [review("look", "Check the onboarding mockup")],
  }));
  assert.deepEqual(needs, { waiting: 3, line: "The CFO supervises 1 goblin." });
  assert.deepEqual(cfoSummary(snapshot({ runs: [run("fix", "Restart the dev database")] })), { waiting: 1, line: "No goblins are at work." });
});

test("what was answered or closed no longer counts", () => {
  assert.deepEqual(cfoSummary(snapshot({ tasks: [task("a")], questions: [question("done", "Ship it?", { status: "answered" })], reviews: [review("closed", "Old", { state: "closed" })] })), { waiting: 0, line: "All quiet. The CFO supervises 1 goblin." });
});

test("while AFK mode is on the bar says so and counts nothing as waiting, whatever waits on the Overlord, and a switch that cannot be read is not taken for on", () => {
  const now = Date.parse("2026-10-02T12:31:00Z");
  const held = { item: "question:own", task: "", what: "Merge PR 91 now?", at: "2026-10-02T03:05:00Z", waiting: true, now: "still waiting on you", meanwhile: "" };
  const on = afk({ state: "on", since: "2026-10-02T02:10:00Z", from: "his own board (goblins-window.exe pid 4242)", decided: 3, held: [held] });
  const waits = { tasks: [task("goblin-a")], questions: [question("own", "Merge PR 91 now?")] };
  const away = cfoSummary(snapshot({ ...waits, afk: on }), now);
  assert.deepEqual(away, { waiting: 0, line: afkLine(on, now) });
  assert.match(away.line, /^AFK since .+, from your board\. 3 decided, 1 held for you\.$/);
  assert.deepEqual(cfoSummary(snapshot({ ...waits, afk: afk({ state: "unreadable", problem: "unexpected EOF" }) }), now), { waiting: 1, line: "The CFO supervises 1 goblin." });
});
