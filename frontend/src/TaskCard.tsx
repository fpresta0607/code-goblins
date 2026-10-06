import { useRef, useState, type SyntheticEvent } from "react";
import type { BoardActivity, Snapshot, Task } from "./types";
import { Avatar } from "./Avatar";
import { ConnectorMark } from "./ConnectorMark";
import { Icon } from "./Icon";
import { clockText, stalledText } from "./cards";
import { harnessMark } from "./connectors";
import type { NextUp } from "./start";
import { TaskControls } from "./task-controls";
import { asksOverlord, harnessTip, nodeStatus, personaFor, pullRequestIcon, pullRequestLabel, safePullRequest, statusPhase, taskColumn } from "./workflow";
import { pauseStatus, plainText, teardownSentence, withoutHarness } from "./task-words";
import { TicketLink } from "./ticket-link";
import { SameAreaAvatars } from "./same-area-avatars";
import { HostedChecksLink } from "./hosted-checks-link";
import { DeploymentLink } from "./deployment-link";

// A task's card on the board: its title, up to three lines, then a muted line
// with the repo and the status, both wrapping onto further lines, and a quiet
// clock of how long its session has run or how long it has waited; the
// goblin's own words stay in its panel. rank, when the card sits in an ordered
// list, is read out with it. Its pull request takes a row of its own under
// that, and its controls sit beside it or, on a narrow card, under it, with the
// mark of the harness it runs in the corner: every part of the card has its own
// place, so none is drawn over another, and a part's tip floats clear of the
// card. The goblin it waits on is named in its status line only. A queued
// task says nothing of what it waits for, whose note is in its panel behind
// More; a paused or finished task shows when, and its status says what: a
// paused one, in place of Paused, why it waits and what resumes it. A live
// goblin past the threshold without progress says for how long. next marks
// the one card the order for a free slot takes first.
export interface CardStart { blocked: string; problem: string; onStart: (source: HTMLElement) => void }
export function TaskCard({ task, snapshot, selected, presentations, now, rank, next, start, onSelect, onTerminal }: {
  task: Task; snapshot: Snapshot; selected: boolean; presentations: BoardActivity[]; now: number; rank?: string;
  next?: NextUp; start?: CardStart;
  onSelect: (task: Task, source: HTMLElement) => void;
  onTerminal: (task: Task, source: HTMLElement) => void;
}) {
  // A title still shortened shows in full in a tip on hover or focus: the
  // card carries data-tip only while its title is shortened.
  const [clipped, setClipped] = useState(false);
  const card = useRef<HTMLButtonElement>(null);
  const measure = (event: SyntheticEvent<HTMLElement>) => {
    const title = event.currentTarget.querySelector<HTMLElement>(".card-title");
    setClipped(!!title && title.scrollHeight > title.clientHeight + 1);
  };
  const name = withoutHarness(task.title) || task.id;
  const tip = clipped ? { "data-tip": name, "data-tip-align": "start" } : {};
  const pr = safePullRequest(task.pr), icon = pullRequestIcon(task), asking = asksOverlord(snapshot, task.id);
  const waiting = task.phase === "queued";
  const column = taskColumn(task);
  const clock = column === "Completed" || column === "Paused" ? "" : clockText(task.since, now, waiting ? "waiting" : "running");
  const clockBadge = clock && <span className="card-clock"><Icon name="clock" /><span className="sr-only">{waiting ? "Waiting for" : "Running for"} </span>{clock}</span>;
  const ended = (column === "Paused" || column === "Completed") && task.at ? new Date(task.at) : null;
  const status = statusPhase(task) === "paused" ? pauseStatus(task.lifecycle?.pause, snapshot.tasks, snapshot.ci_durations) : nodeStatus({ id: task.id, title: task.title, task, relation: "" }, asking);
  const stalled = stalledText(task, now);
  const content = <>
    <Avatar persona={personaFor(task)} />
    <span className="card-copy">{presentations.some((event) => event.task_id === task.id) && <span className="browser-indicator">Browser active</span>}{next && <span className={"next-chip" + (next.tone ? " " + next.tone : "")}>{next.text}</span>}<strong className="card-title">{name}</strong>
      {rank && <span className="sr-only">, {rank}</span>}
      <span className="card-meta">{task.project && <span className="card-repo">{task.project}</span>}<span className={"plain-status phase-" + statusPhase(task) + (task.archived && task.phase !== "stopped" ? " pr-" + icon : "")}><span className="status-dot" /><span className="card-status-text">{status}</span></span></span>
      {clockBadge}
      {stalled && <span className="card-stalled"><Icon name="warning" />{stalled}</span>}
      {task.pending_engine && <span className="card-secondary">{task.pending_engine.when === "resume" ? "Resume with" : "Pending:"} {task.pending_engine.model} {task.pending_engine.effort}</span>}
      {task.switching && <span className="card-secondary">Switching engine...</span>}
      {ended && Number.isFinite(ended.getTime()) && <span className="card-clock"><Icon name="clock" /><time dateTime={task.at}>{ended.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}</time></span>}
      {column === "Completed" && task.phase === "stopped" && task.reason && <span className="card-secondary">{plainText(task.reason)}</span>}
      {task.teardown.length > 0 && <span className="windows-teardown">{teardownSentence(task.teardown)}</span>}
    </span>
  </>;
  const terminal = !!task.generation && column === "In progress" && <button className="icon-button raised card-terminal" aria-label={"Open the terminal of " + name} data-tip="Terminal" data-tip-align="end" onClick={(event) => onTerminal(task, event.currentTarget)}><Icon name="terminal" /></button>;
  return <div className={"task-card-shell" + (selected ? " selected" : "")}>
    <button ref={card} className="task-card"
      aria-pressed={selected} onClick={(event) => onSelect(task, event.currentTarget)} onPointerEnter={measure} onFocus={measure} {...tip}>{content}</button>
    {(pr || column === "Completed" || task.ticket || task.overlaps.length > 0 || task.deployment) && <div className="card-links">
      {pr && <a className={"card-pr pr-" + icon} href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)}><Icon name={icon} />{pullRequestLabel(pr)}</a>}
      {pr && task.hosted_checks && <HostedChecksLink checks={task.hosted_checks} pr={pr} className="card-checks" />}
      {task.deployment && <DeploymentLink deployment={task.deployment} className="card-checks" />}
      {task.ticket && <TicketLink ticket={task.ticket} className="card-ticket" />}
      <SameAreaAvatars overlaps={task.overlaps} />
      {column === "Completed" && <span className="card-secondary card-history-id">{task.branch || task.id.replace(/^finished:/, "")}</span>}
    </div>}
    <TaskControls task={task} snapshot={snapshot} start={start} leading={terminal} onAdjust={(source) => onSelect(task, source)} />
    {task.harness && <span className="card-harness" onClick={() => onSelect(task, card.current!)}><ConnectorMark mark={harnessMark(task.harness)} label={harnessTip(task.harness, task.model, task.effort)} align="end" /></span>}
  </div>;
}
