import assert from "node:assert/strict";
import test from "node:test";
import { cloudWork, cloudWorkSummary } from "./cloud-work.ts";
import type { CloudSighting, Snapshot, Task } from "./types.ts";

const sighting = (changes: Partial<CloudSighting>): CloudSighting => ({ pull_request: "https://github.com/o/r/pull/210", title: "Name the bad header", author: "o", head: "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00", is_teammates: false, seen_at: "2026-11-25T19:00:00Z", is_left: false, ...changes });
const task = (id: string, cloud: CloudSighting[] | undefined): Task => ({ id, generation: id + "-1", title: "Parse headers", cloud_handoff: cloud === undefined ? undefined : { pull_request: "https://github.com/o/r/pull/209", cloud } }) as Task;
const snapshot = (tasks: Task[]): Snapshot => ({ tasks }) as Snapshot;

test("cloud work lists each pull request he has not left, with the session it was seen in", () => {
  // Arrange
  const board = snapshot([
    task("cg-parser", [sighting({}), sighting({ pull_request: "https://github.com/o/r/pull/211", is_left: true })]),
    task("cg-running", undefined),
    task("cg-empty", []),
  ]);

  // Act
  const work = cloudWork(board);

  // Assert
  assert.deepEqual(work.map((entry) => [entry.task.id, entry.sighting.pull_request]), [["cg-parser", "https://github.com/o/r/pull/210"]]);
});

test("a teammate's cloud work says Bring back is his press", () => {
  for (const [isTeammates, says] of [[false, "Name the bad header, at c0ffee0"], [true, "Name the bad header, at c0ffee0, opened by teammate, not by you: Bring back is your press"]] as const) {
    // Arrange
    const [entry] = cloudWork(snapshot([task("cg-parser", [sighting({ author: isTeammates ? "teammate" : "o", is_teammates: isTeammates })])]));

    // Act
    const summary = cloudWorkSummary(entry.sighting);

    // Assert
    assert.equal(summary, says);
  }
});
