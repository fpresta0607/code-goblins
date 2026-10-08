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

const numbers = (cars: TrainCar[]) => cars.map((car) => "#" + car.number).join(", ");

// trainLook says in plain words where a train stands: what CI tests now and
// in which run, or how it ended.
export function trainLook(train: MergeTrain): TrainLook {
  const url = safeGitHubLink(train.pr);
  const landed = train.cars.filter((car) => car.state === "landed");
  switch (train.state) {
    case "testing": {
      const riding = train.cars.filter((car) => car.state === "riding");
      return { text: `CI tests ${numbers(riding)}` + (train.runs > 1 ? `, run ${train.runs}` : ""), tone: "pending", url };
    }
    case "landed":
      return { text: `Landed ${numbers(landed)}`, tone: "passed", url };
    case "stopped": {
      const culprits = train.cars.filter((car) => car.state === "culprit");
      if (culprits.length > 0) return { text: `${numbers(culprits)} breaks CI` + (landed.length > 0 ? `. Landed ${numbers(landed)}` : ""), tone: "failed", url };
      return { text: landed.length > 0 ? `Landed ${numbers(landed)}` : "Nothing merged cleanly", tone: "cancelled", url };
    }
  }
  return { text: "Train failed" + (landed.length > 0 ? `. Landed ${numbers(landed)}` : ""), tone: "failed", url };
}

const CAR_WORDS: Record<string, [string, Tone]> = {
  riding: ["Testing", "pending"],
  waiting: ["Waits its turn", "pending"],
  landed: ["Landed", "passed"],
  culprit: ["Breaks CI", "failed"],
  conflict: ["Conflicts", "cancelled"],
  returned: ["Next train", "cancelled"],
};

// carLook says where one pull request stands on its train.
export function carLook(car: TrainCar): CarLook {
  const [text, tone] = CAR_WORDS[car.state] ?? [car.state, "pending"];
  return { label: "#" + car.number, url: safeGitHubLink(car.url), text, tone, tip: car.note };
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
