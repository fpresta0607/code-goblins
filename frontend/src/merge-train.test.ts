import assert from "node:assert/strict";
import test from "node:test";
import { batchCars, batchRuns, cardCars, carLook, carName, isTrainOver, trainLook } from "./merge-train.ts";
import type { MergeTrain, TrainCar, TrainCheck, TrainRun } from "./types.ts";

const PR = "https://github.com/o/r/pull/";
const car = (number: number, state: string, note = ""): TrainCar => ({ number, url: PR + number, title: "change " + number, task: "g" + number, goblin: "", goblin_title: "", head: "", state, note });
const train = (overrides: Partial<MergeTrain>): MergeTrain => ({
  id: "r-20261007-160000", repository: "o/r", base: "main", pr: PR + "900", state: "testing", runs: 1,
  started: "2026-10-07T16:00:00Z", finished: "", note: "", cars: [], history: [], earlier: [], ...overrides,
});
const run = (number: number, riders: number[], result: string, link = "", failed_once: TrainCheck[] = []): TrainRun => ({ number, riders, base: "b".repeat(40), head: "c".repeat(40), pushed: "2026-10-07T16:00:00Z", result, link, failed_once });
// The check of 2026-10-10 that failed by chance: one key took 1.007 s
// against a limit of 1 s.
const CHANCE: TrainCheck = { name: "go (conpty)", link: "https://github.com/o/r/actions/runs/7/job/71" };
// The Overlord's two cards of 2026-10-08: a train main moved under three
// times, and the train that landed its pull requests with two more.
const failed = train({ id: "r-1", pr: PR + "522", state: "failed", runs: 3, cars: [car(517, "returned"), car(519, "returned")], history: [run(1, [517, 519], "moved"), run(2, [517, 519], "moved"), run(3, [517, 519], "moved")] });
const landed = train({ id: "r-2", pr: PR + "526", state: "landed", runs: 1, earlier: [failed], cars: [car(517, "landed"), car(519, "landed"), car(520, "landed"), car(524, "landed"), car(523, "conflict")], history: [run(1, [517, 519, 520, 524], "landed")] });

test("a train says in plain words where it stands, in its tone, and opens its pull request", () => {
  for (const [given, text, tone] of [
    [train({ cars: [car(1, "riding"), car(2, "riding")] }), "CI tests #1, #2", "pending"],
    [train({ runs: 3, cars: [car(1, "landed"), car(2, "riding"), car(3, "waiting")] }), "CI tests #2, run 3", "pending"],
    [train({ cars: [car(1, "riding"), car(2, "riding")], history: [run(1, [1, 2], "", "", [CHANCE])] }), "CI tests #1, #2 again", "pending"],
    [train({ runs: 2, cars: [car(1, "riding")], history: [run(1, [1, 2], "failed", "", [CHANCE]), run(2, [1], "")] }), "CI tests #1, run 2", "pending"],
    [train({ state: "landed", cars: [car(1, "landed"), car(2, "landed"), car(3, "conflict")] }), "Landed", "passed"],
    [train({ state: "landed", runs: 2, cars: [car(1, "landed")] }), "Landed on run 2", "passed"],
    [train({ state: "stopped", runs: 3, cars: [car(1, "landed"), car(2, "culprit"), car(3, "returned")] }), "Landed on run 3", "passed"],
    [train({ state: "stopped", cars: [car(2, "culprit"), car(3, "returned")] }), "#2 breaks CI", "failed"],
    [train({ state: "stopped", cars: [car(1, "conflict")] }), "Nothing merged cleanly", "cancelled"],
    [train({ state: "failed", cars: [car(1, "landed"), car(2, "returned")] }), "Landed", "passed"],
    [failed, "Train failed, main kept moving", "failed"],
    [train({ state: "failed", cars: [car(1, "returned")], history: [run(1, [1], "failed")] }), "Train failed, main is red", "failed"],
    [train({ state: "failed", cars: [car(1, "returned")], history: [run(1, [1], "stopped")] }), "Train failed", "failed"],
  ] as const) {
    assert.deepEqual(trainLook(given), { text, tone, url: given.pr }, text);
  }
});

test("a train that took on one that landed nothing counts its runs on from it", () => {
  assert.deepEqual(trainLook(landed), { text: "Landed on run 4", tone: "passed", url: PR + "526" });
  const retrying = train({ runs: 1, earlier: [failed], cars: [car(517, "riding"), car(519, "riding")] });
  assert.equal(trainLook(retrying).text, "CI tests #517, #519, run 4");
});

test("a finished train's card lists only what it landed, and a running one what it carries", () => {
  assert.deepEqual(cardCars(landed).map((each) => each.number), [517, 519, 520, 524]);
  assert.deepEqual(cardCars(failed), []);
  const running = train({ cars: [car(1, "landed"), car(2, "riding"), car(3, "waiting"), car(4, "conflict"), car(5, "returned")] });
  assert.deepEqual(cardCars(running).map((each) => each.number), [1, 2, 3]);
});

test("a train's panel lists every run of its batch, numbered on from the trains it took on, each opening its run", () => {
  const check = "https://github.com/o/r/actions/runs/7";
  const red = train({ id: "r-3", pr: PR + "530", state: "stopped", runs: 1, cars: [car(531, "culprit")], history: [run(1, [531], "failed", check)] });
  assert.deepEqual(batchRuns(landed), [
    { number: 1, riders: [517, 519], text: "Main moved", tone: "cancelled", url: PR + "522", once: [] },
    { number: 2, riders: [517, 519], text: "Main moved", tone: "cancelled", url: PR + "522", once: [] },
    { number: 3, riders: [517, 519], text: "Main moved", tone: "cancelled", url: PR + "522", once: [] },
    { number: 4, riders: [517, 519, 520, 524], text: "Landed", tone: "passed", url: PR + "526", once: [] },
  ]);
  assert.deepEqual(batchRuns(red), [{ number: 1, riders: [531], text: "Failed", tone: "failed", url: check, once: [] }]);
  assert.deepEqual(["", "changed", "repushed", "stopped"].map((result) => batchRuns(train({ history: [run(1, [1], result)] }))[0].text), ["Testing", "A pull request changed", "Pushed again", "Stopped"]);
});

test("a run whose failed checks ran again says so and keeps each check that failed once, opening its first try", () => {
  const story = (result: string) => batchRuns(train({ history: [run(1, [1, 2], result, "", [CHANCE])] }))[0];
  assert.deepEqual(["", "landed", "failed", "moved", "stopped"].map((result) => [story(result).text, story(result).tone]), [
    ["Trying again", "pending"], ["Landed on the second try", "passed"], ["Failed twice", "failed"], ["Main moved", "cancelled"], ["Stopped", "cancelled"],
  ]);
  assert.deepEqual(story("landed").once, [{ name: "go (conpty)", url: CHANCE.link }]);
  assert.deepEqual(batchRuns(train({ history: [run(1, [1], "landed", "", [{ name: "scan", link: "https://evil.example/report" }])] }))[0].once, [{ name: "scan", url: "" }]);
});

test("a train's panel lists every pull request of its batch once, where it stands now", () => {
  const retried = train({ id: "r-2", state: "landed", earlier: [train({ id: "r-1", state: "failed", cars: [car(517, "returned"), car(518, "returned")] })], cars: [car(517, "landed")] });
  assert.deepEqual(batchCars(retried).map((each) => "#" + each.number + "=" + each.state), ["#517=landed", "#518=returned"]);
  assert.deepEqual(batchCars(landed).map((each) => each.number), [517, 519, 520, 524, 523]);
});

test("each pull request on a train says where it stands and keeps the train's reason in its tip", () => {
  assert.deepEqual(carLook(car(7, "culprit", "failed: test (https://github.com/o/r/actions/runs/1)")), { label: "#7", url: PR + "7", text: "Breaks CI", tone: "failed", tip: "failed: test (https://github.com/o/r/actions/runs/1)" });
  assert.deepEqual(["riding", "waiting", "landed", "conflict", "returned"].map((state) => carLook(car(1, state)).text), ["Testing", "Waits its turn", "Landed", "Merging main to fix a conflict", "Rides the next train"]);
  assert.deepEqual(["conflict", "returned"].map((state) => carLook(car(1, state)).tone), ["cancelled", "cancelled"]);
});

test("a link off GitHub is not opened", () => {
  assert.equal(trainLook(train({ pr: "javascript:alert(1)" })).url, "");
  assert.equal(carLook({ ...car(1, "riding"), url: "https://evil.example/pull/1" }).url, "");
});

test("only a testing train is still running", () => {
  assert.deepEqual(["testing", "landed", "stopped", "failed"].map((state) => isTrainOver(train({ state }))), [false, true, true, true]);
});

test("a car goes by its goblin's name and title, and by its pull request when its goblin had none", () => {
  assert.equal(carName({ ...car(55, "riding"), goblin: "Mabel", goblin_title: "Bug Hunter" }), "Mabel - Bug Hunter");
  assert.equal(carName(car(56, "riding")), "change 56");
  assert.equal(carName({ ...car(57, "riding"), title: "" }), "g57");
});
