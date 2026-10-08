import assert from "node:assert/strict";
import test from "node:test";
import { carLook, isTrainOver, trainLook } from "./merge-train.ts";
import type { MergeTrain, TrainCar } from "./types.ts";

const PR = "https://github.com/o/r/pull/";
const car = (number: number, state: string, note = ""): TrainCar => ({ number, url: PR + number, title: "change " + number, task: "g" + number, state, note });
const train = (overrides: Partial<MergeTrain>): MergeTrain => ({
  id: "r-20261007-160000", repository: "o/r", base: "main", pr: PR + "900", state: "testing", runs: 1,
  started: "2026-10-07T16:00:00Z", finished: "", note: "", cars: [], ...overrides,
});

test("a train says in plain words where it stands, in its tone, and opens its pull request", () => {
  for (const [given, text, tone] of [
    [train({ cars: [car(1, "riding"), car(2, "riding")] }), "CI tests #1, #2", "pending"],
    [train({ runs: 3, cars: [car(1, "landed"), car(2, "riding"), car(3, "waiting")] }), "CI tests #2, run 3", "pending"],
    [train({ state: "landed", cars: [car(1, "landed"), car(2, "landed"), car(3, "conflict")] }), "Landed #1, #2", "passed"],
    [train({ state: "stopped", cars: [car(1, "landed"), car(2, "culprit"), car(3, "returned")] }), "#2 breaks CI. Landed #1", "failed"],
    [train({ state: "stopped", cars: [car(1, "conflict")] }), "Nothing merged cleanly", "cancelled"],
    [train({ state: "failed", cars: [car(1, "landed"), car(2, "returned")] }), "Train failed. Landed #1", "failed"],
  ] as const) {
    assert.deepEqual(trainLook(given), { text, tone, url: PR + "900" });
  }
});

test("each pull request on a train says where it stands and keeps the train's reason in its tip", () => {
  assert.deepEqual(carLook(car(7, "culprit", "failed: test (https://github.com/o/r/actions/runs/1)")), { label: "#7", url: PR + "7", text: "Breaks CI", tone: "failed", tip: "failed: test (https://github.com/o/r/actions/runs/1)" });
  assert.deepEqual(["riding", "waiting", "landed", "conflict", "returned"].map((state) => carLook(car(1, state)).text), ["Testing", "Waits its turn", "Landed", "Conflicts", "Next train"]);
});

test("a link off GitHub is not opened", () => {
  assert.equal(trainLook(train({ pr: "javascript:alert(1)" })).url, "");
  assert.equal(carLook({ ...car(1, "riding"), url: "https://evil.example/pull/1" }).url, "");
});

test("only a testing train is still running", () => {
  assert.deepEqual(["testing", "landed", "stopped", "failed"].map((state) => isTrainOver(train({ state }))), [false, true, true, true]);
});
