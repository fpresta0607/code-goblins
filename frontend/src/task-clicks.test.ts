import { test } from "node:test";
import assert from "node:assert/strict";
import { withClicks, type TaskClick } from "./task-clicks.ts";
import { nodeStatus, taskColumn } from "./workflow.ts";
import { parseSnapshot, type Snapshot } from "./types.ts";

const snapshot = (revision: number): Snapshot => parseSnapshot({
  healthy: true, revision,
  tasks: [
    { id: "queued-one", phase: "queued", brief: true, starting: false, start_error: "", verified: false },
    { id: "paused-one", phase: "paused", generation: "s1", verified: false, lifecycle: { phase: "paused", action: "pause", at: "2026-10-08T11:00:00Z", kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false } },
    { id: "working-one", phase: "working", generation: "s2", verified: false },
  ],
});
const card = (shown: Snapshot, id: string) => {
  const task = shown.tasks.find((candidate) => candidate.id === id)!;
  return { status: nodeStatus({ id, title: id, task, relation: "" }, false, shown.tasks), column: taskColumn(task) };
};

// The Overlord, 2026-10-08: "it took multiple clicks", and resuming and
// starting "take a long time to adjust". A click shows on its card in the
// frame he clicks, before the supervisor answers, and stays until a snapshot
// from the revision the supervisor answered with shows it.
for (const [action, id, status] of [
  ["start", "queued-one", "Starting"],
  ["resume", "paused-one", "Resuming"],
  ["pause", "working-one", "Pausing"],
  ["stop", "working-one", "Stopping"],
] as const) {
  test(`a ${action} shows on its card at once and until a snapshot of it arrives`, () => {
    // Arrange
    const clicked = (revision: number | null): ReadonlyMap<string, TaskClick> => new Map([[id, { action, revision }]]);

    // Act
    const onClick = card(withClicks(snapshot(7), clicked(null)), id);
    const answered = card(withClicks(snapshot(7), clicked(9)), id);
    const caughtUp = card(withClicks(snapshot(9), clicked(9)), id);

    // Assert
    assert.equal(onClick.status, status, "in the frame he clicks");
    assert.equal(answered.status, status, "until a snapshot from the answer's revision arrives");
    assert.equal(caughtUp.status, card(snapshot(9), id).status, "a snapshot that shows the click shows what it says");
  });
}

test("a Resume keeps the card where paused goblins sit while it resumes", () => {
  const shown = withClicks(snapshot(7), new Map([["paused-one", { action: "resume", revision: null }]]));
  assert.equal(card(shown, "paused-one").column, "Paused");
});

test("a snapshot with no click pending is drawn as it came", () => {
  const received = snapshot(9);
  assert.equal(withClicks(received, new Map()), received);
  assert.equal(withClicks(received, new Map([["paused-one", { action: "resume", revision: 8 }]])), received);
});

test("a queued task being removed says Removing", () => {
  const shown = withClicks(snapshot(7), new Map([["queued-one", { action: "stop", revision: null }]]));
  assert.equal(card(shown, "queued-one").status, "Removing");
});
