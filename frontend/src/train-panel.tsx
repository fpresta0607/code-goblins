import type { ReactNode } from "react";
import type { MergeTrain, Task } from "./types";
import { Avatar } from "./Avatar";
import { BRAND_MARKS } from "./brandMarks";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { batchCars, batchRuns, carLook, carName, trainLook } from "./merge-train";
import { safeGitHubLink } from "./people";
import { PanelRow, type PanelControl } from "./panel-row";
import { taskName } from "./task-words";
import { personaFor, stablePersona } from "./workflow";
import "./train-panel.css";

const at = (when: string, parts: Intl.DateTimeFormatOptions) => new Date(when).toLocaleString(undefined, parts);
const DAY_AND_TIME: Intl.DateTimeFormatOptions = { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" };

// Each pull request has one primary row. Attempt history keeps the actual
// rider groups and earlier trains together, including checks that failed once.
export function TrainPanel({ train, tasks, row }: { train: MergeTrain; tasks: Task[]; row: { controls: PanelControl[]; corner: ReactNode; notice?: ReactNode } }) {
  const look = trainLook(train), cars = batchCars(train), runs = batchRuns(train);
  const trainURL = safeGitHubLink(train.pr);
  const status = <><span className="status-dot" />{look.text}</>;
  let runOffset = 0;
  const attempts = [...train.earlier, train].map((record) => {
    const attemptRuns = runs.slice(runOffset, runOffset + record.history.length);
    runOffset += record.history.length;
    return { record, runs: attemptRuns };
  });
  const hasHistory = train.earlier.length > 0 || runs.length > 1 || runs.some((run) => run.once.length > 0 || run.riders.length > 1 || run.tone === "failed" || run.tone === "cancelled");
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
        {train.state === "testing" && train.deploy_hold && look.url
          ? <a className={"panel-status train-status train-" + look.tone} href={look.url} target="_blank" rel="noreferrer">{status}</a>
          : <p className={"panel-status train-status train-" + look.tone}>{status}</p>}
      </div>
      {trainURL && <div className="panel-actions">
        <a className="icon-button raised pill-link" href={trainURL} target="_blank" rel="noreferrer" aria-label="Open the train's pull request"><svg className="icon brand-glyph" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={BRAND_MARKS.github.path} /></svg><span>#{trainURL.split("/").at(-1)}</span></a>
      </div>}
    </header>
    <div className="panel-task">
      <div className="panel-content train-panel-content">
        {train.note && <p className="train-panel-note">{train.note}</p>}
        <section className="train-panel-section" aria-label="Pull requests">
          <h3>Pull requests<span className="column-count">{cars.length}</span></h3>
          <ol className="train-panel-rows">
            {cars.map((car) => {
              const c = carLook(car), goblin = tasks.find((task) => task.id === car.task);
              const latest = runs.filter((run) => run.riders.includes(car.number)).at(-1);
              const current = latest && (car.state === "riding" || car.state === "landed" && latest.tone === "passed" || car.state === "culprit" && latest.tone === "failed") ? latest : undefined;
              const state = current ?? c;
              const content = <>
                <span className="train-panel-number">{c.label}</span>
                <Avatar persona={goblin ? personaFor(goblin) : stablePersona(car.task)} />
                <span className="train-panel-name">{carName(car)}</span>
              </>;
              const stateClass = "train-panel-state train-" + state.tone;
              return <li key={car.url} className="train-panel-row train-panel-car">
                {c.url ? <a className="train-panel-pull" href={c.url} target="_blank" rel="noreferrer">{content}</a> : <span className="train-panel-pull">{content}</span>}
                {current?.url ? <a className={stateClass} href={current.url} target="_blank" rel="noreferrer">{state.text}</a> : <span className={stateClass}>{state.text}</span>}
                {goblin && <p className="train-panel-task">{taskName(goblin)}</p>}
                {c.tip && <p className="train-panel-car-note">{c.tip}</p>}
              </li>;
            })}
          </ol>
        </section>
        {hasHistory && <Disclosure key={train.id} title="Attempt history">
          {attempts.map(({ record, runs: attemptRuns }, index) => <section key={record.id} className="train-panel-attempt">
            {train.earlier.length > 0 && <h4>{index < train.earlier.length ? `Earlier train ${index + 1}` : "Current train"}: <span className={"train-" + trainLook(record).tone}>{trainLook(record).text}</span></h4>}
            {index < train.earlier.length && <>
              {record.note && <p className="train-panel-attempt-note">{record.note}</p>}
              {record.cars.filter((car) => car.note).map((car) => <p key={car.url} className="train-panel-attempt-note">#{car.number}: {car.note}</p>)}
            </>}
            {attemptRuns.length === 0 && <p className="train-panel-attempt-note">No CI run</p>}
            <ol className="train-panel-rows">
              {attemptRuns.map((run) => <li key={run.number} className="train-panel-row train-panel-run">
                <span className="train-panel-run-number">Run {run.number}</span>
                <span className="train-panel-riders">{run.riders.map((number) => {
                  const url = link(number);
                  return url ? <a key={number} className="train-panel-rider" href={url} target="_blank" rel="noreferrer">#{number}</a> : <span key={number} className="train-panel-rider">#{number}</span>;
                })}</span>
                <span className={"train-panel-state train-" + run.tone}>{run.text}</span>
                {run.url && <a className="icon-button train-panel-open" href={run.url} target="_blank" rel="noreferrer" aria-label={"Open run " + run.number}><Icon name="external" /></a>}
                {run.once.length > 0 && <span className="train-panel-once">
                  <span className="train-panel-once-label">Failed once</span>
                  {run.once.map((check) => check.url ? <a key={check.name} className="train-panel-rider train-panel-check" href={check.url} target="_blank" rel="noreferrer">{check.name}</a> : <span key={check.name} className="train-panel-rider train-panel-check">{check.name}</span>)}
                </span>}
              </li>)}
            </ol>
          </section>)}
        </Disclosure>}
        {Number.isFinite(new Date(started).getTime()) && <p className="card-clock train-panel-clock"><Icon name="clock" />
          <time dateTime={started}>{at(started, DAY_AND_TIME)}</time>
          {train.finished && <> to <time dateTime={train.finished}>{at(train.finished, day(train.finished) === day(started) ? { hour: "numeric", minute: "2-digit" } : DAY_AND_TIME)}</time></>}
        </p>}
      </div>
    </div>
  </section>;
}
