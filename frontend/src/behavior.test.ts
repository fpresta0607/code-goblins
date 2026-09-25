import test from "node:test";
import assert from "node:assert/strict";
import { dragRange, isDrag, parsePatchToRows, splitRows, reviewRange } from "./diff.ts";
import { lineageRoots, ownsTaskSession, sessionModel, projectSessions, tasksWithoutSession, sessionTitle } from "./lineageTree.ts";
import { alreadyKnown, deliveryMark, runMark, submissionFor } from "./feedback.ts";
import { parseAction, parseSnapshot, decisionText } from "./types.ts";
import { arrange, workflowNodes, taskColumn, personaFor, nodeStatus, nativeStatus, statusText, asksOverlord, waitingTarget, pullRequestBadge, pullRequestLabel, safePullRequest, fleetTraffic, reportTraffic, expireTraffic, fitScale, CFO_ROOT, NODE_WIDTH, NODE_HEIGHT } from "./workflow.ts";

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
  assert.equal(nativeStatus("done"), "Turn finished");
  assert.equal(nodeStatus({id:"t",title:"",task,relation:""}), "Done, verifying");
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
  assert.equal(statusText(live.phase), "Waiting for input");
  assert.equal(taskColumn(finished), "Completed");
  assert.equal(nodeStatus({id:"f", title:"old", task:finished, relation:""}), "Finished");
  assert.equal(taskColumn(merged), "Completed");
  assert.equal(nodeStatus({id:"m", title:"x", task:merged, relation:""}), "Merged");
  assert.equal(taskColumn(queued), "Tasks");
  for (const [phase, label] of [["merged", "Merged, verifying"], ["blocked", "Blocked"], ["ready", "Checks passed"]]) {
    const landed = {...live, phase, merged:true};
    assert.equal(taskColumn(landed), "In progress");
    assert.equal(nodeStatus({id:"l", title:"work", task:landed, relation:""}), label);
  }
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

test("only a GitHub pull request wears the GitHub mark and its bare number", () => {
  const cases: [string, { github: boolean; label: string }][] = [
    ["https://github.com/o/code-goblins/pull/29", { github: true, label: "#29" }],
    ["https://gitlab.com/o/code-goblins/-/merge_requests/7", { github: false, label: "Pull request" }],
    ["https://example.invalid/review/3", { github: false, label: "Pull request" }],
  ];
  for (const [url, badge] of cases) assert.deepEqual(pullRequestBadge(url), badge, url);
});

test("dispatched goblins hang under the CFO, and only live work is in the tree", () => {
  const live = ["a", "b", "c", "d", "e", "f"].map((id) => ({id, phase:"working", verified:false}));
  const history = [{id:"finished:x", phase:"done", verified:false, archived:true}, {id:"brief", phase:"queued", verified:false}];
  const drawn = workflowNodes(parseSnapshot({healthy:true, registration:"The CFO is not registered; run cfo register in the CFO session", tasks:[...live, ...history]}));
  const root = drawn.find((node) => node.id === CFO_ROOT);
  assert.ok(root?.cfo);
  assert.equal(nodeStatus(root!), "Registration stale");
  assert.deepEqual(drawn.filter((node) => node.task).map((node) => [node.task!.id, node.parent, node.relation]), live.map((task) => [task.id, CFO_ROOT, "Dispatched by the CFO"]));

  const reported = workflowNodes(parseSnapshot({healthy:true, sessions:[{id:"cfo", role:"cfo"}, {id:"lost", role:"goblin", parent:"missing"}], tasks:[{id:"a", phase:"working", verified:false}]}));
  assert.equal(reported.find((node) => node.id === CFO_ROOT), undefined);
  assert.equal(reported.find((node) => node.id === "task:a")?.parent, "session:cfo");
  assert.equal(reported.find((node) => node.id === "session:lost")?.parent, undefined);
  assert.equal(workflowNodes(parseSnapshot({healthy:true, tasks:history})).length, 0);
});

test("a wide family of goblins wraps into rows that never overlap", () => {
  const nodes = workflowNodes(parseSnapshot({healthy:true, tasks:["a", "b", "c", "d", "e", "f"].map((id) => ({id, phase:"working", verified:false}))}));
  const positions = arrange(nodes);
  const cards = nodes.filter((node) => node.task).map((node) => positions[node.id]);
  const rows = [...new Set(cards.map((point) => point.y))].sort((a, b) => a - b);
  assert.equal(rows.length, 2);
  for (const [i, one] of cards.entries()) for (const other of cards.slice(i + 1)) {
    assert.ok(Math.abs(one.x - other.x) >= NODE_WIDTH || Math.abs(one.y - other.y) >= NODE_HEIGHT, JSON.stringify([one, other]));
  }
  assert.ok(cards.every((point) => point.y > positions[CFO_ROOT].y));
  // A second-row card's connector drops through a gap in the first row, never
  // behind a first-row card that would then look like its parent.
  const first = cards.filter((point) => point.y === rows[0]);
  for (const card of cards.filter((point) => point.y === rows[1])) {
    const center = card.x + NODE_WIDTH / 2;
    assert.ok(first.every((above) => center < above.x || center > above.x + NODE_WIDTH), JSON.stringify({ card, first }));
  }
});

test("a connector pulses only when a goblin reports something new", () => {
  const snapshot = (activity: string, decisions: {seq:number,key:string}[] = []) => parseSnapshot({healthy:true,
    tasks:[{id:"a", phase:"working", verified:false, activity}, {id:"old", phase:"done", verified:false, archived:true, activity}],
    decisions:decisions.map((d) => ({...d, kind:"notify", detail:"blocked: which?", time:"2026-09-23T00:00:00Z"}))});
  const first = fleetTraffic(null, snapshot("working: tests"));
  assert.deepEqual(first.moved, []);
  assert.deepEqual(fleetTraffic(first.signatures, snapshot("working: tests")).moved, []);
  assert.deepEqual(fleetTraffic(first.signatures, snapshot("working: gate review")).moved, ["a"]);
  assert.deepEqual(fleetTraffic(first.signatures, snapshot("working: tests", [{seq:4, key:"a"}])).moved, ["a"]);
  assert.equal(first.signatures.has("old"), false);
});

test("a pulse ends with its own report however many snapshots follow", () => {
  const snapshot = (activity: string) => parseSnapshot({healthy:true, tasks:[{id:"a", phase:"working", verified:false, activity}]});
  let seen = fleetTraffic(null, snapshot("working: tests")).signatures;
  let traffic: Record<string, number> = {};
  const report = (activity: string, at: number) => {
    const { signatures, moved } = fleetTraffic(seen, snapshot(activity));
    seen = signatures;
    traffic = reportTraffic(traffic, moved, at);
    return moved;
  };
  const first = report("working: gate review", 1000);
  for (let at = 1001; at < 1010; at++) assert.deepEqual(report("working: gate review", at), []);
  assert.deepEqual(traffic, {a:1000});
  traffic = expireTraffic(traffic, first, 1000);
  assert.deepEqual(traffic, {});
  const older = report("working: lint", 2000);
  report("working: push", 3000);
  traffic = expireTraffic(traffic, older, 2000);
  assert.deepEqual(traffic, {a:3000});
});

test("every status reads as plain words, never the old evidence jargon", () => {
  const words: [string, string][] = [["queued","Not started"],["working","Working"],["active","Working"],["started","Starting"],["review","In review gate"],["ready","Checks passed"],
    ["done","Delivered"],["merged","Merged, verifying"],["idle","Waiting for input"],["blocked","Blocked"],["failed","Failed"],["unavailable","No fresh evidence"],
    ["stale","No fresh evidence"],["interrupted","Interrupted"],["settled","Turn finished"],["ended","Session ended"],["unknown","No evidence yet"],["","No evidence yet"]];
  for (const [phase, label] of words) assert.equal(statusText(phase), label, phase);
  for (const [phase, label] of [["busy","Working"],["idle","Waiting for input"],["unknown","No evidence yet"],["stale","No fresh evidence"]]) assert.equal(nativeStatus(phase), label, phase);
  const retired = /Ready to start|Awaiting|Evidence|evidence is stale|Native turn|Verify landed|Delivery unverified|PR merged/;
  for (const phase of [...words.map(([phase]) => phase), "busy", "done", "bogus"]) {
    assert.doesNotMatch(statusText(phase), retired, phase);
    assert.doesNotMatch(nativeStatus(phase), retired, phase);
  }
});

test("a goblin waiting on a question says who it is waiting on", () => {
  const snapshot = parseSnapshot({healthy:true, tasks:[
    {id:"asks", phase:"blocked", verified:false, reason:"Waiting on the CFO: which rule?"},
    {id:"cfo", phase:"blocked", verified:false, reason:"Waiting on the CFO: rebase?"},
    {id:"stuck", phase:"failed", verified:false, reason:"gate died"},
  ], questions:[
    {id:"notify-asks-1", identity:"g", task:"asks", status:"pending", options:["A","B"]},
    {id:"notify-stuck-1", identity:"g", task:"stuck", status:"queued", options:["A"]},
    {id:"cfo-question", identity:"c", task:"", status:"pending", options:["A"]},
  ]});
  const [asks, cfo, stuck] = snapshot.tasks;
  assert.equal(asksOverlord(snapshot, "asks"), true);
  assert.equal(asksOverlord(snapshot, "stuck"), false, "an answered question no longer waits on the Overlord");
  assert.equal(asksOverlord(snapshot, ""), false, "the CFO's own question belongs to no goblin");
  assert.equal(nodeStatus({id:"a", title:"", task:asks, relation:""}, true), "Waiting on you");
  assert.equal(nodeStatus({id:"c", title:"", task:cfo, relation:""}), "Waiting on the CFO");
  assert.equal(nodeStatus({id:"s", title:"", task:stuck, relation:""}), "Failed");
});

test("delivery reads as a mark, and only trouble spells itself out", () => {
  const mark = (status: string, kind: string) => deliveryMark(parseAction({ id: "a", kind, status }));
  assert.deepEqual(mark("succeeded", "review"), { icon: "check-double", label: "Accepted by the CFO", trouble: false });
  assert.deepEqual(mark("succeeded", "cfo_answer"), { icon: "check-double", label: "Accepted by the CFO", trouble: false });
  assert.deepEqual(mark("succeeded", "goblin_answer"), { icon: "check-double", label: "Delivered to the goblin", trouble: false });
  assert.deepEqual(mark("succeeded", "evaluate"), { icon: "check-double", label: "Done", trouble: false });
  // A review answer's action succeeds whether it reached the goblin or was
  // handed to the CFO, so only the review item's delivered flag earns two checks.
  assert.deepEqual(mark("succeeded", "review_answer"), { icon: "check", label: "Sent to the goblin or the CFO", trouble: false });
  assert.deepEqual(mark("running", "review"), { icon: "check", label: "Sending", trouble: false });
  assert.deepEqual(mark("queued", "review"), { icon: "check", label: "Queued", trouble: false });
  assert.deepEqual(mark("failed", "review"), { icon: "close", label: "Could not deliver", trouble: true });
  assert.deepEqual(mark("uncertain", "review"), { icon: "warning", label: "Delivery unconfirmed. Inspect the CFO queue before sending again.", trouble: true });
  assert.deepEqual(mark("uncertain", "feedback"), { icon: "warning", label: "Delivery unconfirmed. Inspect the terminal before sending again.", trouble: true });
  assert.deepEqual(mark("", "review"), { icon: "check", label: "Queued", trouble: false });
  assert.deepEqual(mark("succeeded", "review_clear"), { icon: "check", label: "Cleared", trouble: false });
});

test("the orchestration graph fills the canvas, capped so cards never get huge", () => {
  const cases: [number, number, number, number, number][] = [
    [600, 400, 1448, 948, 1.25],
    [1400, 500, 748, 948, .5],
    [700, 900, 1448, 498, .5],
    [5000, 5000, 548, 548, .35],
    [800, 600, 0, 0, 1],
  ];
  for (const [graphWidth, graphHeight, canvasWidth, canvasHeight, scale] of cases) {
    assert.equal(fitScale({ width: graphWidth, height: graphHeight }, { width: canvasWidth, height: canvasHeight }), scale, graphWidth + "x" + graphHeight + " in " + canvasWidth + "x" + canvasHeight);
  }
});

test("a drag across diff rows comments on the lines it covers", () => {
  const rows = parsePatchToRows("@@ -1,3 +1,4 @@\n context\n-removed\n+added one\n+added two\n tail");
  const lines = rows.filter((row) => row.variant !== "hunk");
  const cases: [string, number, number, "old" | "new" | undefined, ReturnType<typeof dragRange>][] = [
    ["context through the added lines", 0, 3, undefined, { side: "new", line: 1, end: 3 }],
    ["only the removed line", 1, 1, undefined, { side: "old", line: 2, end: 2 }],
    ["removed then added, split view started in the old column", 1, 2, "old", { side: "old", line: 2, end: 2 }],
    ["one added line", 3, 3, undefined, { side: "new", line: 3, end: 3 }],
  ];
  for (const [name, first, last, prefer, expected] of cases) assert.deepEqual(dragRange(lines.slice(first, last + 1), prefer), expected, name);
  assert.equal(dragRange(rows.filter((row) => row.variant === "hunk")), null);
});

test("a split drag outside both columns covers both sides, new lines first and old as the fallback", () => {
  const bothSides = (patch: string) => splitRows(parsePatchToRows(patch)).flatMap((pair) => [pair.left, pair.right])
    .flatMap((row) => row && row.variant !== "hunk" ? [row] : []);
  const cases: [string, string, ReturnType<typeof dragRange>][] = [
    ["a removed-only block", "@@ -4,3 +3,0 @@\n-one\n-two\n-three", { side: "old", line: 4, end: 6 }],
    ["mixed removed and added pairs", "@@ -1,3 +1,3 @@\n context\n-removed\n+added\n tail", { side: "new", line: 1, end: 3 }],
  ];
  for (const [name, patch, expected] of cases) assert.deepEqual(dragRange(bothSides(patch)), expected, name);
});

test("only a single-click press that moves more than 4 px counts as a drag", () => {
  const cases: [string, number, { x: number; y: number }, boolean][] = [
    ["a click without movement", 1, { x: 10, y: 10 }, false],
    ["a click that jitters 4 px", 1, { x: 14, y: 10 }, false],
    ["a drag down three lines", 1, { x: 10, y: 70 }, true],
    ["a drag across one line", 1, { x: 15, y: 10 }, true],
    ["a double-click word selection", 2, { x: 10, y: 70 }, false],
    ["a triple-click line selection", 3, { x: 10, y: 70 }, false],
  ];
  for (const [name, detail, to, expected] of cases) assert.equal(isDrag(detail, { x: 10, y: 10 }, to), expected, name);
});

// Main's frontend before this change read phase waiting as No evidence yet,
// so every waiting goblin on the live board lost its status.
test("a waiting goblin says what it waits on, and only a wait on the Overlord reads as his", () => {
  const snapshot = parseSnapshot({ healthy: true, tasks: [
    { id: "billing", phase: "waiting", waiting_on: "board-ui", reason: "needs the new panel", verified: false },
    { id: "board-ui", phase: "working", verified: false },
    { id: "ship", phase: "waiting", waiting_on: "ci", verified: false },
    { id: "release", phase: "waiting", waiting_on: "deploy", verified: false },
    { id: "deploy", phase: "waiting", waiting_on: "deploy", verified: false },
    { id: "ask", phase: "waiting", waiting_on: "overlord", verified: false },
    { id: "gone", phase: "waiting", waiting_on: "retired-task", verified: false },
    { id: "gate", phase: "review", gate_step: "test", verified: false },
    { id: "gate-ci", phase: "review", gate_step: "ci", verified: false },
    { id: "gate-review", phase: "review", gate_step: "review", verified: false },
    { id: "gate-lint", phase: "review", gate_step: "lint", verified: false },
    { id: "gate-push", phase: "review", gate_step: "push", verified: false },
    { id: "gate-new", phase: "review", gate_step: "canary", verified: false },
    { id: "gate-unknown", phase: "review", verified: false },
    { id: "older", phase: "working", verified: false, waiting_on: undefined },
  ] });
  const status = (id: string) => { const task = snapshot.tasks.find((candidate) => candidate.id === id)!; return nodeStatus({ id, title: "", task, relation: "" }, asksOverlord(snapshot, id)); };
  const cases: [string, string][] = [["billing", "Waiting on board-ui"], ["ship", "Waiting on CI"], ["release", "Waiting on deploy"], ["deploy", "Waiting on deploy"], ["ask", "Waiting on you"],
    ["gone", "Waiting on retired-task"], ["gate", "In review gate: tests"], ["gate-ci", "In review gate: CI"],
    ["gate-review", "In review gate: code review"], ["gate-lint", "In review gate: lint"], ["gate-push", "In review gate: push"], ["gate-new", "In review gate"], ["gate-unknown", "In review gate"], ["older", "Working"]];
  for (const [id, label] of cases) assert.equal(status(id), label, id);
  assert.equal(asksOverlord(snapshot, "ask"), true);
  for (const id of ["billing", "ship", "release", "deploy", "gone"]) assert.equal(asksOverlord(snapshot, id), false, id);
  assert.equal(waitingTarget(snapshot, snapshot.tasks[0])?.id, "board-ui");
  for (const id of ["ship", "release", "deploy", "ask", "gone", "board-ui"]) assert.equal(waitingTarget(snapshot, snapshot.tasks.find((task) => task.id === id)!), undefined, id);
  assert.equal(statusText("waiting"), "Waiting");
});

test("a run item states its progress in plain words with its exit code", () => {
  const snapshot = parseSnapshot({ healthy: true, runs: [
    { id: "a", identity: "cfo-1", title: "Rebuild the index", shell: "pwsh", admin: false, command: "Get-Date", cwd: "C:\\work", state: "ready" },
    { id: "b", state: "running" },
    { id: "c", state: "succeeded", exit_code: 0 },
    { id: "d", state: "failed", exit_code: 2, reason: "The command exited with 2." },
    { id: "e", state: "failed", reason: "Windows asked to confirm and it was declined." },
    { id: "f", state: "expired", reason: "It waited more than 24 hours." },
  ] });
  const runs = snapshot.runs ?? [];
  assert.equal(runs[0].command, "Get-Date");
  assert.equal(runs[0].exit_code, null);
  const cases: [string, string, boolean][] = [["Ready to run", "play", false], ["Running", "clock", false], ["Finished · exit 0", "check", false],
    ["Failed · exit 2", "warning", true], ["Failed", "warning", true], ["Expired", "close", false]];
  cases.forEach(([label, icon, trouble], index) => assert.deepEqual(runMark(runs[index]), { icon, label, trouble }, runs[index].id));
});
