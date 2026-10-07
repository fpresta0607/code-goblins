import { test } from "node:test";
import assert from "node:assert/strict";
import { capacityLine, diskBlock, diskScale, diskState, freeGigabytes, holdersLine, memoryBlock, meterScale, meterState, nextChip, nextInOrder, poolWarning, queueBlock, refusalStands, slotBlock, startBlock, startOutcome, tighter } from "./start.ts";
import { nodeStatus } from "./workflow.ts";
import { parseSnapshot, type Disk, type Memory, type Snapshot, type Task } from "./types.ts";

const GB = 2 ** 30;
// memory is a machine with available GB of memory and, unless given, ample
// commit and healthy kernel pools.
const memory = (available: number, commit = 40): Memory => ({ available: available * GB, total: 32 * GB, commit_available: commit * GB, commit_limit: 48 * GB, paged_pool: 0.5 * GB, nonpaged_pool: 0.3 * GB, floor: 4 * GB, next: 5 * GB, holders: [] });
const task = (changes: Partial<Task> = {}) => parseSnapshot({ healthy: true, tasks: [{ id: "next-task", phase: "queued", brief: true, starting: false, start_error: "", verified: false, ...changes }] }).tasks[0];

test("the meter says when the CFO starts the next task, and never that one starts by itself", () => {
  assert.deepEqual(meterState(memory(5.2)), { tone: "ready", text: "Enough memory: the CFO starts the next task." });
  assert.deepEqual(meterState(memory(4)), { tone: "waiting", text: "The CFO starts the next task at 5 GB free." });
  assert.deepEqual(meterState(memory(4.99)), { tone: "waiting", text: "The CFO starts the next task at 5 GB free." });
  assert.deepEqual(meterState(memory(5)), { tone: "ready", text: "Enough memory: the CFO starts the next task." });
  assert.deepEqual(meterState(memory(3.99)), { tone: "under", text: "Under the 4 GB floor: nothing starts until memory frees." });
});

test("the memory bar spans twice the start mark, so the floor and start marks sit apart and a full bar means the next task starts", () => {
  assert.deepEqual(meterScale(memory(4.5)), { fill: 45, floor: 40, next: 50 }, "the 4 GB floor and 5 GB next mark span a 10 GB bar");
  assert.deepEqual(meterScale(memory(20)), { fill: 100, floor: 40, next: 50 }, "memory well past the mark fills the bar");
  assert.deepEqual(meterScale(memory(0)), { fill: 0, floor: 40, next: 50 });
  assert.deepEqual(meterScale({ ...memory(2), total: 5 * GB }), { fill: 40, floor: 80, next: 100 }, "a machine with less memory than that spans its own");
});

test("commit the machine does not report is unknown, not zero, so the meter shows memory", () => {
  const unreported = { ...memory(7.5), commit_available: 0, commit_limit: 0 };
  assert.deepEqual(tighter(unreported), { isCommit: false, free: 7.5 * GB, total: 32 * GB });
  assert.deepEqual(meterScale(unreported), { fill: 75, floor: 40, next: 50 });
  assert.deepEqual(meterState(unreported), { tone: "ready", text: "Enough memory: the CFO starts the next task." });
  assert.equal(memoryBlock(unreported), "");
  assert.equal(tighter(memory(7.5, 0)).isCommit, true, "commit that is reported and used up is still the tighter");
});

test("Start is offered while a queued task can start, and otherwise says why not", () => {
  assert.equal(startBlock(task(), memory(5), false), "");
  assert.equal(startBlock(task(), memory(4.9), false), "Needs 5 GB free to keep the 4 GB floor");
  assert.equal(startBlock(task(), null, false), "", "a board that cannot read memory leaves the check to the supervisor");
  assert.equal(startBlock(task({ brief: false }), memory(5), false), "");
  assert.equal(startBlock(task(), memory(3.9), false), "Needs 5 GB free to keep the 4 GB floor");
  assert.equal(startBlock(task(), memory(5), true), "Another task is starting");
  assert.equal(startBlock(task({ starting: true }), memory(5), true), "Starting", "its own start in flight");
});

test("a queued task that already finished offers no Start, and its card says so and why", () => {
  // Arrange
  const why = "Already finished: its last report was done (2026-10-06 11:59Z: done: PR 9); it never starts again by itself. Move its row to ## Done, or queue new work under a new id";
  const finished = task({ finished: why });

  // Act and assert
  assert.equal(queueBlock(finished), why);
  assert.equal(startBlock(finished, memory(9), false), why);
  assert.equal(nodeStatus({ id: finished.id, title: finished.id, task: finished, relation: "" }), "Already finished");
  assert.equal(queueBlock(task()), "", "work nothing says finished still starts");
});

test("free memory rounds down, so just under the floor or start mark never reads as ready", () => {
  assert.equal(freeGigabytes(3.97 * GB), "3.9");
  assert.equal(freeGigabytes(4 * GB), "4.0");
  assert.equal(freeGigabytes(4.99 * GB), "4.9");
  assert.equal(freeGigabytes(5.26 * GB), "5.2");
});

test("the next eligible task waits for 5 GB when memory is short", () => {
  assert.equal(nextChip(memory(5)), "Next up");
  assert.equal(nextChip(memory(4.2)), "Next at 5 GB");
  assert.equal(nextChip(null), "Next up");
});

test("the meter shows commit instead of memory only while commit is the tighter", () => {
  assert.deepEqual(tighter(memory(7.3, 20)), { isCommit: false, free: 7.3 * GB, total: 32 * GB });
  assert.deepEqual(tighter(memory(3.4, 2.5)), { isCommit: true, free: 2.5 * GB, total: 48 * GB });
  assert.deepEqual(tighter(memory(5, 5)), { isCommit: false, free: 5 * GB, total: 32 * GB }, "a tie shows memory, as the meter always has");
  assert.deepEqual(meterState(memory(16, 3.5)), { tone: "under", text: "Under the 4 GB floor: nothing starts until commit frees." });
  assert.equal(meterState(memory(16, 4.5)).tone, "waiting");
  assert.deepEqual(meterScale(memory(16, 4.5)), { fill: 45, floor: 40, next: 50 });
  assert.deepEqual(meterScale({ ...memory(16, 2), commit_limit: 5 * GB }), { fill: 40, floor: 80, next: 100 }, "a commit limit under the bar's span spans its own");
});

test("Start, Resume and the next task name commit when it is the one short, and clear once both reach 5 GB", () => {
  assert.equal(startBlock(task(), memory(4.9, 40), false), "Needs 5 GB free to keep the 4 GB floor");
  assert.equal(startBlock(task(), memory(16, 4.9), false), "Needs 5 GB of commit free to keep the 4 GB floor");
  assert.equal(startBlock(task(), memory(5, 5), false), "");
  assert.equal(memoryBlock(memory(16, 2.5)), "5 GB of commit free to keep the 4 GB floor");
  assert.equal(memoryBlock(memory(4.9, 2.5)), "5 GB of commit free to keep the 4 GB floor", "both short names the tighter");
  assert.equal(memoryBlock(memory(5, 5)), "");
  assert.equal(memoryBlock(null), "");
  assert.equal(nextChip(memory(16, 4.2)), "Next at 5 GB");
  assert.equal(nextChip(memory(5, 5)), "Next up");
});

test("the meter names the apps holding the most commit only while commit is the tighter", () => {
  const holders = [{ name: "ChatGPT", commit: 11.2 * GB }, { name: "claude", commit: 5.7 * GB }, { name: "cfo", commit: 4.3 * GB }];
  assert.equal(holdersLine({ ...memory(3.4, 2.5), holders }), "Most commit: ChatGPT 11.2 GB, claude 5.7 GB, cfo 4.3 GB");
  assert.equal(holdersLine({ ...memory(3.4, 20), holders }), "", "memory is the tighter");
  assert.equal(holdersLine(memory(3.4, 2.5)), "", "no holders read, no line");
});

test("a paged pool past 4 GB says Windows holds it and a restart frees it", () => {
  assert.equal(poolWarning({ ...memory(7), paged_pool: 4 * GB }), "");
  assert.equal(poolWarning({ ...memory(7), paged_pool: 15.6 * GB }), "Paged pool 15.6 GB: Windows is holding this in its kernel paged pool, memory no goblin can use; restarting the PC frees it.");
  assert.match(poolWarning({ ...memory(7), paged_pool: 4 * GB + 1 }), /^Paged pool/);
});

test("the snapshot's memory carries commit, the kernel pools and the apps holding the most commit", () => {
  // Arrange
  const wire = { available: 1, total: 2, commit_available: 3, commit_limit: 4, paged_pool: 5, nonpaged_pool: 6, floor: 7, next: 8 };

  // Act
  const withHolders = parseSnapshot({ healthy: true, memory: { ...wire, holders: [{ name: "ChatGPT", commit: 9 }] } }).memory;
  const withoutHolders = parseSnapshot({ healthy: true, memory: wire }).memory;

  // Assert
  assert.deepEqual(withHolders, { ...wire, holders: [{ name: "ChatGPT", commit: 9 }] });
  assert.deepEqual(withoutHolders, { ...wire, holders: [] });
});

test("an accepted Start opens its goblin once its session is up, stops on a failure of this start, and otherwise waits", () => {
  // Arrange
  const snapshot = (revision: number, changes: Partial<Task>) => ({ revision, tasks: [task({ generation: "", ...changes })] }) as Snapshot;
  const accepted = { id: "next-task", revision: 12 };
  const cases: [string, Snapshot, "open" | "failed" | "wait"][] = [
    ["its session is up", snapshot(13, { phase: "running", generation: "gen-1" }), "open"],
    ["this start failed", snapshot(13, { start_error: "GITHUB_TOKEN is red" }), "failed"],
    ["this start failed, in the snapshot the start answered", snapshot(12, { start_error: "GITHUB_TOKEN is red" }), "failed"],
    ["a retry, where the older snapshot still shows the last start's failure", snapshot(11, { start_error: "GITHUB_TOKEN is red" }), "wait"],
    ["it is still starting", snapshot(13, { starting: true }), "wait"],
    ["the snapshot does not list it", { revision: 13, tasks: [] as Task[] } as Snapshot, "wait"],
  ];

  for (const [name, shown, want] of cases) {
    // Act
    const outcome = startOutcome(accepted, shown);

    // Assert
    assert.equal(outcome, want, name);
  }
});

test("a passing refusal lapses once a newer snapshot shows Start no longer blocked, and a standing one stays", () => {
  // Arrange
  const passing = { reason: "next-task is starting; start another once it is up", revision: 12, passing: true };
  const standing = { reason: "The brief for next-task names no project", revision: 12, passing: false };
  const cases: [string, typeof passing, number, string, boolean][] = [
    ["a passing refusal, in the snapshot it arrived at", passing, 12, "", true],
    ["a passing refusal, in an older snapshot", passing, 11, "", true],
    ["a passing refusal, in a newer snapshot that still blocks it", passing, 13, "Another task is starting", true],
    ["a passing refusal, in a newer snapshot in which it can start", passing, 13, "", false],
    ["a standing refusal, in a newer snapshot in which it can start", standing, 13, "", true],
    ["a standing refusal, many snapshots later", standing, 40, "", true],
  ];

  for (const [name, refusal, revision, blocked, want] of cases) {
    // Act
    const stands = refusalStands(refusal, { revision, tasks: [task()] } as Snapshot, blocked);

    // Assert
    assert.equal(stands, want, name);
  }
});

// capped is a machine with ample memory and the given live goblins, cap,
// configured maximum and slots left.
const capped = (live: number, limit: number, configured: number): Memory => ({ ...memory(9), capacity: { live, limit, configured, slots: Math.max(0, limit - live) } });

test("the cap line says how many goblins are live against the cap, and the setting when memory lowers it", () => {
  const cases: [string, Memory, { live: string; note: string }][] = [
    ["under the setting", capped(3, 8, 8), { live: "3 of 8", note: "" }],
    ["memory lowers the cap", capped(5, 5, 8), { live: "5 of 5", note: "Memory allows 5 of the 8 set" }],
  ];
  for (const [name, machine, want] of cases) assert.deepEqual(capacityLine(machine.capacity!), want, name);
});

test("a Start or Resume past the cap is refused on the board with its reason", () => {
  // Arrange
  const cases: [string, Memory | null, string][] = [
    ["a slot is free", capped(3, 8, 8), ""],
    ["the setting is reached", capped(8, 8, 8), "No free slot: 8 of 8 goblins live"],
    ["memory lowers the cap to what is live", capped(5, 5, 8), "No free slot: 5 of 5 goblins live"],
    ["a board that reads no cap", memory(9), ""],
    ["a board that reads no memory", null, ""],
  ];

  for (const [name, machine, want] of cases) {
    // Act
    const blocked = startBlock(task(), machine, false);

    // Assert
    assert.equal(slotBlock(machine), want, name);
    assert.equal(blocked, want, name);
  }
});

test("Next sits on the card the supervisor's order for a free slot takes first", () => {
  // Arrange
  const now = Date.parse("2026-10-06T12:00:00Z");
  const paused = (id: string, reason: string, until: string, at: string) => ({ id, phase: "paused", generation: id + "-1", verified: false,
    lifecycle: { phase: "paused", action: "pause", at, kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason, until, at } } });
  const queued = (id: string, fields: Record<string, unknown> = {}) => ({ id, phase: "queued", brief: true, verified: false, ...fields });
  const board = (machine: Memory, tasks: Record<string, unknown>[]) => parseSnapshot({ healthy: true, memory: machine, tasks });
  const cases: [string, Snapshot, ReturnType<typeof nextInOrder>][] = [
    ["only the queue", board(memory(9), [queued("first"), queued("second")]), { id: "first", text: "Next up", tone: "" }],
    ["the queue while memory is short", board(memory(4.2), [queued("first")]), { id: "first", text: "Next at 5 GB", tone: "waiting" }],
    ["a queued task waiting on another goblin is passed over", board(memory(9), [queued("held", { dependencies: ["other"] }), queued("free")]), { id: "free", text: "Next up", tone: "" }],
    ["a queued task that already finished is passed over", board(memory(9), [queued("done", { finished: "Already finished: its last report was done" }), queued("free")]), { id: "free", text: "Next up", tone: "" }],
    ["a goblin paused for memory resumes before the queue", board(memory(4.2), [queued("first"), paused("later", "memory", "", "2026-10-06T11:00:00Z"), paused("older", "memory", "", "2026-10-06T10:00:00Z")]), { id: "older", text: "Next up", tone: "waiting" }],
    ["an allowance whose reset passed counts as cleared", board(memory(9), [queued("first"), paused("reset", "allowance", "2026-10-06T11:59:00Z", "2026-10-05T10:00:00Z")]), { id: "reset", text: "Next up", tone: "" }],
    ["an allowance not yet reset holds no slot", board(memory(9), [queued("first"), paused("later", "allowance", "2026-10-06T13:00:00Z", "2026-10-05T10:00:00Z")]), { id: "first", text: "Next up", tone: "" }],
    ["a goblin blocked on a date does not stop the queue", board(memory(9), [queued("first"), paused("dated", "dependency", "date:2026-10-07T09:00:00Z", "2026-10-05T10:00:00Z")]), { id: "first", text: "Next up", tone: "" }],
    ["a goblin waiting on the Overlord never holds Next", board(memory(9), [queued("first"), paused("his", "overlord", "", "2026-10-05T10:00:00Z"), paused("asks", "question", "q-1", "2026-10-05T10:00:00Z")]), { id: "first", text: "Next up", tone: "" }],
    ["a reported production defect jumps paused goblins and the queue", board(memory(9), [queued("first"), queued("urgent", { priority: "production-defect" }), paused("older", "memory", "", "2026-10-06T10:00:00Z")]), { id: "urgent", text: "Production defect: jumps the queue", tone: "defect" }],
    ["nothing to start", board(memory(9), []), null],
  ];

  for (const [name, snapshot, want] of cases) {
    // Act
    const next = nextInOrder(snapshot, now);

    // Assert
    assert.deepEqual(next, want, name);
  }
});

// disk is a drive with free GB of 900 free, the fleet's 15 GB floor and 10 GB mark.
const disk = (free: number): Disk => ({ drive: "C:", free: free * GB, total: 900 * GB, floor: 15 * GB, wake: 10 * GB });

test("the disk meter says when no goblin starts and when the CFO is woken", () => {
  assert.equal(diskState(disk(48)).tone, "ready");
  assert.deepEqual(diskState(disk(14.9)), { tone: "waiting", text: "Under the 15 GB floor: no goblin or gate test run starts until disk frees." });
  assert.equal(diskState(disk(15)).tone, "ready");
  assert.deepEqual(diskState(disk(9.9)), { tone: "under", text: "Under the 10 GB mark: the CFO is woken, and nothing starts until disk frees." });
});

test("the disk bar spans twice the floor, with the mark and the floor inside it", () => {
  assert.deepEqual(diskScale(disk(12)), { fill: 40, wake: 10 / 30 * 100, floor: 50 });
  assert.deepEqual(diskScale(disk(400)), { fill: 100, wake: 10 / 30 * 100, floor: 50 });
  assert.deepEqual(diskScale({ ...disk(5), total: 20 * GB }), { fill: 25, wake: 50, floor: 75 }, "a drive smaller than that spans its own size");
});

test("a start under the disk floor names the floor and the free disk", () => {
  assert.equal(diskBlock(null), "");
  assert.equal(diskBlock(disk(20)), "");
  assert.equal(diskBlock(disk(13.85)), "15 GB of free disk (13.8 GB free)");
  assert.equal(startBlock(task(), memory(8), false, disk(13.85)), "Needs 15 GB of free disk (13.8 GB free)");
  assert.equal(startBlock(task(), memory(8), false, disk(30)), "");
});

test("a snapshot carries the disk reading, and none when the board cannot read it", () => {
  assert.deepEqual(parseSnapshot({ healthy: true, disk: { drive: "C:", free: 1, total: 2, floor: 3, wake: 4 } }).disk, { drive: "C:", free: 1, total: 2, floor: 3, wake: 4 });
  assert.equal(parseSnapshot({ healthy: true }).disk, null);
});
