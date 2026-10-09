import type { ReactNode } from "react";
import type { MergeTrain, Task } from "./types";
import { Avatar } from "./Avatar";
import { BRAND_MARKS } from "./brandMarks";
import { Icon } from "./Icon";
import { batchCars, batchRuns, carLook, carName, trainLook } from "./merge-train";
import { safeGitHubLink } from "./people";
import { PanelRow, type PanelControl } from "./panel-row";
import { taskName } from "./task-words";
import { personaFor, stablePersona } from "./workflow";
import "./train-panel.css";

const at = (when: string, parts: Intl.DateTimeFormatOptions) => new Date(when).toLocaleString(undefined, parts);
const DAY_AND_TIME: Intl.DateTimeFormatOptions = { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" };

// A merge train's panel, opened from its card: the train and where it
// stands, its pull request one tap away, then every pull request of its
// batch with the goblin that made it and where it stands, each row opening
// the pull request, and every CI run of the batch with the pull requests it
// tested, each opening on GitHub, and how it ended, opening that run. The
// trains it took on are part of it, so their runs come first. tasks are the
// board's, which hold a goblin still on it. row is the panel's top row (see
// PanelRow).
export function TrainPanel({ train, tasks, row }: { train: MergeTrain; tasks: Task[]; row: { controls: PanelControl[]; corner: ReactNode; notice?: ReactNode } }) {
  const look = trainLook(train), cars = batchCars(train), runs = batchRuns(train);
  const started = train.earlier[0]?.started || train.started;
  const link = (number: number) => safeGitHubLink(`https://github.com/${train.repository}/pull/${number}`);
  const day = (when: string) => new Date(when).toDateString();
  return <section className="goblin-panel train-panel" aria-labelledby="panel-title">
    <PanelRow {...row} />
    <header className="panel-header">
      <span className="train-mark train-panel-mark"><Icon name="merge" /></span>
      <div className="panel-identity">
        <h2 id="panel-title">Merge train</h2>
        <p className="project-label">{train.repository} into {train.base}</p>
        <p className={"panel-status train-status train-" + look.tone} {...(train.note ? { "data-tip": train.note } : {})}><span className="status-dot" />{look.text}</p>
      </div>
      {look.url && <div className="panel-actions">
        <a className="icon-button raised pill-link" href={look.url} target="_blank" rel="noreferrer" aria-label="Open the train's pull request" data-tip="Open the train's pull request"><svg className="icon brand-glyph" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={BRAND_MARKS.github.path} /></svg><span>#{look.url.split("/").at(-1)}</span></a>
      </div>}
    </header>
    <div className="panel-task">
      <div className="panel-content train-panel-content">
        <section className="train-panel-section" aria-label="Pull requests">
          <h3>Pull requests<span className="column-count">{cars.length}</span></h3>
          <ol className="train-panel-rows">
            {cars.map((car) => {
              const c = carLook(car), goblin = tasks.find((task) => task.id === car.task);
              const content = <>
                <span className="train-panel-number">{c.label}</span>
                <Avatar persona={goblin ? personaFor(goblin) : stablePersona(car.task)} />
                <span className="train-panel-name">{carName(car)}</span>
                <span className={"train-panel-state train-" + c.tone}>{c.text}</span>
              </>;
              const tip = goblin ? { "data-tip": taskName(goblin) } : {};
              return <li key={car.url}>{c.url ? <a className="train-panel-row" href={c.url} target="_blank" rel="noreferrer" {...tip}>{content}</a> : <span className="train-panel-row" {...tip}>{content}</span>}</li>;
            })}
          </ol>
        </section>
        {runs.length > 0 && <section className="train-panel-section" aria-label="Runs">
          <h3>Runs<span className="column-count">{runs.length}</span></h3>
          <ol className="train-panel-rows">
            {runs.map((run) => <li key={run.number} className="train-panel-row train-panel-run">
              <span className="train-panel-run-number">Run {run.number}</span>
              <span className="train-panel-riders">{run.riders.map((number) => {
                const url = link(number);
                return url ? <a key={number} className="train-panel-rider" href={url} target="_blank" rel="noreferrer">#{number}</a> : <span key={number} className="train-panel-rider">#{number}</span>;
              })}</span>
              <span className={"train-panel-state train-" + run.tone}>{run.text}</span>
              {run.url && <a className="icon-button train-panel-open" href={run.url} target="_blank" rel="noreferrer" aria-label={"Open run " + run.number} data-tip={"Open run " + run.number}><Icon name="external" /></a>}
            </li>)}
          </ol>
        </section>}
        {Number.isFinite(new Date(started).getTime()) && <p className="card-clock train-panel-clock"><Icon name="clock" />
          <time dateTime={started}>{at(started, DAY_AND_TIME)}</time>
          {train.finished && <> to <time dateTime={train.finished}>{at(train.finished, day(train.finished) === day(started) ? { hour: "numeric", minute: "2-digit" } : DAY_AND_TIME)}</time></>}
        </p>}
      </div>
    </div>
  </section>;
}
