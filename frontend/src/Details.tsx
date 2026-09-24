import { useState } from "react";
import type { Snapshot, Task } from "./types";
import {
  parseDiff,
  parseFiles,
  parseHistory,
  strings,
} from "./types";
import { useResource } from "./api";
import { age } from "./presentation";
import { DiffView } from "./DiffView";
import { WorkspaceDetails } from "./WorkspaceDetails";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { deliveryMark } from "./feedback";
import type { ReviewControls } from "./review";

function ErrorBox({ error, retry }: { error: string; retry?: () => void }) {
  return (
    <div className="error-box" role="alert">
      {error}
      {retry && <button className="icon-button raised" aria-label="Retry" data-tip="Retry" data-tip-align="start" onClick={retry}><Icon name="refresh" /></button>}
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
      <button className="icon-button raised" aria-label="Refresh changes" data-tip="Refresh changes" data-tip-align="end" onClick={() => { files.reload(); setVersion((prior) => prior + 1); }}><Icon name="refresh" /></button>
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


function Activity({ task, snapshot }: { task?: Task; snapshot: Snapshot }) {
  const activity = useResource(task?.generation ? "/api/tasks/" + encodeURIComponent(task.id) + "/activity" : null, strings);
  const actions = snapshot.actions.filter((action) => action.task_id === task?.id).slice(-20).reverse();
  const actionLabel = (kind: string) => ({ review: "Review comment", feedback: "Task instruction", evaluate: "Progress check", cfo_message: "CFO message", cfo_answer: "Question answer", goblin_answer: "Question answer" })[kind] || "Action";
  return <>
    {activity.error ? <ErrorBox error={activity.error} retry={activity.reload} /> :
      activity.data?.length ? <ol className="activity-list">
        {activity.data.slice().reverse().map((line, i) => <li key={i}>{line}</li>)}
      </ol> : <p className="muted">{task?.generation && !activity.data ? "Loading activity…" : "No task status records yet."}</p>}
    {actions.length > 0 && <section className="action-history">
      <h3>Action delivery</h3>
      <ol className="action-list">{actions.map((action) => {
        const delivery = deliveryMark(action);
        return <li key={action.id}>
          <div><strong>{actionLabel(action.kind)}</strong><span className={"delivery " + action.status} role="img" aria-label={delivery.label} data-tip={delivery.label}><Icon name={delivery.icon} /></span><time>{age(action.updated_at)}</time></div>
          {action.text && <p className="action-text">{action.text}</p>}
          {delivery.trouble && <p className="warning-text">{delivery.label}{action.message && " " + action.message}</p>}
        </li>;
      })}</ol>
    </section>}
  </>;
}

// The task view of the goblin panel: where the work lives, what changed and
// what happened, below the panel header.
export function TaskView({ task, snapshot, connected, reviews }: {
  task: Task; snapshot: Snapshot; connected: boolean; reviews: ReviewControls;
}) {
  return <div className="panel-content">
      <WorkspaceDetails task={task} />
      {task.generation ? <Disclosure title="Changes" defaultOpen kind="changes-section"><Changes task={task} reviews={reviews} connected={connected} /></Disclosure> : <p className="muted padded">Changes will appear when this task starts.</p>}
      <Disclosure title="Activity"><Activity task={task} snapshot={snapshot} /></Disclosure>
      {task.generation && <Disclosure title="History"><History task={task} reviews={reviews} connected={connected} /></Disclosure>}
    </div>;
}
