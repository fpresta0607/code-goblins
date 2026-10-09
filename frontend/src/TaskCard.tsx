import { useRef, useState, type SyntheticEvent } from "react";
import type { BoardActivity, Snapshot, Task } from "./types";
import { Avatar } from "./Avatar";
import { ConnectorMark } from "./ConnectorMark";
import { Icon } from "./Icon";
import { clockText } from "./cards";
import { harnessMark } from "./connectors";
import type { NextUp } from "./start";
import { TaskControls } from "./task-controls";
import { harnessName, harnessTip, personaFor, pullRequestIcon, pullRequestLabel, safePullRequest, taskColumn } from "./workflow";
import { goblinName, plainText, taskName } from "./task-words";
import { taskStatus } from "./task-status";
import { TicketLink } from "./ticket-link";
import { SameAreaAvatars } from "./same-area-avatars";
import { PullRequestTestLink } from "./pull-request-test-link";
import { pullRequestTest } from "./pull-request-test";
import { DeploymentLink } from "./deployment-link";
import { LocalChecksLink } from "./local-checks-link";
import { BabyGoblin } from "./BabyGoblin";
import { formatMemory, summarize } from "./fleet-tree";

// A task's card on the board: its portrait beside its title, up to three
// lines, then across the card's whole width one muted line with the repo, the
// status and a quiet clock of how long its session has run or how long it has
// waited, or when it paused or finished, wrapping onto further lines only
// when it must; the goblin's own words stay in its panel, and so does what
// runs under it, a silent child included. rank,
// when the card sits in an ordered list, is read out with it. Its pull request
// takes a row of its own under that, and its controls sit beside it or, on a
// narrow card, in one row under it, with the mark of the harness it runs at
// that row's end: every part of the card has its own place, so none is drawn
// over another, and a part's tip floats clear of the card. The goblin it waits
// on is named in its status line only. A queued
// task that waits says what for in its status, in place of Queued, and the
// CFO's note on it is in its panel behind More; a paused or finished task shows when, and its status says what: a
// paused one, in place of Paused, why it waits and what resumes it. A goblin
// that waits only on its pull request's test, live or paused until it merges,
// says where that test stands, the same as its merge train's card, and its
// pull request's chip opens the same page. A goblin
// gone quiet, and windows still closing, are the CFO's to hear, never warnings
// on the card. next marks the one card the order for a free slot takes first.
export interface CardStart { blocked: string; onStart: () => void }
export function TaskCard({ task, snapshot, selected, presentations, now, rank, next, start, onSelect, onCount }: {
  task: Task; snapshot: Snapshot; selected: boolean; presentations: BoardActivity[]; now: number; rank?: string;
  next?: NextUp; start?: CardStart;
  onSelect: (task: Task, source: HTMLElement) => void;
  // onCount shows what runs under the goblin, on the Orchestration canvas;
  // a card without it shows no count.
  onCount?: (task: Task) => void;
}) {
  // A live goblin goes by its name and shows its task in a tip on hover or
  // focus, and a completed card leads with what it delivered. Any other
  // title still shortened shows in full in its tip: that card carries
  // data-tip only while its title is shortened.
  const [clipped, setClipped] = useState(false);
  const card = useRef<HTMLButtonElement>(null);
  const measure = (event: SyntheticEvent<HTMLElement>) => {
    const title = event.currentTarget.querySelector<HTMLElement>(".card-title");
    setClipped(!!title && title.scrollHeight > title.clientHeight + 1);
  };
  const column = taskColumn(task);
  const isNamed = task.goblin_name !== "" && column !== "Completed";
  const name = isNamed ? goblinName(task) : taskName(task);
  const tip = isNamed ? { "data-tip": taskName(task), "data-tip-align": "start" } : clipped ? { "data-tip": name, "data-tip-align": "start" } : {};
  const pr = safePullRequest(task.pr), icon = pullRequestIcon(task);
  const test = pullRequestTest(task, snapshot.merge_trains ?? []), { text: status, phase } = taskStatus(task, snapshot);
  const waiting = task.phase === "queued";
  // A queued task the supervisor is starting has waited, and has not run yet.
  const clock = column === "Completed" || column === "Paused" || task.phase === "paused" || waiting && column !== "Tasks" ? "" : clockText(task.since, now, waiting ? "waiting" : "running");
  const clockBadge = clock && <span className="card-clock"><Icon name="clock" /><span className="sr-only">{waiting ? "Waiting for" : "Running for"} </span>{clock}</span>;
  const ended = (column === "Paused" || column === "Completed") && task.at ? new Date(task.at) : null;
  const parent = task.parent && snapshot.tasks.find((other) => other.id === task.parent);
  const parentName = parent ? goblinName(parent) : task.parent;
  // What runs under a live goblin, as baby goblins with a count each, which
  // opens the Orchestration canvas on the goblin: its baby goblins live
  // there, never as cards of their own.
  const kinds = column === "In progress" ? summarize(task.tree).kinds : [];
  const count = kinds.length > 0 && onCount && <button type="button" className="card-tree" aria-label={"Show what runs under " + name + " on the canvas"} data-tip="Show on the canvas" data-tip-align="start" onClick={() => onCount(task)}>
    {kinds.map(([baby, number]) => <span key={baby} className="tree-tally"><BabyGoblin baby={baby} small hasTip={false} />{number}</span>)}{task.tree && formatMemory(task.tree.memory)}
  </button>;
  const content = <>
    <Avatar persona={personaFor(task)} />
    <span className="card-copy"><span className="card-head">{presentations.some((event) => event.task_id === task.id) && <span className="browser-indicator">Browser active</span>}{next && <span className={"next-chip" + (next.tone ? " " + next.tone : "")}>{next.text}</span>}<strong className="card-title">{name}</strong></span>
      {rank && <span className="sr-only">, {rank}</span>}
      <span className="card-meta">{task.project && <span className="card-repo">{task.project}</span>}<span className={"plain-status phase-" + phase + (task.archived && task.phase !== "stopped" ? " pr-" + icon : "")}><span className="status-dot" /><span className="card-status-text">{status}</span></span>{clockBadge}{ended && Number.isFinite(ended.getTime()) && <span className="card-clock"><Icon name="clock" /><time dateTime={task.at}>{ended.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}</time></span>}</span>
      {parentName && <span className="card-secondary">Helper of {parentName}</span>}
      {task.pending_engine?.when === "update"
        ? <span className="card-secondary">{task.switching ? "Updating " + harnessName(task.harness) + "..." : "Updates " + harnessName(task.harness) + " at its next stopping point"}</span>
        : task.pending_engine && <span className="card-secondary">{task.pending_engine.when === "resume" ? "Resume with" : "Pending:"} {task.pending_engine.model} {task.pending_engine.effort}</span>}
      {task.switching && task.pending_engine?.when !== "update" && <span className="card-secondary">Switching engine...</span>}
      {column === "Completed" && task.phase === "stopped" && task.reason && <span className="card-secondary">{plainText(task.reason)}</span>}
    </span>
  </>;
  return <div className={"task-card-shell" + (selected ? " selected" : "")}>
    <button ref={card} className="task-card"
      aria-pressed={selected} onClick={(event) => onSelect(task, event.currentTarget)} onPointerEnter={measure} onFocus={measure} {...tip}>{content}</button>
    {(count || pr || column === "Completed" || task.ticket || task.overlaps.length > 0 || task.local_checks || task.deployment) && <div className="card-links">
      {count}
      {pr && <a className={"card-pr pr-" + icon} href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)} {...column === "Completed" && task.branch ? { "data-tip": task.branch, "data-tip-align": "start" } : {}}><Icon name={icon} />{pullRequestLabel(pr)}</a>}
      {task.local_checks && <LocalChecksLink checks={task.local_checks} taskId={task.id} className="card-checks" />}
      {pr && test && <PullRequestTestLink test={test} approved={!!task.hosted_checks?.approved} className="card-checks" />}
      {task.deployment && <DeploymentLink deployment={task.deployment} className="card-checks" />}
      {task.ticket && <TicketLink ticket={task.ticket} className="card-ticket" />}
      <SameAreaAvatars overlaps={task.overlaps} />
      {column === "Completed" && !pr && <span className="card-secondary card-history-id">{task.branch || task.id.replace(/^finished:/, "")}</span>}
    </div>}
    <TaskControls task={task} snapshot={snapshot} start={start} onAdjust={(source) => onSelect(task, source)} />
    {task.harness && <span className="card-harness" onClick={() => onSelect(task, card.current!)}><ConnectorMark mark={harnessMark(task.harness)} label={harnessTip(task.harness, task.model, task.effort)} align="end" /></span>}
  </div>;
}
