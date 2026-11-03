import { hostedChecksLook } from "./hosted-checks.ts";
import { carLook, isTrainOver, trainLook, type Tone } from "./merge-train.ts";
import type { MergeTrain, Task } from "./types.ts";

// PullRequestTest is where a goblin's pull request stands under test, read
// the same in its panel, its canvas node and its train's card, and on its
// card while it only waits on it: the words, their tone, the page a click
// opens, the detail in its tip, and whether a merge train says it.
export interface PullRequestTest { text: string; tone: Tone; url: string; tip: string; isOnTrain: boolean }

// pullRequestTest is where a goblin's pull request stands under test. On a
// merge train it reads as the train's card reads it, with the train's link:
// the running train that carries it, else the newest finished one while the
// pull request's head is still the one that rode. Trains come newest first,
// each before the trains it took on. Otherwise its own checks say it, with
// their run's link.
export function pullRequestTest(task: Task, trains: MergeTrain[]): PullRequestTest | undefined {
  if (!task.pr || task.archived) return undefined;
  const carried = trains.flatMap((train) => [train, ...[...train.earlier].reverse()]).filter((train) => train.cars.some((car) => car.url === task.pr));
  const train = carried.find((candidate) => !isTrainOver(candidate)) ?? carried[0];
  const car = train?.cars.find((candidate) => candidate.url === task.pr);
  const head = task.hosted_checks?.head;
  if (train && car && (!isTrainOver(train) || !head || !car.head || car.head === head)) {
    const look = carLook(car), whole = trainLook(train);
    return { text: look.text, tone: look.tone, url: whole.url, tip: look.tip || whole.text, isOnTrain: true };
  }
  return task.hosted_checks && { ...hostedChecksLook(task.hosted_checks, task.pr), isOnTrain: false };
}

// landingStatus is what a goblin whose done report is in reads while its pull
// request lands, given where it stands under test: Landing on a train while
// one carries it, else Landing, waits to merge, and the test's own words once
// it failed, was cancelled or landed.
export function landingStatus(test: PullRequestTest | undefined): string {
  if (test && (test.tone === "failed" || test.tone === "cancelled" || test.isOnTrain && test.tone === "passed")) return test.text;
  return test?.isOnTrain ? "Landing on a train" : "Landing, waits to merge";
}

// The phases a goblin that reported its pull request done sits in while it
// only waits on it. At work, blocked, asking, waiting on something else or
// past the test, it says that itself.
const AWAITING = new Set(["idle", "review", "ready", "unavailable", "unknown"]);

// isAwaitingTest says all a goblin waits on is its pull request's test: live
// with nothing left to do since it reported the pull request done, whether or
// not a test of it is recorded yet. A paused goblin reads its pause, even one
// paused until its pull request merges, and its panel still says where the
// pull request stands (the Overlord, 2026-10-16: "paused does it means youre
// on the bench").
export const isAwaitingTest = (task: Task): boolean =>
  !!task.pr && !task.archived && task.report === "done" && AWAITING.has(task.phase) && !(task.phase === "review" && task.gate_step) && !task.comeback;

// awaitedTest is the test a goblin's card, panel and canvas node draw in
// place of its own state while all it waits on is that test.
export function awaitedTest(task: Task, trains: MergeTrain[]): PullRequestTest | undefined {
  return isAwaitingTest(task) ? pullRequestTest(task, trains) : undefined;
}
