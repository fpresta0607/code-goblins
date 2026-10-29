import { useState, type ReactNode } from "react";
import { message, request } from "./api";
import { type Session, type Snapshot, type Task } from "./types";
import { Avatar } from "./Avatar";
import { HarnessMark } from "./harness-mark";
import { BRAND_MARKS } from "./brandMarks";
import { Icon } from "./Icon";
import { Notice } from "./notice";
import { ownsTaskSession, sessionTitle } from "./lineageTree";
import { ALREADY_FINISHED, harnessName, harnessTip, liveCfoSession, nodeStatus, personaFor, pullRequestBadge, pullRequestLabel, safePullRequest, waitingTarget } from "./workflow";
import { reviewLine, waitingItems, type Item } from "./commandQueue";
import { credentialAsk } from "./credentials";
import { signInHeading } from "./browser-signins";
import { plainMessage } from "./messageText";
import { AfkToggle } from "./afk-toggle";
import { OPEN_AFK_REPORT } from "./afk";
import { PanelRow, type PanelControl } from "./panel-row";
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
// it. The header is one short row, so the conversation gets the room (the
// Overlord, 2026-10-29: "Minimize height and maximize the space"): the
// session's small picture, its name, its harness's mark and a goblin's task
// id, each cut short when the row is narrow, then switcher, the panel's Chat
// and Task icons, centred between them and the row's end, then row, the
// panel's controls with Close or Back, led on the CFO's panel by its AFK
// switch. The CFO's rare states take a line of their own under the row, at
// its end, only while one shows: Restart PC while the kernel's pools are past
// their line, Update while an update of the CFO's harness waits, and the
// words for a switch that cannot be read, so the row keeps its room.
// The CFO's picture is Home, onHome, which goes back to the board (the
// Overlord, 2026-10-29: "just click on the CFO in the top, the CFO avatar,
// and that should take us back to the home screen").
// Everything else is the details, isDetails, at the top of the Task view: the
// goblin's whole name and task id, its status, the one place the panel says
// the task's state, with one plain sentence under it and the words that
// sentence leaves out behind Details, where a failure links to the task's
// log, the task's buttons, and on the CFO's panel the last AFK report and
// what a press of Update waits for.
export function PanelHeader({ task, node, snapshot, isDetails = false, switcher, row, onAnswer, onOpenTask, onOpenLog, onHome }: { task?: Task; node?: Session; snapshot: Snapshot; isDetails?: boolean; switcher?: ReactNode; row?: { controls: PanelControl[]; corner: ReactNode; notice?: ReactNode }; onAnswer: (key: string) => void; onOpenTask: (task: Task) => void; onOpenLog: () => void; onHome?: () => void }) {
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
  const harness = (task && task.harness) || (node && node.harness) || (cfo ? snapshot.cfo_harness : "");
  const goblinTask = owner && task.phase !== "queued" && task.goblin_name ? task.id : "";
  // A helper names the goblin it helps here, since its card on the board is
  // one row with no room for it.
  const helped = owner && task.parent ? snapshot.tasks.find((other) => other.id === task.parent) : undefined;
  const helps = owner && task.parent ? (helped ? goblinName(helped) : task.parent) : "";
  const said = owner ? taskSummary(task, snapshot.tasks, status) : undefined;
  // What says a queued task already finished is what a person needs before
  // removing it or queueing it again, so it shows under its status, muted.
  const evidence = owner && status === ALREADY_FINISHED ? finishedEvidence(task.finished) : "";
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
  // What the details show at the top of the Task view.
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
        <button className="icon-button raised opens-on-pc" disabled={opening || !snapshot.instance} aria-label="Open in VS Code" onClick={() => void open("vscode")}><img className="brand-icon" src="/assets/vscode.svg" alt="" /></button>
        <button className="icon-button raised opens-on-pc" disabled={opening || !snapshot.instance} aria-label="Open folder" onClick={() => void open("folder")}><Icon name="folder" /></button>
      </>}
      {waiting && <button className="icon-button raised pill-link answer" aria-label={"Answer: " + (waiting.kind === "question" ? plainMessage(waiting.question.text) : waiting.kind === "credential" ? credentialAsk(waiting.request) : waiting.kind === "browser-signin" ? signInHeading(waiting.signin) : reviewLine(waiting.review))} onClick={() => onAnswer(waiting.key)}><Icon name="command-center" /><span>Answer</span></button>}
      {task.ticket && <TicketLink ticket={task.ticket} className="icon-button raised pill-link" />}
      {pr && <a className="icon-button raised pill-link" href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)}>{badge.github ? <svg className="icon brand-glyph" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={BRAND_MARKS.github.path} /></svg> : <Icon name="pull-request" />}<span>{badge.label}</span></a>}
      {owner && task.local_checks && <LocalChecksLink checks={task.local_checks} taskId={task.id} className="icon-button raised pill-link" />}
      {pr && test && <PullRequestTestLink test={test} approved={!!task.hosted_checks?.approved} className="icon-button raised pill-link" />}
      {owner && task.deployment && <DeploymentLink deployment={task.deployment} className="icon-button raised pill-link" />}
      <ClickFeedback text={feedback} />
    </div>;
  const awaitedLink = awaited && <button className="status-link" aria-label={"Open " + goblinName(awaited) + ", which this goblin is waiting on"} onClick={() => onOpenTask(awaited)}><Icon name="next" /></button>;
  if (isDetails) return <div className="panel-header-details">
    {owner && task.phase === "queued" && <TaskAdjustment key={task.id} task={task} snapshot={snapshot} />}
    {goblinTask && <p className="panel-whole-name">{title} · {goblinTask}</p>}
    {projectLine}{helper}{partOf}
    <p className={"panel-status plain-status phase-" + phase}><span className="status-dot" />{status}{awaitedLink}</p>
    {evidence && <p className="muted">{evidence}</p>}
    {activity}{detailsRow}{actions}
    {cfo && snapshot.afk.state === "off" && snapshot.afk.page && <div className="panel-actions"><button className="labelled-button" onClick={() => window.dispatchEvent(new Event(OPEN_AFK_REPORT))}><Icon name="file" /><span>Open your last AFK report</span></button></div>}
    {cfo && snapshot.cfo_update?.pending && <Notice tone="warning" icon="clock">Restarts onto the {harnessName(snapshot.cfo_update.harness)} update when its turn ends.</Notice>}
  </div>;
  return <>
    <header className="panel-header one-row">
      <div className="panel-identity">
        {cfo && onHome ? <button className="panel-home" aria-label="Home" onClick={onHome}><Avatar persona="cfo" small /></button> : <Avatar persona={cfo ? "cfo" : personaFor(task, node)} small />}
        <h2 id="panel-title">{title}</h2>
        {harness && <HarnessMark harness={harness} label={harnessTip(harness, cfo ? cfoSession?.model || "" : task?.model || node?.model || "", task?.effort || "")} />}
        {goblinTask && <p className="panel-goblin-task">{goblinTask}</p>}
      </div>
      {switcher}
      {row && <PanelRow {...row} isShared leading={cfo && <AfkToggle afk={snapshot.afk} instance={snapshot.instance} />} />}
    </header>
    {cfo && <div className="cfo-header-line">
      <RestartPcButton snapshot={snapshot} />
      <CfoUpdate snapshot={snapshot} />
      {snapshot.afk.state === "unreadable" && <span className="muted">Unreadable: a press resets it</span>}
    </div>}
  </>;
}
