import { useId, useState, type PointerEvent, type ReactNode } from "react";
import { message, request } from "./api";
import { type Session, type Snapshot, type Task } from "./types";
import { Avatar } from "./Avatar";
import { BRAND_MARKS } from "./brandMarks";
import { Icon } from "./Icon";
import { Notice } from "./notice";
import { ownsTaskSession, sessionTitle } from "./lineageTree";
import { ALREADY_FINISHED, harnessName, liveCfoSession, nodeStatus, personaFor, pullRequestBadge, pullRequestLabel, safePullRequest, waitingTarget } from "./workflow";
import { reviewLine, waitingItems, type Item } from "./commandQueue";
import { credentialAsk } from "./credentials";
import { signInHeading } from "./browser-signins";
import { plainMessage } from "./messageText";
import { AfkToggle } from "./afk-toggle";
import { CfoUpdate } from "./cfo-update";
import { RestartPcButton } from "./restart-pc-button";
import { finishedEvidence, goblinName, taskSummary } from "./task-words";
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
// Details; a failure links to the task's log. The name and the task take the
// room first, the name on one line and the task on at most two, and the
// buttons stay beside them only while all fit on one row, else they take a
// row of their own under them. A goblin's Terminal view heads as the CFO's
// does, with its picture, its name and title, its task id and one status
// line, and no description and no buttons, which its Task view holds (the
// Overlord, 2026-10-17: "If they exist on the task panel, we don't need them
// in the terminal header"). The CFO drawn without a task has
// no worktree to open; its header carries Restart right below its name on
// its Task view, the AFK toggle beside its status, with Update before it
// while an update of the CFO's harness waits and Restart PC before that while
// the kernel's pools are past their line, and under them what a press of
// Update waits for or why it did not restart the CFO. switcher, the panel's
// Chat and Task icons, sits in the same row, before the CFO's controls, and
// the status line carries no mark of the session's harness (the Overlord,
// 2026-10-28: "We already know it's running Claude because of the chat").
export function PanelHeader({ task, node, snapshot, compact, isDetails = false, switcher, onAnswer, onOpenTask, onOpenLog }: { task?: Task; node?: Session; snapshot: Snapshot; compact: boolean; isDetails?: boolean; switcher?: ReactNode; onAnswer: (key: string) => void; onOpenTask: (task: Task) => void; onOpenLog: () => void }) {
  const [opening, setOpening] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const cfo = !task && !node;
  const owner = !!task && ownsTaskSession(node, task);
  const cfoSession = liveCfoSession(snapshot);
  const title = cfo ? "CFO" : node ? sessionTitle(node, task) : task ? goblinName(task) : "";
  const trains = snapshot.merge_trains ?? [];
  // A goblin's own panel reads its task's one status, as its card does; the
  // CFO and a child session read their sessions'.
  const { text: status, phase } = owner ? taskStatus(task, snapshot)
    : cfo ? { text: nodeStatus({ id: "cfo", title, session: cfoSession, relation: "", status: snapshot.registration ? "Registration stale" : cfoSession ? undefined : "Supervising" }), phase: snapshot.registration ? "stale" : cfoSession?.runtime?.state || cfoSession?.phase || "working" }
      : { text: nodeStatus({ id: title, title, task, session: node, relation: "" }, false, snapshot.tasks, trains), phase: node?.runtime?.state || node?.phase || "" };
 const goblinTask = owner && task.phase !== "queued" && task.goblin_name ? task.id : "";
  // A helper names the goblin it helps here, since its card on the board is
  // one row with no room for it.
  const helped = owner && task.parent ? snapshot.tasks.find((other) => other.id === task.parent) : undefined;
  const helps = owner && task.parent ? (helped ? goblinName(helped) : task.parent) : "";
  // The name or the task cut short shows in full in its tip. Each carries an
  // empty tip until it is pointed at, so the board's tips count it as a part
  // from the start, as they count a card.
  const [cut, setCut] = useState({ name: false, task: false });
  const isCut = (element: Element | null) => !!element && (element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1);
  const measure = (event: PointerEvent<HTMLElement>) => setCut({ name: isCut(event.currentTarget.querySelector("#panel-title")), task: isCut(event.currentTarget.querySelector("p.panel-goblin-task")) });
  const said = owner ? taskSummary(task, snapshot.tasks, status) : undefined;
  // What says a queued task already finished is what a person needs before
  // removing it or queueing it again, so its status carries it as its tip,
  // which the keyboard reaches and a screen reader reads with the status.
  const evidence = owner && status === ALREADY_FINISHED ? finishedEvidence(task.finished) : "";
  const evidenceId = useId();
  const pr = owner ? safePullRequest(task.pr) : "";
  const test = owner ? pullRequestTest(task, trains) : undefined;
  const badge = pullRequestBadge(pr);
  const awaited = owner ? waitingTarget(snapshot, task) : undefined;
  // What this goblin waits on the Overlord for: a question, a review item or
  // a credential request.
  // Runs are the CFO's own and never wait on a goblin's panel.
  const waiting = owner ? waitingItems(snapshot).find((item): item is Exclude<Item, { kind: "run" }> => item.kind !== "run" && (item.kind === "question" ? item.question.for_task || item.question.task : item.kind === "credential" ? item.request.task : item.kind === "browser-signin" ? item.signin.task : item.review.task) === task.id) : undefined;
  const open = async (target: "vscode" | "folder") => {
    if (!task || opening) return;
    setOpening(true);
    showFeedback("");
    try {
      await request("/api/workspace/open", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id, generation: task.generation, target }) });
    } catch (error: unknown) { showFeedback(message(error)); }
    finally { setOpening(false); }
  };
  // What the full header shows and the compact one leaves out, which the
  // panel's Task view shows when the selector chooses it.
  const projectLine = task?.project && <div className="project-line"><p className="project-label">{task.project}</p><PeopleRow snapshot={snapshot} task={task} /></div>;
  const helper = helps && <p className="muted">Helper of {helps}</p>;
  const partOf = !owner && node && task && <p className="muted">Part of {goblinName(task)}</p>;
  const activity = said?.sentence && <p className="panel-activity">{said.sentence}</p>;
  const detailsRow = said && (said.details.length > 0 || said.isFailure) && <div className="panel-details-row">
      <RawDetails lines={said.details} isPlain={said.isPlain} />
      {owner && said.isFailure && !task.archived && <button className="text-button" onClick={onOpenLog}>Open the log</button>}
    </div>;
  const actions = owner && (!!task.generation || task.ticket || pr || task.local_checks || task.deployment) && <div className="panel-actions">
      {!!task.generation && <>
        <button className="icon-button raised opens-on-pc" disabled={opening || !snapshot.instance} aria-label="Open in VS Code" data-tip="Open in VS Code" onClick={() => void open("vscode")}><img className="brand-icon" src="/assets/vscode.svg" alt="" /></button>
        <button className="icon-button raised opens-on-pc" disabled={opening || !snapshot.instance} aria-label="Open folder" data-tip="Open folder" onClick={() => void open("folder")}><Icon name="folder" /></button>
      </>}
      {waiting && <button className="icon-button raised pill-link answer" aria-label={"Answer: " + (waiting.kind === "question" ? plainMessage(waiting.question.text) : waiting.kind === "credential" ? credentialAsk(waiting.request) : waiting.kind === "browser-signin" ? signInHeading(waiting.signin) : reviewLine(waiting.review))} onClick={() => onAnswer(waiting.key)}><Icon name="command-center" /><span>Answer</span></button>}
      {task.ticket && <TicketLink ticket={task.ticket} className="icon-button raised pill-link" />}
      {pr && <a className="icon-button raised pill-link" href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)} data-tip="Open pull request">{badge.github ? <svg className="icon brand-glyph" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={BRAND_MARKS.github.path} /></svg> : <Icon name="pull-request" />}<span>{badge.label}</span></a>}
      {owner && task.local_checks && <LocalChecksLink checks={task.local_checks} taskId={task.id} className="icon-button raised pill-link" />}
      {pr && test && <PullRequestTestLink test={test} approved={!!task.hosted_checks?.approved} className="icon-button raised pill-link" />}
      {owner && task.deployment && <DeploymentLink deployment={task.deployment} className="icon-button raised pill-link" />}
      <ClickFeedback text={feedback} />
    </div>;
  const awaitedLink = awaited && <button className="status-link" aria-label={"Open " + goblinName(awaited) + ", which this goblin is waiting on"} data-tip={"Open " + goblinName(awaited)} onClick={() => onOpenTask(awaited)}><Icon name="next" /></button>;
  if (isDetails) return <div className="panel-header-details">{projectLine}{helper}{partOf}{awaited && <p>{goblinName(awaited)}{awaitedLink}</p>}{activity}{detailsRow}{actions}</div>;
  return <header className={"panel-header" + (compact ? " compact" : "")}>
    <Avatar persona={cfo ? "cfo" : personaFor(task, node)} small />
    <div className="panel-heading">
      <div className="panel-identity" onPointerEnter={measure}>
        <h2 id="panel-title" data-tip={cut.name ? title : ""}>{title}</h2>
        {owner && task.phase === "queued" ? <TaskAdjustment key={task.id} task={task} snapshot={snapshot} /> : goblinTask && <p className="panel-goblin-task" data-tip={cut.task ? goblinTask : ""}>{goblinTask}</p>}
        {!compact && projectLine}
        {!compact && helper}
        <p className={"panel-status plain-status phase-" + phase} {...(evidence ? { "data-tip": evidence, tabIndex: 0, "aria-describedby": evidenceId } : {})}><span className="status-dot" />{status}{!compact && awaitedLink}</p>
        {evidence && <span id={evidenceId} className="sr-only">{evidence}</span>}
        {!compact && partOf}
        {!compact && activity}
        {!compact && detailsRow}
      </div>
      {/* A finished task's checks and deploy are in its panel alone, so its
          links show there too; only a live one opens its folder, and not on
          a small screen, since VS Code and the folder open on the PC. */}
      {!compact && actions}
    </div>
    {switcher}
    {cfo && <AfkToggle afk={snapshot.afk} instance={snapshot.instance} leading={<><RestartPcButton snapshot={snapshot} /><CfoUpdate snapshot={snapshot} /></>} />}
    {cfo && snapshot.cfo_update?.pending && <Notice tone="warning" icon="clock">Restarts onto the {harnessName(snapshot.cfo_update.harness)} update when its turn ends.</Notice>}
  </header>;
}
