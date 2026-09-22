import { useState, type ReactNode } from "react";
import type { Session, Snapshot, Task } from "./types";
import {
  parseAction,
  parseDiff,
  parseFiles,
  parseHistory,
  parseTerminal,
  strings,
  decisionText,
} from "./types";
import { message, request, useNativeOutput, useResource } from "./api";
import { age } from "./presentation";
import { Avatar } from "./Avatar";
import { nativeStatus, nodeStatus, personaFor, taskColumn } from "./workflow";
import { MessageComposer, type MessageControls } from "./Messages";
import { DiffView } from "./DiffView";
import { ownsTaskSession, sessionModel, sessionRole, sessionTitle } from "./lineageTree";
import type { ReviewControls } from "./review";

function ErrorBox({ error, retry }: { error: string; retry?: () => void }) {
  return (
    <div className="error-box" role="alert">
      {error}
      {retry && <button onClick={retry}>Retry</button>}
    </div>
  );
}

function FileReview({ task, path, revision, reviews, connected }: {
  task: Task; path: string; revision: string; reviews: ReviewControls; connected: boolean;
}) {
  const diff = useResource(
    "/api/tasks/" + encodeURIComponent(task.id) + "/diff?revision=" + encodeURIComponent(revision) + "&path=" + encodeURIComponent(path),
    parseDiff,
  );
  if (diff.error) return <ErrorBox error={diff.error} retry={diff.reload} />;
  if (!diff.data) return <p className="loading" role="status">Loading code preview…</p>;
  return <DiffView diff={diff.data} reviews={reviews} connected={connected} />;
}

function Changes({ task, revision = "", reviews, connected }: {
  task: Task; revision?: string; reviews: ReviewControls; connected: boolean;
}) {
  const files = useResource("/api/tasks/" + encodeURIComponent(task.id) + "/files?revision=" + encodeURIComponent(revision), parseFiles);
  const [version, setVersion] = useState(0);
  return <div className="changes">
    <div className="section-toolbar"><p className="muted">{revision ? "Commit " + revision.slice(0, 8) : "Full task changes"} · {files.data?.length ?? "…"} files</p>
      <button onClick={() => { files.reload(); setVersion((prior) => prior + 1); }}>Refresh changes</button>
    </div>
    {files.error ? <ErrorBox error={files.error} retry={files.reload} /> :
      !files.data ? <p className="loading" role="status">Reading the change set…</p> :
        !files.data.length ? <p className="muted padded">No previewable changes in this change set.</p> :
          <div className="file-reviews">{files.data.map((file, i) => <Disclosure
            key={revision + ":" + file.path} title={<><span className="file-name">{file.path}</span><span className={"file-status status-" + file.status}>{file.status}</span></>}
            defaultOpen={i === 0} kind="file-review">
            <FileReview key={version} task={task} path={file.path} revision={revision} reviews={reviews} connected={connected} />
          </Disclosure>)}</div>}
  </div>;
}

// Selection of commits and on-demand commit diffs follows Cline Kanban's
// git-history-view.tsx. CFO omits destructive discard/ref mutation controls.
function History({
  task,
  reviews,
  connected,
}: {
  task: Task;
  reviews: ReviewControls;
  connected: boolean;
}) {
  const history = useResource(
    `/api/tasks/${encodeURIComponent(task.id)}/history`,
    parseHistory,
  );
  const [selected, setSelected] = useState("");
  const revision = history.data?.some((commit) => commit.sha === selected)
    ? selected
    : history.data?.[0]?.sha;
  if (history.error)
    return <ErrorBox error={history.error} retry={history.reload} />;
  if (!history.data)
    return (
      <p className="loading" role="status">
        Reading commit history…
      </p>
    );
  return (
    <div className="history-layout">
      <ol className="commit-list" aria-label="Commits">
        {history.data.map((commit) => (
          <li key={commit.sha}>
            <button
              aria-pressed={commit.sha === revision}
              onClick={() => setSelected(commit.sha)}
            >
              <span className="commit-dot" />
              <strong>{commit.subject}</strong>
              <span>
                <code>{commit.short}</code> · {commit.author}
              </span>
              <small>{new Date(commit.date).toLocaleString()}</small>
            </button>
          </li>
        ))}
      </ol>
      <div className="commit-diff">
        {revision ? (
          <Changes
            key={revision}
            task={task}
            revision={revision}
            reviews={reviews}
            connected={connected}
          />
        ) : (
          <p className="muted">No commits yet.</p>
        )}
      </div>
    </div>
  );
}

function Terminal({ task, connected, visible, messages }: { task: Task; connected: boolean; visible: boolean; messages: MessageControls }) {
  const terminal = useNativeOutput(visible && connected ? `/api/tasks/${encodeURIComponent(task.id)}/terminal` : null, parseTerminal);
  return <section className="worker-terminal" aria-label="Task terminal">
    <div className="native-output-heading"><h3>Native session</h3><button disabled={terminal.loading || !connected} onClick={terminal.reload}>Refresh</button></div>
    {terminal.error && <ErrorBox error={terminal.error} retry={terminal.reload} />}
    {terminal.data === undefined ? <p className="loading" role="status">{connected ? "Reading native output…" : "Disconnected from the supervisor."}</p>
      : <pre className="native-session-output" tabIndex={0}>{terminal.data || "No terminal output reported."}</pre>}
    <p className="transport-note">Captured native output · submitted messages</p>
    <MessageComposer messages={messages} channel={"task:" + task.id} recipient={{ kind: "feedback", task_id: task.id, generation: task.generation }} connected={connected} disabled={!!terminal.error} />
  </section>;
}
// Native disclosure styling and load-on-open behavior adapted from SIQshift
// settings-group and shared ShiftGroups. Closing releases preview resources;
// feedback stays mounted separately so an ambiguous submission keeps its ID.
function Disclosure({ title, children, defaultOpen = false, kind = "" }: { title: ReactNode; children: ReactNode; defaultOpen?: boolean; kind?: string }) {
  const [open, setOpen] = useState(defaultOpen);
  return <details className={"disclosure " + kind} open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>{title}</summary>
    {open && <div className="disclosure-content">{children}</div>}
  </details>;
}

function Activity({ task, snapshot }: { task?: Task; snapshot: Snapshot }) {
  const activity = useResource(task?.generation ? "/api/tasks/" + encodeURIComponent(task.id) + "/activity" : null, strings);
  const actions = snapshot.actions.filter((action) => action.task_id === task?.id).slice(-20).reverse();
  return <>
    {activity.error ? <ErrorBox error={activity.error} retry={activity.reload} /> :
      activity.data?.length ? <ol className="activity-list">
        {activity.data.slice().reverse().map((line, i) => <li key={i}>{line}</li>)}
      </ol> : <p className="muted">{task?.generation && !activity.data ? "Loading activity…" : "No task status records yet."}</p>}
    {actions.length > 0 && <section className="action-history">
      <h3>Action delivery</h3>
      <ol className="action-list">{actions.map((action) => <li key={action.id}>
        <div><strong>{action.kind}</strong><span className={"action-state " + action.status}>{action.status}</span><time>{age(action.updated_at)}</time></div>
        <p>{action.message || "Waiting for execution"}</p>
        {action.status === "uncertain" && <p className="warning-text">Inspect {action.kind === "review" ? "the CFO queue" : "the terminal"} before sending again. This action will not be replayed automatically.</p>}
      </li>)}</ol>
    </section>}
  </>;
}

export function Details({ task, node, missingSession, snapshot, connected, visible, reviews, messages, onSelectTask }: {
  task?: Task;
  node?: Session;
  missingSession: boolean;
  snapshot: Snapshot;
  connected: boolean;
  visible: boolean;
  reviews: ReviewControls;
  messages: MessageControls;
  onSelectTask: (id: string, source: HTMLElement) => void;
}) {
  const [actionError, setActionError] = useState("");
  const [evaluating, setEvaluating] = useState(false);
  const canUseTask = !!task?.generation;
  const taskOwner = ownsTaskSession(node, task);
  const completed = !!task && taskColumn(task) === "Completed";
  const runtime = node?.runtime || (taskOwner ? task?.runtime : undefined);
  const decisions = snapshot.decisions.filter((decision) => decision.key === task?.id && decision.kind !== "heartbeat");
  const refreshEvidence = async () => {
    if (!task || evaluating) return;
    setEvaluating(true);
    setActionError("");
    try {
      parseAction(await request("/api/actions", undefined, {
        method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance },
        body: JSON.stringify({ id: crypto.randomUUID(), kind: "evaluate", task_id: task.id, generation: task.generation }),
      }));
    } catch (error: unknown) { setActionError(message(error)); }
    finally { setEvaluating(false); }
  };
  return <section className="details-panel" aria-labelledby="details-title">
    <header className="panel-header">
      <Avatar persona={personaFor(task, node)} small />
      <div><p className="muted">{node ? sessionRole(node) : "Goblin"}</p>
        <h2 id="details-title">{node ? sessionTitle(node, task) : task?.title || "Session unavailable"}</h2>
        <p className="panel-status">{nodeStatus({ id: node?.id || task?.id || "", title: "", task, session: node, relation: "" })}</p>
      </div>
    </header>
    <div className="panel-content">
      {missingSession && <p className="warning-text">This session is no longer in the active store. Its parent may be retained as a retired reference.</p>}
      {!taskOwner && task && <p className="task-context">Task: <button onClick={(event) => onSelectTask(task.id, event.currentTarget)}>{task.title || task.id}</button></p>}
      {canUseTask && <Disclosure title="Changes" defaultOpen={completed} kind="changes-section"><Changes task={task} reviews={reviews} connected={connected} /></Disclosure>}
      {canUseTask && taskOwner && <Disclosure title="Terminal" defaultOpen={!completed} kind="terminal-section"><Terminal task={task} connected={connected} visible={visible} messages={messages} /></Disclosure>}
      {(!canUseTask || !taskOwner) && <p className="muted padded">{node && !taskOwner ? "This child has no separately reported native terminal." + (task ? " Its owning task is linked above." : "") : "Native terminal transport is not reported for this session."}</p>}
      {decisions.length > 0 && <details className="task-decisions disclosure" aria-label="Task decisions">
        <summary>{decisions.length} {decisions.length === 1 ? "item" : "items"} awaiting CFO</summary>
        {decisions.slice().reverse().map((decision) => <article className="decision" key={decision.seq}><p>{decisionText(decision.detail)}</p><time>{age(decision.time)}</time></article>)}
      </details>}
      <Disclosure title="Activity"><Activity task={task} snapshot={snapshot} /></Disclosure>
      {canUseTask && <Disclosure title="History"><History task={task} reviews={reviews} connected={connected} /></Disclosure>}
      <Disclosure title="Session evidence">
        <p className="panel-reason">{taskOwner ? task?.reason || "Awaiting task evidence." : "Native session evidence. Task completion is evaluated separately."}</p>
        <dl className="summary-facts">
          <div><dt>Harness</dt><dd>{node?.harness || (taskOwner ? task?.harness : "") || "Unreported"}</dd></div>
          <div><dt>Model</dt><dd>{sessionModel(node, task)}{taskOwner && task?.effort && <span className="effort"> · {task.effort} effort</span>}</dd></div>
          {runtime?.state && <div><dt>Runtime</dt><dd>{nativeStatus(runtime.state)}<p className="muted">{runtime.reason}</p></dd></div>}
          <div><dt>Evidence</dt><dd>{age(node?.updated_at || task?.at || "")}{runtime && <p className="muted">Runtime checked {age(runtime.at)}</p>}</dd></div>
        </dl>
        <dl className="evidence-facts">
          <div><dt>Native session</dt><dd>{node?.native_id || task?.session || "Unknown / unlinked"}</dd></div>
          <div><dt>Parent relationship</dt><dd>{node?.parent
            ? (snapshot.retired.includes(node.parent) ? "Retired parent" : snapshot.sessions.some((parent) => parent.id === node.parent) ? node.relation || "Reported child" : "Unreported parent") + " · " + node.parent
            : node?.role === "cfo" ? "Reported supervisor" : "Unknown / unlinked"}</dd></div>
          <div><dt>Reported root</dt><dd>{node?.reported_root || "Not reported"}</dd></div>
          {node && <div><dt>Native phase</dt><dd>{nativeStatus(node.phase)}</dd></div>}
          {task?.head && <div><dt>Evaluated commit</dt><dd className="mono">{task.head}</dd></div>}
          {task?.pr && <div><dt>Pull request</dt><dd>{/^https:\/\/github\.com\/[^/]+\/[^/]+\/pull\/\d+$/.test(task.pr)
            ? <a href={task.pr} target="_blank" rel="noreferrer">Open pull request ↗</a> : task.pr}</dd></div>}
        </dl>
        {task && task.dependencies.length > 0 && <section className="task-dependencies" aria-label="Task dependencies"><h3>Depends on</h3>
          {task.dependencies.map((id) => {
            const dependency = snapshot.tasks.find((other) => other.id === id);
            return dependency ? <button key={id} onClick={(event) => onSelectTask(id, event.currentTarget)}>{dependency.title || id}</button> : <p key={id}>Unreported task: {id}</p>;
          })}
        </section>}
        {canUseTask && <button disabled={!connected || evaluating} onClick={() => { void refreshEvidence(); }}>{evaluating ? "Queuing…" : "Refresh evidence"}</button>}
        {actionError && <ErrorBox error={actionError} />}
      </Disclosure>
    </div>
  </section>;
}
