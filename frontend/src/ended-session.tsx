import { Icon } from "./Icon";
import type { Task } from "./types";
import type { sessionEnd } from "./session-end";
import "./ended-session.css";

export function EndedSession({ task, kind }: { task: Task; kind: NonNullable<ReturnType<typeof sessionEnd>> }) {
  const at = kind === "retired" ? task.retired_at || "" : task.at;
  const date = new Date(at);
  const hasTime = Number.isFinite(date.getTime()) && date.getUTCFullYear() > 1;
  const label = kind[0].toUpperCase() + kind.slice(1);
  return <section className="ended-session" aria-label={"Session " + kind}>
    <div className="ended-session-content">
      <span className="ended-session-icon"><Icon name={kind === "retired" ? "terminal" : kind === "paused" ? "pause-circle" : "stop-circle"} /></span>
      <h2>Session {kind}</h2>
      {hasTime && <p className="ended-session-time">{label} <time dateTime={at} title={date.toLocaleString()}>{date.toLocaleDateString(undefined, { month: "short", day: "numeric" })} at {date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false })}</time></p>}
      {task.last_report && <div className="ended-session-report"><p><Icon name="task" />Last report</p><p>{task.last_report}</p></div>}
      {task.handoff && <a className="ended-session-handoff" href={"/api/tasks/" + encodeURIComponent(task.id.replace(/^finished:/, "")) + "/handoff"} target="_blank" rel="noreferrer"><Icon name="file" />Open handoff</a>}
    </div>
  </section>;
}
