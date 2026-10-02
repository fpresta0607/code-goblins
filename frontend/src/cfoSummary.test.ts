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

test("with nothing waiting on the Overlord the CFO says how many goblins it supervises", () => {
  assert.deepEqual(cfoSummary(snapshot({ tasks: [task("a"), task("b"), task("queued", { generation: "" }), task("history", { archived: true })] })), { asking: false, line: "All quiet. The CFO supervises 2 goblins." });
  assert.deepEqual(cfoSummary(snapshot({ tasks: [task("a")] })), { asking: false, line: "All quiet. The CFO supervises 1 goblin." });
  assert.deepEqual(cfoSummary(snapshot()), { asking: false, line: "All quiet. No goblins are at work." });
});

test("what the CFO needs from the Overlord leads, the CFO's own first, in plain words", () => {
  const needs = cfoSummary(snapshot({
    tasks: [task("goblin-a")],
    questions: [question("goblin", "Which **layout** should I use?\n\n- A: stacked", { task: "goblin-a", created_at: "2026-09-25T09:00:00Z" }), question("own", "Merge **PR 91** now?")],
    reviews: [review("look", "Check the onboarding mockup")],
  }));
  assert.deepEqual(needs, { asking: true, line: "Waiting on you: Merge PR 91 now? and 2 more" });
});

test("a review or a command to run is named by its title, and answered items no longer count", () => {
  assert.deepEqual(cfoSummary(snapshot({ reviews: [review("look", "Check the onboarding mockup")] })), { asking: true, line: "Waiting on you: Check the onboarding mockup" });
  assert.deepEqual(cfoSummary(snapshot({ runs: [run("fix", "Restart the dev database")] })), { asking: true, line: "Waiting on you: Restart the dev database" });
  assert.deepEqual(cfoSummary(snapshot({ tasks: [task("a")], questions: [question("done", "Ship it?", { status: "answered" })], reviews: [review("closed", "Old", { state: "closed" })] })), { asking: false, line: "All quiet. The CFO supervises 1 goblin." });
});

test("a goblin's wait on the Overlord, titled Waiting on you by its item, says it once", () => {
  assert.equal(cfoSummary(snapshot({ reviews: [review("waiting-goblin-a-7", "Waiting on you: pick the settings layout")] })).line, "Waiting on you: pick the settings layout");
});

test("a question is named by its lead sentence alone, without its details", () => {
  assert.equal(cfoSummary(snapshot({ questions: [question("q", "Which **layout** should I use?\n\n- A: stacked\n- B: tabs")] })).line, "Waiting on you: Which layout should I use?");
  assert.equal(cfoSummary(snapshot({ questions: [question("q", "Pick one?\n- A\n- B")] })).line, "Waiting on you: Pick one?");
});

test("while AFK mode is on the bar says so in its plain form, whatever waits on the Overlord, and a switch that cannot be read is not taken for on", () => {
  const now = Date.parse("2026-10-02T12:31:00Z");
  const held = { item: "question:own", task: "", what: "Merge PR 91 now?", at: "2026-10-02T03:05:00Z", waiting: true, now: "still waiting on you", meanwhile: "" };
  const on = afk({ state: "on", since: "2026-10-02T02:10:00Z", from: "his own board (goblins-window.exe pid 4242)", decided: 3, held: [held] });
  const waits = { tasks: [task("goblin-a")], questions: [question("own", "Merge PR 91 now?")] };
  const away = cfoSummary(snapshot({ ...waits, afk: on }), now);
  assert.deepEqual(away, { asking: false, line: afkLine(on, now) });
  assert.match(away.line, /^AFK since .+, from your board\. 3 decided, 1 held for you\.$/);
  assert.deepEqual(cfoSummary(snapshot({ ...waits, afk: afk({ state: "unreadable", problem: "unexpected EOF" }) }), now), { asking: true, line: "Waiting on you: Merge PR 91 now?" });
});
