import { Icon } from "./Icon";
import type { MergeTrain, Task } from "./types";
import type { sessionEnd } from "./session-end";
import "./ended-session.css";
import { reportSaid } from "./task-words";
import { RawDetails } from "./raw-details";
import { awaitedTest } from "./pull-request-test";

// A goblin paused only until its own pull request merges, or until its CI
// run on it finishes, says where that pull request's test stands, as its
// card does, rather than that its session paused. trains are the board's.
export function EndedSession({ task, kind, trains }: { task: Task; kind: NonNullable<ReturnType<typeof sessionEnd>>; trains: MergeTrain[] }) {
  const awaited = kind === "paused" ? awaitedTest(task, trains) : undefined;
  const at = kind === "retired" ? task.retired_at || "" : task.at;
  const date = new Date(at);
  const hasTime = Number.isFinite(date.getTime()) && date.getUTCFullYear() > 1;
  const label = kind[0].toUpperCase() + kind.slice(1);
  const report = reportSaid(task.last_report || "");
  return <section className="ended-session" aria-label={"Session " + kind}>
    <div className="ended-session-content">
      <span className="ended-session-icon"><Icon name={awaited ? "pull-request" : kind === "retired" ? "terminal" : kind === "paused" ? "pause-circle" : "stop-circle"} /></span>
      <h2>{awaited ? awaited.text : "Session " + kind}</h2>
      {hasTime && !awaited && <p className="ended-session-time">{label} <time dateTime={at} data-tip={date.toLocaleString()}>{date.toLocaleDateString(undefined, { month: "short", day: "numeric" })} at {date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false })}</time></p>}
      {report.sentence && <div className="ended-session-report"><p><Icon name="task" />Last report</p><p>{report.sentence}</p><RawDetails lines={report.details} /></div>}
      {task.handoff && <a className="ended-session-handoff" href={"/api/tasks/" + encodeURIComponent(task.id.replace(/^finished:/, "")) + "/handoff"} target="_blank" rel="noreferrer"><Icon name="file" />Open handoff</a>}
    </div>
  </section>;
}
