import test from "node:test";
import assert from "node:assert/strict";
import { parsePatchToRows, splitRows } from "./diff.ts";
import { lineageRoots, ownsTaskSession, sessionModel } from "./lineageTree.ts";
import { alreadyKnown, submissionFor } from "./feedback.ts";
import { parseAction, parseSnapshot } from "./types.ts";

test("diff coordinates survive additions, deletions, separate hunks and split alignment", () => {
  const rows = parsePatchToRows(
    "--- a/example.ts\n+++ b/example.ts\n@@ -3,2 +3,3 @@\n-old\n+new\n+extra\n context\n@@ -20 +21 @@\n-last\n+next\n",
  );
  assert.deepEqual(
    rows.filter((row) => row.variant === "added").map((row) => row.next),
    [3, 4, 21],
  );
  assert.deepEqual(
    rows.filter((row) => row.variant === "removed").map((row) => row.old),
    [3, 20],
  );
  const split = splitRows(rows);
  assert.equal(split[1].left?.text, "old");
  assert.equal(split[1].right?.text, "new");
  assert.equal(split[2].left, undefined);
});

test("lost HTTP response plus SSE success keeps exactly one request identity", () => {
  const payload = JSON.stringify({
    task_id: "task",
    text: "please fix",
    file: "main.go",
    line: 3,
  });
  let generated = 0;
  const first = submissionFor(payload, null, () => `id-${++generated}`);
  // HTTP was delivered but its response was lost. SSE supplies the result.
  const actions = [
    parseAction({ id: first.id, kind: "feedback", status: "succeeded" }),
  ];
  const retry = submissionFor(payload, first, () => `id-${++generated}`);
  assert.equal(retry.id, first.id);
  assert.equal(generated, 1);
  assert.equal(alreadyKnown(retry, payload, actions)?.status, "succeeded");
  assert.equal(
    submissionFor(payload + "changed", first, () => `id-${++generated}`).id,
    "id-2",
  );
});

test("lineage retains unknown parents and safely exposes disconnected cycles", () => {
  const snapshot = parseSnapshot({
    healthy: true,
    sessions: [
      { id: "a", parent: "b" },
      { id: "b", parent: "a" },
      { id: "c", parent: "unknown" },
    ],
  });
  assert.deepEqual(
    lineageRoots(snapshot.sessions).map((node) => node.id),
    ["c", "a"],
  );
  assert.equal(snapshot.sessions[2].parent, "unknown");
});

test("invalid streamed data is a visible protocol error", () => {
  assert.throws(
    () => parseSnapshot({ healthy: true, tasks: "bad" }),
    /Invalid response list/,
  );
});

test("child sessions never borrow the owning goblin model or effort", () => {
  const snapshot = parseSnapshot({
    healthy: true,
    tasks: [
      {
        id: "task",
        session: "goblin",
        generation: "g1",
        model: "explicit-parent-model",
        effort: "max",
        verified: false,
      },
    ],
    sessions: [
      { id: "goblin", role: "goblin", generation: "g1", task_id: "task" },
      {
        id: "child",
        role: "subagent",
        generation: "g1",
        task_id: "task",
        parent: "goblin",
      },
    ],
  });
  const [parent, child] = snapshot.sessions;
  const task = snapshot.tasks[0];
  assert.equal(sessionModel(child, task), "Model unreported");
  assert.equal(ownsTaskSession(child, task), false);
  assert.equal(sessionModel(parent, task), "explicit-parent-model");
  assert.equal(sessionModel(undefined, task), "explicit-parent-model");
  assert.equal(
    sessionModel({ ...child, model: "child-native-model" }, task),
    "child-native-model",
  );
});
