import { hostedChecksLook } from "./hosted-checks.ts";
import { carLook, isTrainOver, trainLook, type Tone } from "./merge-train.ts";
import { safeGitHubLink } from "./people.ts";
import { isPausedForItsPullRequest, pauseStatus } from "./task-words.ts";
import type { MergeTrain, Task } from "./types.ts";

// PullRequestTest is where a goblin's pull request stands under test, read
// the same on its card, its panel, its canvas node and its train's card: the
// words, their tone, the page a click opens and the detail in its tip.
export interface PullRequestTest { text: string; tone: Tone; url: string; tip: string }

// pullRequestTest is where a goblin's pull request stands under test. On a
// merge train it reads as the train's card reads it, with the train's link:
// the running train that carries it, else the newest finished one while the
// pull request's head is still the one that rode. Trains come newest first.
// Otherwise its own checks say it, with their run's link.
export function pullRequestTest(task: Task, trains: MergeTrain[]): PullRequestTest | undefined {
  if (!task.pr || task.archived) return undefined;
  const carried = trains.filter((train) => train.cars.some((car) => car.url === task.pr));
  const train = carried.find((candidate) => !isTrainOver(candidate)) ?? carried[0];
  const car = train?.cars.find((candidate) => candidate.url === task.pr);
  const head = task.hosted_checks?.head;
  if (train && car && (!isTrainOver(train) || !head || !car.head || car.head === head)) {
    const look = carLook(car), whole = trainLook(train);
    return { text: look.text, tone: look.tone, url: whole.url, tip: look.tip || whole.text };
  }
  return task.hosted_checks && hostedChecksLook(task.hosted_checks, task.pr);
}

// The phases a goblin that reported its pull request done sits in while it
// only waits on it. At work, blocked, asking, waiting on something else or
// past the test, it says that itself.
const AWAITING = new Set(["idle", "review", "ready", "unavailable", "unknown"]);

// awaitedTest is what a goblin reads in place of its own state on its card,
// its panel and its canvas node while all it waits on is its pull request's
// test: paused until the pull request merges or its CI run on it finishes,
// or live with nothing left to do since it reported the pull request done.
// Paused before its checks were read, it waits on its pull request. A resume
// under way says so itself.
export function awaitedTest(task: Task, trains: MergeTrain[]): PullRequestTest | undefined {
  if (isPausedForItsPullRequest(task)) {
    if (task.phase === "resuming") return undefined;
    return pullRequestTest(task, trains) ?? { text: pauseStatus(task.lifecycle?.pause, [], []), tone: "pending", url: safeGitHubLink(task.pr), tip: "" };
  }
  const isOnlyWaiting = task.report === "done" && AWAITING.has(task.phase) && !(task.phase === "review" && task.gate_step) && !task.comeback;
  return isOnlyWaiting ? pullRequestTest(task, trains) : undefined;
}
