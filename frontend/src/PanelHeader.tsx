import { useState } from "react";
import { message, request } from "./api";
import { type Session, type Snapshot, type Task } from "./types";
import { Avatar } from "./Avatar";
import { BRAND_MARKS } from "./brandMarks";
import { Icon } from "./Icon";
import { ownsTaskSession, sessionTitle } from "./lineageTree";
import { harnessName, nodeStatus, personaFor, pullRequestBadge, pullRequestLabel, safePullRequest, waitingTarget } from "./workflow";
import { reviewLine, waitingItems, type Item } from "./commandQueue";
import { credentialAsk } from "./credentials";
import { plainMessage } from "./messageText";
import { AfkToggle } from "./afk-toggle";
import { CfoUpdate } from "./cfo-update";
import { goblinName, taskName, taskSummary, withoutHarness } from "./task-words";
import { RawDetails } from "./raw-details";
import { taskStatus } from "./task-status";
import { PeopleRow } from "./people-row";
import { TicketLink } from "./ticket-link";
import { PullRequestTestLink } from "./pull-request-test-link";
import { pullRequestTest } from "./pull-request-test";
import { DeploymentLink } from "./deployment-link";
import { LocalChecksLink } from "./local-checks-link";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { TaskAdjustment } from "./task-adjustment";

// Who the goblin is, what it is doing now and what the Overlord can do about
// it. Its status is the one place the panel says the task's state, with one
// plain sentence under it and the words that sentence leaves out behind
// Details; a failure links to the task's log. The CFO drawn without a task has
// no worktree to open; its header carries the AFK toggle beside its status,
// with Update before it while an update of the CFO's harness waits, and under
// them what a press of Update waits for or why it did not restart the CFO.
export function PanelHeader({ task, node, snapshot, compact, onAnswer, onOpenTask, onOpenLog }: { task?: Task; node?: Session; snapshot: Snapshot; compact: boolean; onAnswer: (key: string) => void; onOpenTask: (task: Task) => void; onOpenLog: () => void }) {
  const [opening, setOpening] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const cfo = !task && !node;
  const owner = !!task && ownsTaskSession(node, task);
  const cfoSession = snapshot.sessions.find((session) => session.role === "cfo");
  const title = cfo ? "CFO" : node ? sessionTitle(node, task) : task ? goblinName(task) : "";
  const trains = snapshot.merge_trains ?? [];
  // A goblin's own panel reads its task's one status, as its card does; the
  // CFO and a child session read their sessions'.
  const { text: status, phase } = owner ? taskStatus(task, snapshot)
    : cfo ? { text: nodeStatus({ id: "cfo", title, session: cfoSession, relation: "", status: snapshot.registration ? "Registration stale" : cfoSession ? undefined : "Supervising" }), phase: snapshot.registration ? "stale" : cfoSession?.runtime?.state || cfoSession?.phase || "working" }
      : { text: nodeStatus({ id: title, title, task, session: node, relation: "" }, false, snapshot.tasks, trains), phase: node?.runtime?.state || node?.phase || "" };
  const said = owner ? taskSummary(task, snapshot.tasks, status) : undefined;
  // What a queued task's wait line leaves out: the CFO's note on it, or what
  // says it already finished.
  const note = owner && task.phase === "queued" && (task.finished || task.dependencies.length) ? withoutHarness(task.finished || task.reason) : "";
  const pr = owner ? safePullRequest(task.pr) : "";
  const test = owner ? pullRequestTest(task, trains) : undefined;
  const badge = pullRequestBadge(pr);
  const awaited = owner ? waitingTarget(snapshot, task) : undefined;
  // What this goblin waits on the Overlord for: a question, a review item or
  // a credential request.
  // Runs are the CFO's own and never wait on a goblin's panel.
  const waiting = owner ? waitingItems(snapshot).find((item): item is Exclude<Item, { kind: "run" }> => item.kind !== "run" && (item.kind === "question" ? item.question.task : item.kind === "credential" ? item.request.task : item.review.task) === task.id) : undefined;
  const open = async (target: "vscode" | "folder") => {
    if (!task || opening) return;
    setOpening(true);
    showFeedback("");
    try {
      await request("/api/workspace/open", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id, generation: task.generation, target }) });
    } catch (error: unknown) { showFeedback(message(error)); }
    finally { setOpening(false); }
  };
  return <header className={"panel-header" + (compact ? " compact" : "")}>
    <Avatar persona={cfo ? "cfo" : personaFor(task, node)} small />
    <div className="panel-identity">
      <h2 id="panel-title">{title}</h2>
      {owner && task.phase === "queued" ? <TaskAdjustment key={task.id} task={task} snapshot={snapshot} /> : owner && task.goblin_name && <p className="panel-goblin-task">{taskName(task)}</p>}
      {!compact && task?.project && <div className="project-line"><p className="project-label">{task.project}</p><PeopleRow snapshot={snapshot} task={task} /></div>}
      <p className={"panel-status plain-status phase-" + phase}><span className="status-dot" />{status}{awaited && <button className="status-link" aria-label={"Open " + goblinName(awaited) + ", which this goblin is waiting on"} data-tip={"Open " + goblinName(awaited)} onClick={() => onOpenTask(awaited)}><Icon name="next" /></button>}</p>
      {!compact && !owner && node && task && <p className="muted">Part of {goblinName(task)}</p>}
      {!compact && said?.sentence && <p className="panel-activity">{said.sentence}</p>}
      {!compact && said && (said.details.length > 0 || said.isFailure || note) && <div className="panel-details-row">
        <RawDetails lines={said.details} />
        <RawDetails lines={note ? [note] : []} label="More" />
        {owner && said.isFailure && !task.archived && <button className="text-button" onClick={onOpenLog}>Open the log</button>}
      </div>}
    </div>
    {cfo && <AfkToggle afk={snapshot.afk} instance={snapshot.instance} leading={<CfoUpdate snapshot={snapshot} />} />}
    {cfo && snapshot.cfo_update?.pending && <p className="cfo-update-line" role="status"><Icon name="clock" />Restarts onto the {harnessName(snapshot.cfo_update.harness)} update when its turn ends.</p>}
    {owner && !!task.generation && <div className="panel-actions">
      <button className="icon-button raised" disabled={opening || !snapshot.instance} aria-label="Open in VS Code" data-tip="Open in VS Code" data-tip-align="start" onClick={() => void open("vscode")}><img className="brand-icon" src="/assets/vscode.svg" alt="" /></button>
      <button className="icon-button raised" disabled={opening || !snapshot.instance} aria-label="Open folder" data-tip="Open folder" onClick={() => void open("folder")}><Icon name="folder" /></button>
      {waiting && <button className="icon-button raised pill-link answer" aria-label={"Answer: " + (waiting.kind === "question" ? plainMessage(waiting.question.text) : waiting.kind === "credential" ? credentialAsk(waiting.request) : reviewLine(waiting.review))} onClick={() => onAnswer(waiting.key)}><Icon name="command-center" /><span>Answer</span></button>}
      {task.ticket && <TicketLink ticket={task.ticket} className="icon-button raised pill-link" />}
      {pr && <a className="icon-button raised pill-link" href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)} data-tip="Open pull request">{badge.github ? <svg className="icon brand-glyph" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={BRAND_MARKS.github.path} /></svg> : <Icon name="pull-request" />}<span>{badge.label}</span></a>}
      {owner && task.local_checks && <LocalChecksLink checks={task.local_checks} taskId={task.id} className="icon-button raised pill-link" />}
      {pr && test && <PullRequestTestLink test={test} approved={!!task.hosted_checks?.approved} className="icon-button raised pill-link" />}
      {owner && task.deployment && <DeploymentLink deployment={task.deployment} className="icon-button raised pill-link" />}
      <ClickFeedback text={feedback} />
    </div>}
  </header>;
}
