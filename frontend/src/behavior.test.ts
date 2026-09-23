import test from "node:test";
import assert from "node:assert/strict";
import { parsePatchToRows, splitRows, reviewRange } from "./diff.ts";
import { lineageRoots, ownsTaskSession, sessionModel, projectSessions, tasksWithoutSession, sessionTitle } from "./lineageTree.ts";
import { alreadyKnown, submissionFor } from "./feedback.ts";
import { parseAction, parseSnapshot, decisionText } from "./types.ts";
import { arrange, workflowNodes, taskColumn, personaFor, nodeStatus, nativeStatus, statusText, pullRequestLabel, safePullRequest } from "./workflow.ts";

test("board completion and semantic personas require the corresponding evidence", () => {
  const task = parseSnapshot({healthy:true, tasks:[{id:"work",title:"Test keyboard access",phase:"done",generation:"new",verified:false}]}).tasks[0];
  assert.equal(taskColumn(task), "In progress");
  assert.equal(taskColumn({...task,verified:true}), "Completed");
  assert.equal(personaFor(task), "tester");
  // Work no keyword classifies still gets its own stable goblin, never the
  // shared app icon, which stays for nodes with no task at all.
  const unclassified = personaFor({...task,title:"Unclassified work"});
  assert.notEqual(unclassified, "general");
  assert.equal(personaFor({...task,title:"Unclassified work"}), unclassified);
  assert.equal(personaFor(), "general");
  assert.equal(personaFor({...task,archived:true}), "finisher");
  assert.equal(personaFor({...task,title:"Build review panel"}), "builder");
  assert.equal(personaFor({...task,title:"Review the authentication changes"}), "reviewer");
  assert.equal(nativeStatus("busy"), "Working");
  assert.equal(nativeStatus("done"), "Native turn finished");
  assert.equal(nodeStatus({id:"t",title:"",task,relation:""}), "Delivery unverified");
  assert.equal(statusText("blocked"), "Blocked");
  assert.equal(statusText("failed"), "Failed");
});

test("arrangement preserves five-level lineage and never invents an orphan parent", () => {
  const snapshot=parseSnapshot({healthy:true,sessions:[
    {id:"cfo",role:"cfo"},{id:"g",parent:"cfo"},{id:"child",parent:"g"},
    {id:"nested",parent:"child"},{id:"deep",parent:"nested"},{id:"orphan",parent:"missing"},
    {id:"cycle1",parent:"cycle2"},{id:"cycle2",parent:"cycle1"},
  ]});
  const nodes=workflowNodes(snapshot), positions=arrange(nodes);
  assert.equal(nodes.find(node=>node.id==="session:orphan")?.parent,undefined);
  assert.equal(Object.keys(positions).length,8);
  assert.ok(positions["session:deep"].y>positions["session:nested"].y);
  assert.equal(nodes.find(node=>node.id==="session:deep")?.parent,"session:nested");
  assert.equal(nodes.find(node=>node.id==="session:cycle1")?.relation,"Cyclic parent link");
});

test("queued review notices show the comment and coordinates without a metadata wall", () => {
  assert.equal(decisionText('review: {"file":"main.go","line":2,"end_line":4,"side":"new","text":"Simplify","generation":"g1","head":"abc"}'), "main.go · new lines 2–4\nSimplify");
  assert.equal(decisionText("Choose a gate action"), "Choose a gate action");
  assert.match(decisionText("review: invalid"), /metadata is invalid/);
});

test("review ranges preserve each side, visible context and hunk gaps", () => {
  const rows = parsePatchToRows("@@ -2,3 +2,4 @@\n before\n-old\n+new\n+extra\n after\n@@ -20 +21 @@\n-far\n+away\n");
  assert.equal(reviewRange(rows, 3, 5, "new"), true);
  assert.equal(reviewRange(rows, 2, 2, "old"), true);
  assert.equal(reviewRange(rows, 3, 4, "old"), true);
  assert.equal(reviewRange(rows, 2, 21, "new"), false);
  assert.equal(reviewRange(rows, 2, 202, "new"), false);
  assert.equal(reviewRange(rows, 5, 5, "old"), false);
});

test("project filtering keeps native relatives without inventing missing owners", () => {
  const snapshot = parseSnapshot({ healthy: true, tasks: [
    { id: "one", project: "one", session: "goblin", generation: "g2", verified: false },
    { id: "two", project: "two", session: "other", generation: "g1", verified: false },
  ], sessions: [
    { id: "cfo", role: "cfo" },
    { id: "goblin", role: "goblin", generation: "g1", task_id: "one", parent: "cfo" },
    { id: "child", role: "worker", native_id: "actual-child", parent: "goblin" },
    { id: "other", role: "goblin", generation: "g1", task_id: "two", parent: "cfo" },
  ] });
  assert.deepEqual(projectSessions(snapshot.sessions, snapshot.tasks, "one").map((node) => node.id), ["cfo", "goblin", "child"]);
  assert.deepEqual(tasksWithoutSession(snapshot.tasks, snapshot.sessions).map((task) => task.id), ["one"]);
  assert.equal(sessionTitle(snapshot.sessions[2], { ...snapshot.tasks[0], title: "Owning task" }), "actual-child");
});

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

test("the board keeps the CFO registration state the supervisor reports", () => {
  const stale = "The CFO is not registered; run cfo register in the CFO session";
  assert.equal(parseSnapshot({healthy:true, registration:stale}).registration, stale);
  assert.equal(parseSnapshot({healthy:true}).registration, "");
});

test("completed history and fleet statuses read the way the fleet reports them", () => {
  const [live, finished, merged, queued] = parseSnapshot({healthy:true, tasks:[
    {id:"work", phase:"idle", verified:false, activity:"working: gate test step", pr:"https://github.com/o/code-goblins/pull/29"},
    {id:"finished:old", phase:"done", verified:false, archived:true},
    {id:"merged:x", phase:"done", verified:false, archived:true, merged:true, pr:"https://github.com/o/code-goblins/pull/30"},
    {id:"brief", phase:"queued", verified:false},
  ]}).tasks;
  assert.equal(live.activity, "working: gate test step");
  assert.equal(taskColumn(live), "In progress");
  assert.equal(statusText(live.phase), "Awaiting input");
  assert.equal(taskColumn(finished), "Completed");
  assert.equal(nodeStatus({id:"f", title:"old", task:finished, relation:""}), "Finished");
  assert.equal(taskColumn(merged), "Completed");
  assert.equal(nodeStatus({id:"m", title:"x", task:merged, relation:""}), "PR merged");
  assert.equal(taskColumn(queued), "Tasks");
  assert.equal(parseSnapshot({healthy:true, tasks:[{id:"older-server", verified:false}]}).tasks[0].archived, false);
});

test("every live goblin gets its own stable artwork instead of the generic app icon", () => {
  const ids = ["cfo-native-board", "cms-editor-fast-load", "pd-land-1251-1252"];
  const persona = (id: string) => personaFor(parseSnapshot({healthy:true, tasks:[{id, title:id, verified:false}]}).tasks[0]);
  const personas = ids.map(persona);
  assert.ok(!personas.includes("general"), personas.join());
  assert.equal(new Set(personas).size, ids.length, personas.join());
  assert.deepEqual(ids.map(persona), personas);
});

test("only an https pull request becomes a link, labelled by repository and number", () => {
  assert.equal(safePullRequest("https://github.com/o/code-goblins/pull/29"), "https://github.com/o/code-goblins/pull/29");
  for (const unsafe of ["javascript:alert(1)", "http://github.com/o/r/pull/1", "https://x y", ""]) assert.equal(safePullRequest(unsafe), "", unsafe);
  assert.equal(pullRequestLabel("https://github.com/o/code-goblins/pull/29"), "code-goblins #29");
  assert.equal(pullRequestLabel("https://example.invalid/review/3"), "Pull request");
});
