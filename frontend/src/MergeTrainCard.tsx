import type { MergeTrain, Task } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { cardDate } from "./cards";
import { carLook, carName, trainLook, trainSection } from "./merge-train";
import { taskName } from "./task-words";
import { personaFor, stablePersona } from "./workflow";
import "./merge-train.css";

// A merge train's card, one for each batch of pull requests: the repository
// and the branch it lands on, where it stands in its tone, opening the
// train's pull request, with what happened last in its tip, then its pull
// requests in train order, opening on GitHub, with the
// goblin that made it, by its avatar, name and title, its task in the
// goblin's tip, and where it stands and, in its tip, why, unless the train's
// status already says it: a train that tests its riders, landed them or
// names what broke CI says so once (the Overlord, 2026-10-17: "landed status is already above it"). A
// running train shows when it started, a finished one when it finished, as
// its month and day or, for today, its time. A click anywhere on it but a
// link opens its panel. tasks are the board's, which hold a goblin still on
// it.
export function MergeTrainCard({ train, tasks, now, selected, onSelect }: { train: MergeTrain; tasks: Task[]; now: number; selected: boolean; onSelect: (train: MergeTrain, source: HTMLElement) => void }) {
  const look = trainLook(train);
  const said = train.state === "testing" ? "riding" : look.tone === "passed" ? "landed" : train.cars.some((car) => car.state === "culprit") ? "culprit" : "";
  const when = train.finished || train.started;
  const at = new Date(when);
  const status = <><span className="status-dot" />{look.text}</>;
  const tip = train.note ? { "data-tip": train.note } : {};
  return <section className={"train-card" + (selected ? " selected" : "")} aria-label={"Merge train " + train.id}
    onClick={(event) => { if (!(event.target as Element).closest("a")) onSelect(train, event.currentTarget); }}>
    <button type="button" className="train-head" aria-pressed={selected} aria-label={"Open merge train " + train.repository + " into " + train.base}>
      <span className="train-mark"><Icon name="merge" /></span>
      <span className="train-title"><strong>Merge train</strong><span className="card-repo">{train.repository} into {train.base}</span></span>
    </button>
    {look.url
      ? <a className={"train-status train-" + look.tone} href={look.url} target="_blank" rel="noreferrer" aria-label={look.text + ": open the train's pull request"} {...tip}>{status}</a>
      : <span className={"train-status train-" + look.tone} {...tip}>{status}</span>}
    {trainSection(train) === "Train history" && train.note && <p className="train-note">{train.note}</p>}
    {train.cars.length > 0 && <ol className="train-cars" aria-label="Pull requests on the train">
      {train.cars.map((car) => {
        const c = carLook(car), goblin = tasks.find((task) => task.id === car.task);
        return <li key={car.number} className={"train-car train-" + c.tone} {...(c.tip ? { "data-tip": c.tip } : {})}>
          {c.url ? <a className="train-car-number" href={c.url} target="_blank" rel="noreferrer">{c.label}</a> : <span className="train-car-number">{c.label}</span>}
          <span className="train-car-goblin" {...(car.goblin ? { "data-tip": goblin ? taskName(goblin) : car.title } : {})}>
            <Avatar persona={goblin ? personaFor(goblin) : stablePersona(car.task)} />
            <span className={"train-car-title" + (car.goblin ? " goblin-called" : "")}>{carName(car)}</span>
          </span>
          {car.state !== said && <span className="train-car-state">{c.text}</span>}
        </li>;
      })}
    </ol>}
    {Number.isFinite(at.getTime()) && <span className="card-clock"><Icon name="clock" /><span className="sr-only">{train.finished ? "Finished" : "Started"} </span><time dateTime={when}>{cardDate(at, now)}</time></span>}
  </section>;
}
