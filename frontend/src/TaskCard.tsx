import { useRef, useState, type SyntheticEvent } from "react";
import type { BoardActivity, Snapshot, Task } from "./types";
import { Avatar } from "./Avatar";
import { ConnectorMark } from "./ConnectorMark";
import { harnessMark } from "./connectors";
import type { NextUp } from "./start";
import { harnessTip, personaFor, pullRequestIcon } from "./workflow";
import { goblinName, taskName } from "./task-words";
import { taskStatus } from "./task-status";
import { TicketLink } from "./ticket-link";
import { SameAreaAvatars } from "./same-area-avatars";
import { CardClock } from "./card-clock";
import { CardPullRequest } from "./card-pull-request";

// A task's card on the board is one row, like a conversation in a list (the
// Overlord, 2026-10-17: "minimal cards are better like an imessage history
// with name status of whether working time working or last worked tickets pr
// icons etc and harness icon"): its portrait, its name with a quiet clock of
// how long it has run or waited, or when it paused or finished, beside it,
// and under them its status in at most two lines and its project on a quiet
// line of its own (the Overlord, 2026-10-17: "please include project repo
// below status line in board"), with only its ticket and pull request as
// icons that open them, each saying in its tip what it is (the Overlord,
// 2026-10-17: "for now, we just want to know the pull request and the
// issue"), then the mark of the harness it runs, whose tip names the model
// and effort. A goblin leads with its name, a finished one too, which wraps
// rather than ending in an ellipsis, and shows its task in the name's tip
// (the CFO's decision 1008011). Its checks and deploy, the goblin it helps,
// what runs under it and a waiting update are in its panel. rank, when the
// card sits in an ordered list, is read out with it; next marks the one card
// the order for a free slot takes first. The goblin it waits on is named in
// its status line only. A goblin gone quiet, and windows still closing, are
// the CFO's to hear, never warnings on the card.
export function TaskCard({ task, snapshot, selected, presentations, now, rank, next, onSelect }: {
  task: Task; snapshot: Snapshot; selected: boolean; presentations: BoardActivity[]; now: number; rank?: string;
  next?: NextUp;
  onSelect: (task: Task, source: HTMLElement) => void;
}) {
  // A task with no goblin yet goes by its title, which shows in full in its
  // tip while it is shortened: that card carries data-tip only then.
  const [clipped, setClipped] = useState(false);
  const card = useRef<HTMLButtonElement>(null);
  const measure = (event: SyntheticEvent<HTMLElement>) => {
    const title = event.currentTarget.querySelector<HTMLElement>(".card-title");
    setClipped(!!title && title.scrollWidth > title.clientWidth + 1);
  };
  const isNamed = task.goblin_name !== "";
  const name = isNamed ? goblinName(task) : taskName(task);
  const tip = isNamed ? { "data-tip": taskName(task) } : clipped ? { "data-tip": name } : {};
  const icon = pullRequestIcon(task), { text: status, phase } = taskStatus(task, snapshot);
  const content = <>
    <Avatar persona={personaFor(task)} />
    <span className="card-copy"><span className="card-head">{presentations.some((event) => event.task_id === task.id) && <span className="browser-indicator">Browser active</span>}{next && <span className={"next-chip" + (next.tone ? " " + next.tone : "")}>{next.text}</span>}<strong className={"card-title" + (isNamed ? "" : " card-task-title")}>{name}</strong></span>
      {rank && <span className="sr-only">, {rank}</span>}
      <span className={"plain-status phase-" + phase + (task.archived && task.phase !== "stopped" ? " pr-" + icon : "")}><span className="status-dot" /><span className="card-status-text">{status}</span></span>
      {task.project && <span className="card-repo">{task.project}</span>}
    </span>
  </>;
  return <div className={"task-card-shell" + (selected ? " selected" : "")}>
    <button ref={card} className="task-card"
      aria-pressed={selected} onClick={(event) => onSelect(task, event.currentTarget)} onPointerEnter={measure} onFocus={measure} {...tip}>{content}</button>
    <CardClock task={task} now={now} />
    <div className="card-icons">
      {task.ticket && <TicketLink ticket={task.ticket} className="card-ticket" isIconOnly />}
      <CardPullRequest task={task} isIconOnly />
      <SameAreaAvatars overlaps={task.overlaps} />
      {task.harness && <span className="card-harness" onClick={() => onSelect(task, card.current!)}><ConnectorMark mark={harnessMark(task.harness)} label={harnessTip(task.harness, task.model, task.effort)} /></span>}
    </div>
  </div>;
}
