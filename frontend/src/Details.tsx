import { useState, type ReactNode } from "react";
import type { Snapshot, Task } from "./types";
import {
  parseDiff,
  parseFiles,
  parseHistory,
  strings,
} from "./types";
import { useResource } from "./api";
import { age } from "./presentation";
import { Avatar } from "./Avatar";
import { nodeStatus, personaFor } from "./workflow";
import { DiffView } from "./DiffView";
import { WorkspaceDetails } from "./WorkspaceDetails";
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
  const actionLabel = (kind: string) => ({ review: "Review comment", feedback: "Task instruction", evaluate: "Progress check", cfo_message: "CFO message", cfo_answer: "Question answer" })[kind] || "Action";
  const outcomeLabel = (status: string, kind: string) => status === "succeeded" ? (kind === "review" || kind.startsWith("cfo_") ? "Sent to CFO" : "Completed") : ({ queued: "Queued", running: "Sending", failed: "Could not deliver", uncertain: "Delivery unconfirmed" })[status] || "Awaiting evidence";
  return <>
    {activity.error ? <ErrorBox error={activity.error} retry={activity.reload} /> :
      activity.data?.length ? <ol className="activity-list">
        {activity.data.slice().reverse().map((line, i) => <li key={i}>{line}</li>)}
      </ol> : <p className="muted">{task?.generation && !activity.data ? "Loading activity…" : "No task status records yet."}</p>}
    {actions.length > 0 && <section className="action-history">
      <h3>Action delivery</h3>
      <ol className="action-list">{actions.map((action) => <li key={action.id}>
        <div><strong>{actionLabel(action.kind)}</strong><span className={"action-state " + action.status}>{outcomeLabel(action.status, action.kind)}</span><time>{age(action.updated_at)}</time></div>
        <p>{action.message || "Waiting for execution"}</p>
        {action.status === "uncertain" && <p className="warning-text">Inspect {action.kind === "review" ? "the CFO queue" : "the terminal"} before sending again. This action will not be replayed automatically.</p>}
      </li>)}</ol>
    </section>}
  </>;
}

export function Details({ task, snapshot, connected, reviews }: {
  task?: Task; snapshot: Snapshot; connected: boolean; reviews: ReviewControls;
}) {
  if (!task) return <section className="review-placeholder"><Avatar persona="reviewer" /><h2>Review the work</h2><p>Select a task to explore its changes and activity.</p></section>;
  return <section className="details-panel" aria-labelledby="details-title">
    <header className="panel-header"><Avatar persona={personaFor(task)} small /><div>
      <h2 id="details-title">{task.title || task.id}</h2>
      {task.project && <p className="project-label">{task.project}</p>}
      <p className="panel-status">{nodeStatus({ id: task.id, title: task.title, task, relation: "" })}</p>
    </div><WorkspaceDetails task={task} /></header>
    <div className="panel-content">
      {task.pr && /^https:\/\/github\.com\/[^/]+\/[^/]+\/pull\/\d+$/.test(task.pr) && <a className="review-pr" href={task.pr} target="_blank" rel="noreferrer">Open pull request</a>}
      {task.generation ? <Disclosure title="Changes" defaultOpen kind="changes-section"><Changes task={task} reviews={reviews} connected={connected} /></Disclosure> : <p className="muted padded">Changes will appear when this task starts.</p>}
      <Disclosure title="Activity"><Activity task={task} snapshot={snapshot} /></Disclosure>
      {task.generation && <Disclosure title="History"><History task={task} reviews={reviews} connected={connected} /></Disclosure>}
    </div>
  </section>;
}
