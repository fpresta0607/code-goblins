import type { MergeTrain, Task } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { carLook, carName, trainLook } from "./merge-train";
import { taskName } from "./task-words";
import { personaFor, stablePersona } from "./workflow";
import "./merge-train.css";

// A merge train's card, one for the whole train: the repository and the
// branch it lands on, where it stands in its tone, opening the train's pull
// request, what happened last, then each pull request on it in train order,
// opening on GitHub, with the goblin that made it, by its avatar, name and
// title, its task in the goblin's tip, and where it stands and, in its tip,
// why. A running train shows when it started, a finished one when it
// finished. tasks are the board's, which hold a goblin still on it.
export function MergeTrainCard({ train, tasks }: { train: MergeTrain; tasks: Task[] }) {
  const look = trainLook(train);
  const when = train.finished || train.started;
  const at = new Date(when);
  const status = <><span className="status-dot" />{look.text}</>;
  return <section className="train-card" aria-label={"Merge train " + train.id}>
    <header className="train-head">
      <span className="train-mark"><Icon name="merge" /></span>
      <span className="train-title"><strong>Merge train</strong><span className="card-repo">{train.repository} into {train.base}</span></span>
    </header>
    {look.url
      ? <a className={"train-status train-" + look.tone} href={look.url} target="_blank" rel="noreferrer" aria-label={look.text + ": open the train's pull request"}>{status}</a>
      : <span className={"train-status train-" + look.tone}>{status}</span>}
    {train.note && <p className="train-note">{train.note}</p>}
    <ol className="train-cars" aria-label="Pull requests on the train">
      {train.cars.map((car) => {
        const c = carLook(car), goblin = tasks.find((task) => task.id === car.task);
        return <li key={car.number} className={"train-car train-" + c.tone} {...(c.tip ? { "data-tip": c.tip, "data-tip-align": "start" } : {})}>
          {c.url ? <a className="train-car-number" href={c.url} target="_blank" rel="noreferrer">{c.label}</a> : <span className="train-car-number">{c.label}</span>}
          <span className="train-car-goblin" {...(car.goblin ? { "data-tip": goblin ? taskName(goblin) : car.title, "data-tip-align": "start" } : {})}>
            <Avatar persona={goblin ? personaFor(goblin) : stablePersona(car.task)} />
            <span className={"train-car-title" + (car.goblin ? " goblin-called" : "")}>{carName(car)}</span>
          </span>
          <span className="train-car-state">{c.text}</span>
        </li>;
      })}
    </ol>
    {Number.isFinite(at.getTime()) && <span className="card-clock"><Icon name="clock" /><span className="sr-only">{train.finished ? "Finished" : "Started"} </span><time dateTime={when}>{at.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}</time></span>}
  </section>;
}
