import { safeGitHubLink } from "./people.ts";
import type { MergeTrain, TrainCar } from "./types";

// Tone is how a train or one of its pull requests reads at a glance, in the
// colours a pull request's checks use.
export type Tone = "pending" | "passed" | "failed" | "cancelled";

// TrainLook is how a merge train shows on its card: the words that say where
// it stands, in its tone, and its pull request's page.
export interface TrainLook { text: string; tone: Tone; url: string }

// CarLook is how one pull request on a train shows: its number, its page,
// where it stands on the train, and why, when the train said why.
export interface CarLook { label: string; url: string; text: string; tone: Tone; tip: string }

// RunLook is how one CI run of a train shows in its panel: its number among
// the runs of its batch, the pull requests it tested, how it ended in its
// tone, and the page that shows it, its first failed check's when it was red
// and else its train's pull request. once are the checks that were red on
// its first try, when its failed checks ran again, each with that try's page.
export interface RunLook { number: number; riders: number[]; text: string; tone: Tone; url: string; once: CheckLook[] }

// CheckLook is how a check that failed shows: its name and its page.
export interface CheckLook { name: string; url: string }

const numbers = (cars: TrainCar[]) => cars.map((car) => "#" + car.number).join(", ");
const capital = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

// The runs of a train's batch: its own and those of the trains it took on.
const batchRunCount = (train: MergeTrain) => train.earlier.reduce((count, earlier) => count + earlier.runs, train.runs);

// trainLook says in plain words where a train stands: what CI tests now and
// in which run of its batch, or how it ended. A train that landed anything
// says so, since its card lists only what it landed; one that landed nothing
// says why.
export function trainLook(train: MergeTrain): TrainLook {
  const url = safeGitHubLink(train.pr);
  const runs = batchRunCount(train);
  const onRun = runs > 1 ? " on run " + runs : "";
  if (train.state === "testing") {
    const riding = train.cars.filter((car) => car.state === "riding");
    const open = train.history.at(-1);
    const again = open?.result === "" && open.failed_once.length > 0 ? " again" : "";
    return { text: `CI tests ${numbers(riding)}${again}` + (runs > 1 ? `, run ${runs}` : ""), tone: "pending", url };
  }
  if (train.cars.some((car) => car.state === "landed")) return { text: "Landed" + onRun, tone: "passed", url };
  const culprits = train.cars.filter((car) => car.state === "culprit");
  if (culprits.length > 0) return { text: `${numbers(culprits)} breaks CI`, tone: "failed", url };
  if (train.state === "stopped") return { text: "Nothing merged cleanly", tone: "cancelled", url };
  const why: Record<string, string> = { moved: train.base + " kept moving", failed: train.base + " is red" };
  const last = train.history.at(-1)?.result ?? "";
  return { text: "Train failed" + (why[last] ? ", " + why[last] : ""), tone: "failed", url };
}

// What a pull request on a train says, and in what tone: one the train could
// not merge or left for the next one says what happens to it next.
const CAR_WORDS: Record<string, [string, Tone]> = {
  riding: ["Testing", "pending"],
  waiting: ["Waits its turn", "pending"],
  landed: ["Landed", "passed"],
  culprit: ["Breaks CI", "failed"],
  conflict: ["Merging main to fix a conflict", "cancelled"],
  returned: ["Rides the next train", "cancelled"],
};

// carLook says where one pull request stands on its train.
export function carLook(car: TrainCar): CarLook {
  const [text, tone] = CAR_WORDS[car.state] ?? [car.state, "pending"];
  return { label: "#" + car.number, url: safeGitHubLink(car.url), text, tone, tip: car.note };
}

// The pull requests a train's card lists: what it tests, what waits its turn
// and what it landed. One it could not merge, left for the next train or
// found broken shows on its goblin's card instead.
const ON_CARD = new Set(["riding", "waiting", "landed"]);

// cardCars are the pull requests a train's card lists, in train order.
export function cardCars(train: MergeTrain): TrainCar[] {
  return train.cars.filter((car) => ON_CARD.has(car.state));
}

// batchCars are every pull request of a train's batch, each once, where it
// stands now: the train's own in train order, then those only the trains it
// took on carried.
export function batchCars(train: MergeTrain): TrainCar[] {
  const cars = [...train.cars];
  for (const earlier of [...train.earlier].reverse()) {
    for (const car of earlier.cars) if (!cars.some((other) => other.url === car.url)) cars.push(car);
  }
  return cars;
}

// How a run ended, in its panel. A run CI still tests has no result.
const RUN_WORDS: Record<string, [string, Tone]> = {
  "": ["Testing", "pending"],
  landed: ["Landed", "passed"],
  failed: ["Failed", "failed"],
  changed: ["A pull request changed", "cancelled"],
  repushed: ["Pushed again", "cancelled"],
  stopped: ["Stopped", "cancelled"],
};

// How a run whose failed checks ran again stands or ended: a check can fail
// by chance, so a run is red only once it was red twice.
const SECOND_TRY_WORDS: Record<string, [string, Tone]> = {
  "": ["Trying again", "pending"],
  landed: ["Landed on the second try", "passed"],
  failed: ["Failed twice", "failed"],
};

// batchRuns are the CI runs of a train's batch, oldest first, numbered on
// from the trains it took on.
export function batchRuns(train: MergeTrain): RunLook[] {
  let before = 0;
  return [...train.earlier, train].flatMap((each) => {
    const offset = before;
    before += each.runs;
    return each.history.map((run) => {
      const words: [string, Tone] = run.failed_once.length > 0 && SECOND_TRY_WORDS[run.result] || RUN_WORDS[run.result] || [run.result, "cancelled"];
      const [text, tone] = run.result === "moved" ? [capital(each.base) + " moved", "cancelled" as const] : words;
      return {
        number: offset + run.number, riders: run.riders, text, tone, url: run.result === "failed" && safeGitHubLink(run.link) || safeGitHubLink(each.pr),
        once: run.failed_once.map((check) => ({ name: check.name, url: safeGitHubLink(check.link) })),
      };
    });
  });
}

// carName is how a car goes on its train: by the name and title of the
// goblin that reported it done, else by its pull request's title.
export function carName(car: TrainCar): string {
  return car.goblin ? [car.goblin, car.goblin_title].filter(Boolean).join(" - ") : car.title || car.task;
}

// isTrainOver says whether a train has finished, so its card belongs among
// the completed work.
export function isTrainOver(train: MergeTrain): boolean {
  return train.state !== "testing";
}
