import { test } from "node:test";
import assert from "node:assert/strict";
import { babyFor, babyName, BABY_HEIGHT, BABY_WIDTH, branchLayout, elbows, finished, hasRunningChildren, forHowLong, formatMemory, isDimmed, LINE_GAP, running, stateWord, summarize, titleFor } from "./fleet-tree.ts";
import { arrange, detours, makeRoom, NODE_HEIGHT, NODE_WIDTH, settle, waitingOn, workflowNodes, type Extent } from "./workflow.ts";
import { parseSnapshot, type FleetTree, type TreeNode } from "./types.ts";

const MINUTE = 60_000;
const now = Date.parse("2026-10-06T21:00:00Z");
const ago = (minutes: number) => new Date(now - minutes * MINUTE).toISOString();

const child = (fields: Partial<TreeNode>): TreeNode => ({
  id: "x", kind: "subagent", group: "", parent: "", label: "", detail: "", task: "", state: "working", started: ago(10), last_activity: ago(1),
  finished: "", last_line: "", memory: 0, source_updated_at: ago(1), fetched_at: ago(0), ...fields,
});
const tree = (children: TreeNode[]): FleetTree => ({ task_id: "g", generation: "s1", harness: "claude", memory: 2 ** 30, own_memory: 0, conversation_at: "", children, unread: [], source_updated_at: "", fetched_at: "" });

test("each child is drawn as the baby goblin of its kind, a job as what it does", () => {
  for (const [fields, baby] of [
    [{ kind: "subagent" }, "subagent"], [{ kind: "shell", group: "test" }, "shell"], [{ kind: "monitor" }, "monitor"], [{ kind: "gate" }, "gate"],
    [{ kind: "process", group: "dev-server" }, "server"], [{ kind: "process", group: "test" }, "test"], [{ kind: "process", group: "build" }, "build"],
    [{ kind: "process", group: "browser" }, "browser"], [{ kind: "process", group: "other" }, "other"], [{ kind: "something new" }, "other"],
  ] as [Partial<TreeNode>, string][]) assert.equal(babyFor(child(fields)), baby, JSON.stringify(fields));
});

// The Overlord, 2026-10-08: "why are the idle ones unclickable". An idle
// child can be opened like any other, so only a finished one is dimmed.
test("a child reads as working, idle, waiting, done, failed or silent, and only finished ones are dimmed", () => {
  for (const [fields, word, dimmed] of [
    [{ state: "working" }, "Working", false], [{ kind: "process", state: "waiting" }, "Idle", false], [{ kind: "gate", state: "waiting" }, "Waiting", false],
    [{ state: "done" }, "Done", true], [{ state: "failed" }, "Failed", true], [{ state: "silent" }, "Silent", false],
  ] as [Partial<TreeNode>, string, boolean][]) {
    assert.equal(stateWord(child(fields)), word);
    assert.equal(isDimmed(child(fields)), dimmed, word);
  }
});

test("for how long: working since it started, silent since it last did anything, finished how long ago", () => {
  assert.equal(forHowLong(child({ state: "working", started: ago(4) }), now), "4m");
  assert.equal(forHowLong(child({ state: "silent", started: ago(60), last_activity: ago(14) }), now), "14m");
  assert.equal(forHowLong(child({ state: "done", finished: ago(12) }), now), "12m ago");
  assert.equal(forHowLong(child({ state: "done", finished: ago(0) }), now), "just now");
  assert.equal(forHowLong(child({ state: "working", started: "0001-01-01T00:00:00Z" }), now), "");
});

test("memory reads in megabytes under a gigabyte, and nothing for none", () => {
  assert.equal(formatMemory(0), "");
  assert.equal(formatMemory(412 * 2 ** 20), "412 MB");
  assert.equal(formatMemory(1.14 * 2 ** 30), "1.1 GB");
  assert.equal(formatMemory(1000), "1 MB");
});

test("a goblin's children at a glance: counts by state, and how many of each kind still run", () => {
  const children = [
    child({ id: "a", kind: "subagent", state: "working" }), child({ id: "b", kind: "subagent", state: "done", finished: ago(12) }),
    child({ id: "c", kind: "process", group: "dev-server", state: "waiting" }), child({ id: "d", kind: "shell", state: "silent", last_activity: ago(30) }),
    child({ id: "e", kind: "shell", state: "silent", last_activity: ago(14) }), child({ id: "f", kind: "subagent", state: "failed", finished: ago(2) }),
  ];
  const summary = summarize(tree(children));
  assert.deepEqual({ ...summary, kinds: summary.kinds }, { working: 1, silent: 2, idle: 1, kinds: [["subagent", 1], ["shell", 2], ["server", 1]] });
  assert.deepEqual(running(tree(children)).map((node) => node.id), ["a", "c", "d", "e"]);
  assert.deepEqual(finished(tree(children)).map((node) => node.id), ["f", "b"], "newest first");
});

test("a goblin has children to draw on the canvas only while one still runs or idles", () => {
  for (const [states, isDrawn] of [
    [[], false], [["done"], false], [["done", "failed"], false], [["working", "done"], true], [["waiting"], true], [["silent"], true],
  ] as [string[], boolean][]) assert.equal(hasRunningChildren(tree(states.map((state, i) => child({ id: "c" + i, state })))), isDrawn, states.join(", "));
});

test("rows under a goblin whose children show move down to make room, and keep their rows", () => {
  const positions = { cfo: { x: 400, y: 72 }, one: { x: 40, y: 316 }, two: { x: 376, y: 316 }, low: { x: 208, y: 560 } };
  assert.deepEqual(makeRoom(positions, {}), positions, "nothing open moves nothing");
  assert.deepEqual(makeRoom(positions, { one: 58 }), positions, "a count fits in the gap between rows");
  const room = makeRoom(positions, { one: 300 });
  assert.equal(room.cfo.y, 72);
  assert.equal(room.one.y, 316);
  assert.equal(room.two.y, 316, "a row moves as one");
  assert.ok(room.low.y >= 316 + NODE_HEIGHT + 300, "the row under the open goblin starts below its children");
  assert.equal(room.low.x, 208);
});

test("a snapshot carries each goblin's tree", () => {
  const snapshot = parseSnapshot({ healthy: true, instance: "x", tasks: [{ id: "g", title: "g", generation: "s1", phase: "working", verified: false,
    tree: { task_id: "g", generation: "s1", harness: "claude", memory: 3, own_memory: 1, children: [{ id: "subagent:t", kind: "subagent", label: "Map", state: "working", started: ago(4), memory: 0 }], source_updated_at: ago(0), fetched_at: ago(0) } }] });
  const parsed = snapshot.tasks[0].tree!;
  assert.equal(parsed.children[0].label, "Map");
  assert.deepEqual(parsed.unread, []);
  assert.equal(parseSnapshot({ healthy: true, instance: "x", tasks: [{ id: "q", title: "q", phase: "queued", verified: false }] }).tasks[0].tree, undefined);
});

test("a goblin the Overlord moved keeps its open children clear of every card arranged around it", () => {
  const arranged = { "task:a": { x: 0, y: 0 }, "task:b": { x: 0, y: 488 } };
  const placed = { "task:a": { x: 0, y: 300 } };
  const below = { "task:a": 400 };
  const shown = settle(arranged, placed, { "task:a": { width: NODE_WIDTH, below: below["task:a"], drops: [0] } });
  const a = shown["task:a"], b = shown["task:b"];
  assert.deepEqual(a, placed["task:a"], "his card stays where he put it");
  const clear = b.x >= a.x + NODE_WIDTH || b.x + NODE_WIDTH <= a.x || b.y >= a.y + NODE_HEIGHT + below["task:a"] || b.y + NODE_HEIGHT <= a.y;
  assert.ok(clear, `the card at ${JSON.stringify(b)} sits among the children under the moved goblin at ${JSON.stringify(a)}`);
});

// What hangs under a goblin's card with count running children.
const extent = (count: number): Extent => {
  const branches = branchLayout(count, NODE_WIDTH);
  return { width: branches.width, below: branches.height, drops: branches.drops };
};

test("a goblin's running children hang in about as many columns as rows, each clear of the others, under a block at least as wide as its card", () => {
  for (const [count, columns] of [[1, 1], [2, 2], [3, 2], [4, 2], [5, 3], [9, 3], [10, 4], [17, 4]]) {
    const branches = branchLayout(count, NODE_WIDTH);
    assert.equal(new Set(branches.places.map((place) => place.x)).size, columns, count + " children");
    assert.ok(branches.width >= NODE_WIDTH, count + " children: as wide as the card");
    for (const [i, one] of branches.places.entries()) {
      assert.ok(one.x >= 0 && one.y > 0 && one.x + BABY_WIDTH <= branches.width && one.y + BABY_HEIGHT <= branches.height, count + " children: inside the block " + JSON.stringify(one));
      for (const other of branches.places.slice(i + 1)) assert.ok(Math.abs(one.x - other.x) >= BABY_WIDTH || Math.abs(one.y - other.y) >= BABY_HEIGHT, count + " children: " + JSON.stringify([one, other]));
    }
    assert.equal(branches.drops.length, 2 + (count === 1 ? 0 : columns - 1), count + " children: lines run down at the middle and beside each column");
  }
});

interface Point { x: number; y: number }
interface Box { left: number; right: number; top: number; bottom: number }

// Where a branch line starts, turns and ends: of every point it names, its
// turns and the ends of its rounded corners, those not on a straight run.
const pointsOf = (line: string): Point[] => {
  const named = [...line.matchAll(/(-?[\d.]+),(-?[\d.]+)/g)].map((match) => ({ x: Number(match[1]), y: Number(match[2]) }));
  return named.filter((point, i) => !i || i === named.length - 1
    || !(named[i - 1].x === point.x && point.x === named[i + 1].x || named[i - 1].y === point.y && point.y === named[i + 1].y));
};
const segmentsOf = (line: string): Box[] => {
  const points = pointsOf(line);
  return points.slice(1).map((to, i) => ({ left: Math.min(points[i].x, to.x), right: Math.max(points[i].x, to.x), top: Math.min(points[i].y, to.y), bottom: Math.max(points[i].y, to.y) }));
};
// How near two segments, or a segment and a baby goblin, come.
const apart = (one: Box, other: Box) => Math.hypot(Math.max(0, one.left - other.right, other.left - one.right), Math.max(0, one.top - other.bottom, other.top - one.bottom));
const boxOf = (place: Point): Box => ({ left: place.x, right: place.x + BABY_WIDTH, top: place.y, bottom: place.y + BABY_HEIGHT });
const FAMILIES = Array.from({ length: 24 }, (_, i) => i + 1);

// The Overlord, 2026-10-08: "branches should connect to the top of their baby
// goblins".
test("each child's branch leaves the card's bottom and ends at the middle of the child's top", () => {
  for (const count of FAMILIES) {
    const branches = branchLayout(count, NODE_WIDTH);
    assert.equal(branches.lines.length, count, count + " children: a branch to each");
    for (const [i, line] of branches.lines.entries()) {
      const points = pointsOf(line);
      assert.equal(points[0].y, 0, count + " children: branch " + i + " starts at the card's bottom");
      assert.ok(Math.abs(points[0].x - branches.width / 2) < NODE_WIDTH / 2 - 16, count + " children: branch " + i + " starts under the card, clear of its corners: " + points[0].x);
      assert.deepEqual(points.at(-1), { x: branches.places[i].x + BABY_WIDTH / 2, y: branches.places[i].y }, count + " children: branch " + i + " ends at its child's top");
      assert.deepEqual(branches.ends[i], points.at(-1), count + " children: its end is marked");
    }
  }
});

// The Overlord, 2026-10-08 about 22:20Z: "child goblin branches are not
// individually distinguishable and appear to be taking weird routes when
// spacing lines can be cleaner with less overlap".
test("a goblin's branches fan out from its card side by side, evenly spaced about its middle", () => {
  for (const count of FAMILIES.slice(1)) {
    const branches = branchLayout(count, NODE_WIDTH);
    const starts = branches.lines.map((line) => pointsOf(line)[0].x).sort((one, other) => one - other);
    for (const [i, x] of starts.slice(1).entries()) assert.equal(x - starts[i], LINE_GAP, count + " children: " + starts.join(", "));
    assert.ok(Math.abs((starts[0] + starts.at(-1)!) / 2 - branches.width / 2) <= LINE_GAP / 2, count + " children: about the card's middle: " + starts.join(", "));
  }
});

test("sibling branches never meet, and keep a line's gap apart all the way", () => {
  for (const count of FAMILIES) for (const passes of [0, 1, 2]) {
    const branches = branchLayout(count, NODE_WIDTH, passes);
    const lines = [...branches.lines, ...branches.passes.map(elbows)];
    for (const [i, line] of lines.entries()) for (const [j, other] of lines.entries()) {
      if (j <= i) continue;
      const nearest = Math.min(...segmentsOf(line).flatMap((one) => segmentsOf(other).map((two) => apart(one, two))));
      assert.ok(nearest >= LINE_GAP, `${count} children, ${passes} passing: lines ${i} and ${j} come ${nearest} apart`);
    }
  }
});

test("no branch comes near a baby goblin but its own, which it enters straight down into the middle of its top", () => {
  for (const count of FAMILIES) for (const passes of [0, 1]) {
    const branches = branchLayout(count, NODE_WIDTH, passes);
    const boxes = branches.places.map(boxOf);
    for (const [i, line] of [...branches.lines, ...branches.passes.map(elbows)].entries()) {
      const segments = segmentsOf(line);
      for (const [s, segment] of segments.entries()) for (const [b, box] of boxes.entries()) {
        if (b === i && s === segments.length - 1) {
          assert.ok(segment.left === segment.right && segment.bottom === box.top, `${count} children: branch ${i} drops into its child's top`);
          continue;
        }
        assert.ok(apart(segment, box) >= LINE_GAP, `${count} children, ${passes} passing: line ${i} comes ${apart(segment, box)} from child ${b}`);
      }
    }
  }
});

test("each branch takes the direct way to its child: down, and across toward it, around only the baby goblin under the card's middle", () => {
  for (const count of FAMILIES) {
    const branches = branchLayout(count, NODE_WIDTH);
    const columns = new Set(branches.places.map((place) => place.x)).size;
    for (const [i, line] of branches.lines.entries()) {
      const points = pointsOf(line), steps = points.slice(1).map((point, j) => ({ x: point.x - points[j].x, y: point.y - points[j].y }));
      assert.ok(steps.every((step) => step.y >= 0), `${count} children: branch ${i} never climbs`);
      const ways = steps.filter((step) => step.x).map((step) => Math.sign(step.x)).filter((way, j, all) => !j || way !== all[j - 1]);
      const isAround = columns % 2 === 1 && i >= columns && i % columns === (columns - 1) / 2;
      assert.ok(ways.length <= (isAround ? 2 : 1), `${count} children: branch ${i} turns back across: ${line}`);
    }
  }
});

test("a connector to a goblin under the baby goblins leaves the card's bottom and runs down beside them on a line of its own", () => {
  for (const count of FAMILIES.slice(0, 10)) for (const passes of [1, 2]) {
    const branches = branchLayout(count, NODE_WIDTH, passes);
    assert.equal(branches.passes.length, passes);
    const left = Math.min(...branches.places.map((place) => place.x));
    for (const [j, pass] of branches.passes.map(elbows).entries()) {
      const points = pointsOf(pass);
      assert.equal(points[0].y, 0, `${count} children: pass ${j} starts at the card's bottom`);
      assert.equal(points.at(-1)!.y, branches.height, `${count} children: pass ${j} runs down past them all`);
      assert.ok(points.at(-1)!.x <= left - LINE_GAP && points.at(-1)!.x >= 0, `${count} children: pass ${j} runs down beside them, inside the block: ${pass}`);
    }
    const ends = branches.passes.map((pass) => pass.at(-1)!.x);
    assert.deepEqual(ends, [...ends].sort((one, other) => one - other), "outermost first");
  }
});

test("a goblin whose branches are wider than its card keeps every other card and its branches clear of them", () => {
  const snapshot = parseSnapshot({ healthy: true, tasks: ["a", "b", "c", "d", "e"].map((id) => ({ id, phase: "working", verified: false })) });
  const nodes = workflowNodes(snapshot);
  for (const extents of [
    { "task:b": extent(9) },
    { "task:a": extent(4), "task:c": extent(16) },
  ] as Record<string, Extent>[]) for (const canvas of [{ width: 2400, height: 800 }, { width: 836, height: 956 }]) {
    const below = Object.fromEntries(Object.entries(extents).map(([id, extent]) => [id, extent.below]));
    const positions = makeRoom(arrange(nodes, canvas, {}, extents), below);
    const blocks = Object.entries(positions).map(([id, point]) => {
      const width = extents[id]?.width || NODE_WIDTH;
      return { id, left: point.x + (NODE_WIDTH - width) / 2, right: point.x + (NODE_WIDTH + width) / 2, top: point.y, bottom: point.y + NODE_HEIGHT + (below[id] || 0) };
    });
    for (const [i, one] of blocks.entries()) for (const other of blocks.slice(i + 1)) {
      assert.ok(one.right <= other.left || other.right <= one.left || one.bottom <= other.top || other.bottom <= one.top, JSON.stringify({ canvas, one, other }));
    }
  }
});

test("a connector to a later row of goblins never drops down a line of a goblin above it, its middle or a line beside or between its branches", () => {
  const ids = ["billing", "checkout", "search", "docs", "ledger", "export", "rates"];
  const nodes = workflowNodes(parseSnapshot({ healthy: true, tasks: ids.map((id) => ({ id, phase: "working", verified: false })) }));
  for (const extents of [{}, { "task:billing": extent(5) }, { "task:billing": extent(5), "task:docs": extent(9), "task:rates": extent(2) }, { "task:search": extent(3), "task:export": extent(12) }] as Record<string, Extent>[]) {
    for (const canvas of [{ width: 836, height: 956 }, { width: 800, height: 760 }, { width: 1400, height: 900 }]) {
      const positions = arrange(nodes, canvas, {}, extents);
      const cards = nodes.filter((node) => node.task).map((node) => ({ point: positions[node.id], drops: extents[node.id]?.drops || [0] }));
      const rows = [...new Set(cards.map((card) => card.point.y))].sort((one, other) => one - other);
      assert.ok(rows.length > 1, JSON.stringify({ canvas, rows }));
      for (const card of cards) for (const above of cards.filter((other) => other.point.y < card.point.y)) for (const drop of above.drops) {
        assert.ok(Math.abs(card.point.x - (above.point.x + drop)) >= (NODE_WIDTH + 44) / 10, JSON.stringify({ extents: Object.keys(extents), canvas, card: card.point, above: above.point, drop }));
      }
    }
  }
});

// A connector to a later row runs from the bottom of the S curve under its
// parent, down to the gap above its card's row, across and into its card.
test("a connector to a later row of goblins runs down clear of every card and baby goblin above it, a line's gap from any other", () => {
  const ids = ["billing", "checkout", "search", "docs", "ledger", "export"];
  const nodes = workflowNodes(parseSnapshot({ healthy: true, tasks: ids.map((id) => ({ id, phase: "working", verified: false })) }));
  let detoured = 0;
  for (const counts of [[7, 3, 4], [5, 5, 2, 9], [3, 0, 4, 0, 6], [10, 2, 3], [1, 1, 1, 1, 1, 1], [0, 0, 0, 0, 0, 0]]) for (const canvas of [{ width: 836, height: 956 }, { width: 800, height: 760 }, { width: 600, height: 1200 }]) {
    const layouts = Object.fromEntries(counts.flatMap((count, i) => count ? [["task:" + ids[i], branchLayout(count, NODE_WIDTH)]] : []));
    const extents: Record<string, Extent> = Object.fromEntries(Object.entries(layouts).map(([id, branches]) => [id, { width: branches.width, below: branches.height, drops: branches.drops }]));
    const positions = makeRoom(arrange(nodes, canvas, {}, extents), Object.fromEntries(Object.entries(extents).map(([id, extent]) => [id, extent.below])));
    const taken = nodes.map((node) => {
      const point = positions[node.id], width = extents[node.id]?.width || NODE_WIDTH;
      return { left: point.x + (NODE_WIDTH - width) / 2, right: point.x + (NODE_WIDTH + width) / 2, top: point.y, bottom: point.y + NODE_HEIGHT + (extents[node.id]?.below || 0) };
    });
    const boxes = nodes.flatMap((node) => {
      const point = positions[node.id], branches = layouts[node.id], card = { name: node.id, left: point.x, right: point.x + NODE_WIDTH, top: point.y, bottom: point.y + NODE_HEIGHT };
      return [card, ...(branches?.places || []).map((place, i) => ({ ...boxOf({ x: point.x + (NODE_WIDTH - branches.width) / 2 + place.x, y: point.y + NODE_HEIGHT + place.y }), name: node.id + " child " + i }))];
    });
    const cfo = positions[nodes.find((node) => !node.parent)!.id], children = nodes.filter((node) => node.parent);
    const y = cfo.y + NODE_HEIGHT, targets = children.map((node) => ({ x: positions[node.id].x + NODE_WIDTH / 2, y: positions[node.id].y }));
    const ways = detours(y, targets, taken);
    const routes = targets.map((target, i) => {
      const way = ways[i], top = y + Math.min((target.y - y) / 2, 56) * 2;
      return way ? [{ x: way.x, y: top }, { x: way.x, y: way.turn }, { x: target.x, y: way.turn }, target] : [{ x: target.x, y: top }, target];
    });
    detoured += ways.filter(Boolean).length;
    for (const [i, route] of routes.entries()) {
      const name = JSON.stringify({ counts, canvas, to: children[i].id });
      const segments = segmentsOf(elbows(route));
      for (const box of boxes.filter((box) => box.name !== children[i].id)) for (const segment of segments) {
        assert.ok(apart(segment, box) >= (ways[i] ? LINE_GAP : 1), `${name} runs ${apart(segment, box)} from ${box.name}`);
      }
      for (const [j, other] of routes.entries()) if (j > i && ways[i] && ways[j]) for (const one of segments) for (const two of segmentsOf(elbows(other))) {
        assert.ok(apart(one, two) >= LINE_GAP, `${name} comes ${apart(one, two)} from the connector to ${children[j].id}`);
      }
    }
  }
  assert.ok(detoured > 10, "connectors that had to go around: " + detoured);
});

test("a sub-agent its goblin's tree holds is a baby goblin under it, not a card of its own", () => {
  const sessions = [
    { id: "own", native_id: "own", harness: "claude", role: "goblin", task_id: "g" },
    { id: "a1", native_id: "a1", harness: "claude", role: "subagent", task_id: "g", parent: "own", relation: "delegated" },
  ];
  const goblin = { id: "g", phase: "working", verified: false };
  const held = { ...goblin, tree: { task_id: "g", generation: "s1", harness: "claude", children: [{ id: "subagent:a1", kind: "subagent", state: "working" }] } };
  const cards = (task: object) => workflowNodes(parseSnapshot({ healthy: true, tasks: [task], sessions })).map((node) => node.id);
  assert.ok(cards(held).includes("session:own") && !cards(held).includes("session:a1"), "the tree holds the sub-agent: " + cards(held).join(", "));
  assert.ok(cards(goblin).includes("session:a1"), "with no tree to hold it, the sub-agent keeps its card");
});

test("a helper goblin is drawn as a goblin baby under its parent and named a helper", () => {
  assert.equal(babyFor(child({ kind: "helper" })), "helper");
  assert.deepEqual(summarize(tree([child({ id: "helper:g-h1", kind: "helper", state: "working" })])).kinds, [["helper", 1]]);
});

test("a helper its parent's tree holds is a baby goblin under its parent, not a card of its own", () => {
  const sessions = [
    { id: "own", native_id: "own", harness: "claude", role: "goblin", task_id: "g" },
    { id: "helper", native_id: "helper", harness: "claude", role: "goblin", task_id: "g-h1" },
  ];
  const helper = { id: "g-h1", parent: "g", phase: "working", verified: false };
  const parent = { id: "g", phase: "working", verified: false };
  const holding = { ...parent, tree: { task_id: "g", generation: "s1", harness: "claude", children: [{ id: "helper:g-h1", kind: "helper", state: "working" }] } };
  const cards = (tasks: object[], withSessions = true) => workflowNodes(parseSnapshot({ healthy: true, tasks, sessions: withSessions ? sessions : [] })).map((node) => node.id);
  assert.ok(cards([holding, helper]).includes("session:own") && !cards([holding, helper]).includes("session:helper"), "the tree holds the helper's session: " + cards([holding, helper]).join(", "));
  assert.ok(!cards([holding, helper], false).includes("task:g-h1"), "the tree holds the helper's task: " + cards([holding, helper], false).join(", "));
  assert.ok(cards([parent, helper]).includes("session:helper"), "a helper no tree holds keeps its card");
  assert.equal(parseSnapshot({ healthy: true, tasks: [helper] }).tasks[0].parent, "g");
  assert.equal(parseSnapshot({ healthy: true, tasks: [parent] }).tasks[0].parent, "");
});

test("a helper no family tree holds hangs under its parent's card, never the CFO's", () => {
  const cfo = { id: "cfo", native_id: "cfo", harness: "claude", role: "cfo", generation: "c1" };
  const own = { id: "own", native_id: "own", harness: "claude", role: "goblin", task_id: "g", generation: "s1", parent: "cfo" };
  const helperSession = { id: "helper", native_id: "helper", harness: "claude", role: "goblin", task_id: "g-h1", generation: "s2", parent: "cfo" };
  const parent = { id: "g", phase: "paused", verified: false, generation: "s1" };
  const helper = { id: "g-h1", parent: "g", phase: "working", verified: false, generation: "s2" };
  const parentOf = (tasks: object[], sessions: object[]) => Object.fromEntries(workflowNodes(parseSnapshot({ healthy: true, tasks, sessions }))
    .map((node) => [node.id, [node.parent, node.relation]]));
  for (const [name, tasks, sessions, card, under] of [
    ["no session reported", [parent, helper], [], "task:g-h1", "task:g"],
    ["both sessions reported", [{ ...parent, session: "own" }, { ...helper, session: "helper" }], [cfo, own, helperSession], "session:helper", "session:own"],
    ["only the parent's reported", [{ ...parent, session: "own" }, helper], [cfo, own], "task:g-h1", "session:own"],
    ["only the helper's reported", [parent, { ...helper, session: "helper" }], [cfo, helperSession], "session:helper", "task:g"],
  ] as [string, object[], object[], string, string][]) {
    assert.deepEqual(parentOf(tasks, sessions)[card], [under, "Helper goblin"], name);
  }
  assert.deepEqual(parentOf([parent, helper], [])["task:g"], ["cfo:primary", "Dispatched by the CFO"], "its parent still hangs under the CFO");
  assert.equal(parentOf([helper], [])["task:g-h1"][0], "cfo:primary", "a helper whose parent is gone hangs under the CFO");
});

test("a goblin waiting on the helper hung under it draws no dashed line to it", () => {
  const waiting = { id: "g", phase: "waiting", waiting_on: "g-h1", reason: "merging its work", verified: false, generation: "s1" };
  const helper = { id: "g-h1", parent: "g", phase: "working", verified: false, generation: "s2" };
  const other = { id: "o", phase: "working", verified: false, generation: "s3" };
  const snapshot = (tasks: object[]) => parseSnapshot({ healthy: true, tasks });
  const helperWait = snapshot([waiting, helper]);
  assert.deepEqual(waitingOn(helperWait, workflowNodes(helperWait)), {});
  const otherWait = snapshot([{ ...waiting, waiting_on: "o" }, helper, other]);
  assert.deepEqual(waitingOn(otherWait, workflowNodes(otherWait)), { "task:g": "task:o" });
});

// The Overlord, 2026-10-08: "the same goblin naming convention like Kip Jr.
// Kip II etc with a job title for that specific agent session".
test("a baby goblin's title is the one who does what its own description says, or else its kind's", () => {
  for (const [fields, title] of [
    [{ label: "Start the board" }, "Board Starter"], [{ label: "Test dictation" }, "Dictation Tester"],
    [{ label: "Map harness plumbing" }, "Plumbing Mapper"], [{ label: "Research MCP OAuth" }, "OAuth Researcher"],
    [{ label: "Run the affected Go tests", kind: "shell" }, "Test Runner"], [{ label: "Measure the branches" }, "Branch Measurer"],
    [{ label: "Verify the replies." }, "Reply Verifier"], [{ label: "review hosted producer" }, "Producer Reviewer"], [{ label: "Explore" }, "Explorer"],
    [{ label: "Dev server :5173", kind: "process", group: "dev-server" }, "Server Keeper"], [{ label: "CI checks on PR 398", kind: "monitor" }, "Watcher"],
    [{ label: 'C:\\WINDOWS\\System32\\cmd.exe /d /s /c "npm run dev"', kind: "process", group: "other" }, "Process Wrangler"], [{ label: "Sub-agent" }, "Scout"],
  ] as [Partial<TreeNode>, string][]) assert.equal(titleFor(child(fields)), title, JSON.stringify(fields));
});

test("baby goblins are named after their goblin in the order they started, Jr. then II and on, and a helper keeps its own name", () => {
  const children = [
    child({ id: "c", label: "Check the list", started: ago(30) }), child({ id: "a", label: "Start the board", started: ago(50) }),
    child({ id: "h", kind: "helper", label: "Pip - Rail Fitter", started: ago(45) }), child({ id: "b", label: "Test dictation", started: ago(40), state: "done" }),
    ...Array.from({ length: 8 }, (_, i) => child({ id: "z" + i, label: "Trace the export", started: ago(20 - i) })),
  ];
  const goblin = parseSnapshot({ healthy: true, tasks: [{ id: "g", title: "Fix it", goblin_name: "Kip", goblin_title: "Echo Chaser", phase: "working", verified: false, tree: tree(children) }] }).tasks[0];
  const name = (id: string) => babyName(goblin, children.find((node) => node.id === id)!);
  assert.deepEqual(["a", "b", "c", "z0", "z7", "h"].map(name), ["Kip Jr. - Board Starter", "Kip II - Dictation Tester", "Kip III - List Checker", "Kip IV - Export Tracer", "Kip XI - Export Tracer", "Pip - Rail Fitter"]);
  assert.equal(babyName({ ...goblin, goblin_name: "" }, children[1]), "Board Starter", "a goblin with no name yet: the title alone");
});
