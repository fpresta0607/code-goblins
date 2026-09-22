import { useEffect, useRef, useState } from "react";
import type { Feedback, Session, Snapshot, Task } from "./types";
import {
  parseAction,
  parseDiff,
  parseFiles,
  parseHistory,
  parseTerminal,
  strings,
} from "./types";
import { message, request, useResource } from "./api";
import { age, Badge } from "./Board";
import { DiffView } from "./DiffView";
import { alreadyKnown, submissionFor, type Submission } from "./feedback";
import { ownsTaskSession, sessionModel } from "./lineageTree";

function ErrorBox({ error, retry }: { error: string; retry?: () => void }) {
  return (
    <div className="error-box" role="alert">
      {error}
      {retry && <button onClick={retry}>Retry</button>}
    </div>
  );
}

function Changes({
  task,
  revision = "",
  onComment,
}: {
  task: Task;
  revision?: string;
  onComment: (context: Feedback) => void;
}) {
  const [selected, setSelected] = useState("");
  const query = `?revision=${encodeURIComponent(revision)}`;
  const files = useResource(
    `/api/tasks/${encodeURIComponent(task.id)}/files${query}`,
    parseFiles,
  );
  const path = files.data?.some((file) => file.path === selected)
    ? selected
    : files.data?.[0]?.path;
  const diff = useResource(
    path
      ? `/api/tasks/${encodeURIComponent(task.id)}/diff${query}&path=${encodeURIComponent(path)}`
      : null,
    parseDiff,
  );
  return (
    <div className="changes">
      <div className="section-toolbar">
        <h3>
          {revision ? `Commit ${revision.slice(0, 8)}` : "Task changes"}{" "}
          <span className="count">{files.data?.length ?? "…"}</span>
        </h3>
        <button
          className="subtle"
          onClick={() => {
            files.reload();
            diff.reload();
          }}
        >
          Refresh changes
        </button>
      </div>
      {files.error ? (
        <ErrorBox error={files.error} retry={files.reload} />
      ) : !files.data ? (
        <p className="loading" role="status">
          Reading the change set…
        </p>
      ) : files.data.length === 0 ? (
        <div className="empty-state">
          <h3>No previewable changes</h3>
          <p>
            This change set is empty. Credential files are excluded from
            previews.
          </p>
        </div>
      ) : (
        <>
          <div className="file-tabs" role="tablist" aria-label="Changed files">
            {files.data.map((file) => (
              <button
                key={file.path}
                role="tab"
                aria-selected={path === file.path}
                onClick={() => setSelected(file.path)}
              >
                <span className={`file-status status-${file.status}`}>
                  {file.status}
                </span>
                <span className="mono">{file.path}</span>
              </button>
            ))}
          </div>
          {diff.error ? (
            <ErrorBox error={diff.error} retry={diff.reload} />
          ) : !diff.data ? (
            <p className="loading" role="status">
              Loading code preview…
            </p>
          ) : (
            <DiffView
              key={`${path}-${revision}`}
              diff={diff.data}
              onComment={onComment}
            />
          )}
        </>
      )}
    </div>
  );
}

// Selection of commits and on-demand commit diffs follows Cline Kanban's
// git-history-view.tsx. CFO omits destructive discard/ref mutation controls.
function History({
  task,
  onComment,
}: {
  task: Task;
  onComment: (context: Feedback) => void;
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
            onComment={onComment}
          />
        ) : (
          <p className="muted">No commits yet.</p>
        )}
      </div>
    </div>
  );
}

function Terminal({ task }: { task: Task }) {
  const terminal = useResource(
    `/api/tasks/${encodeURIComponent(task.id)}/terminal`,
    parseTerminal,
  );
  return (
    <section>
      <div className="section-toolbar">
        <div>
          <h3>Task terminal</h3>
          <p>Herdr capture · last 120 lines · loaded on demand</p>
        </div>
        <button onClick={terminal.reload}>Refresh terminal</button>
      </div>
      <p className="terminal-note">
        The installed Herdr interface provides text capture and verified agent
        steering. Continue interactive terminal work in the task's Herdr tab.
      </p>
      {terminal.error ? (
        <ErrorBox error={terminal.error} retry={terminal.reload} />
      ) : terminal.data === undefined ? (
        <p className="loading" role="status">
          Reading Herdr…
        </p>
      ) : (
        <pre className="terminal-output" tabIndex={0}>
          {terminal.data || "No terminal output reported."}
        </pre>
      )}
    </section>
  );
}

function FeedbackForm({
  task,
  snapshot,
  context,
  clearContext,
  connected,
}: {
  task: Task;
  snapshot: Snapshot;
  context: Feedback | null;
  clearContext: () => void;
  connected: boolean;
}) {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);
  const [submission, setSubmission] = useState<Submission | null>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (context) textarea.current?.focus();
  }, [context]);
  const payload = JSON.stringify({
    kind: "feedback",
    task_id: task.id,
    generation: task.generation,
    text,
    ...context,
  });
  const action = snapshot.actions.find(
    (action) => action.id === submission?.id,
  );
  const known = alreadyKnown(submission, payload, snapshot.actions);
  const submit = async () => {
    if (!text.trim() || sending) return;
    if (known) {
      setError("");
      return;
    }
    const attempt = submissionFor(payload, submission, () =>
      crypto.randomUUID(),
    );
    setSubmission(attempt);
    setSending(true);
    setError("");
    try {
      parseAction(
        await request("/api/actions", undefined, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-CFO-Token": snapshot.instance,
          },
          body: JSON.stringify({
            id: attempt.id,
            kind: "feedback",
            task_id: task.id,
            generation: task.generation,
            text,
            ...context,
          }),
        }),
      );
      setText("");
      clearContext();
    } catch (error: unknown) {
      setError(message(error));
    } finally {
      setSending(false);
    }
  };
  return (
    <section className="feedback-form" aria-label="Task feedback">
      <div className="feedback-title">
        <strong>Steer this task</strong>
        <span>Verified Herdr delivery</span>
      </div>
      {context && (
        <div className="feedback-context">
          <code>
            {context.file}:{context.line} · {context.side}
          </code>
          <button
            aria-label="Clear line context"
            disabled={sending}
            onClick={clearContext}
          >
            ×
          </button>
        </div>
      )}
      <label className="sr-only" htmlFor="task-feedback">
        Feedback to task agent
      </label>
      <textarea
        id="task-feedback"
        disabled={sending}
        ref={textarea}
        rows={3}
        placeholder={
          context
            ? "Describe the change at this line…"
            : "Send a clear instruction or review note…"
        }
        value={text}
        maxLength={16000}
        onChange={(event) => {
          setText(event.target.value);
          setError("");
        }}
      />
      <div className="feedback-bottom">
        <span>Pipeline custody is checked before delivery.</span>
        <button
          className="primary"
          disabled={!connected || sending || !text.trim() || !!known}
          onClick={() => {
            void submit();
          }}
        >
          {sending
            ? "Queuing…"
            : known
              ? "Submission recorded"
              : "Send feedback"}{" "}
          <span aria-hidden="true">↗</span>
        </button>
      </div>
      {error && !action && <ErrorBox error={error} />}
      {submission && (!error || action) && (
        <p
          className={`action-result ${action?.status ?? "queued"}`}
          role="status"
        >
          {action
            ? `${action.status}: ${action.message || "Waiting for the supervisor"}`
            : "Queued for durable delivery."}
        </p>
      )}
      {known && (
        <button
          className="new-instruction"
          onClick={() => {
            setSubmission(null);
            setText("");
            setError("");
            clearContext();
          }}
        >
          Start a new instruction
        </button>
      )}
    </section>
  );
}

export function Details({
  task,
  node,
  snapshot,
  connected,
  onClose,
}: {
  task?: Task;
  node?: Session;
  snapshot: Snapshot;
  connected: boolean;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [tab, setTab] = useState("activity");
  const [context, setContext] = useState<Feedback | null>(null);
  const [actionError, setActionError] = useState("");
  const [evaluating, setEvaluating] = useState(false);
  const canUseTask = !!task?.generation;
  const taskOwner = ownsTaskSession(node, task);
  const runtime = node?.runtime || (taskOwner ? task?.runtime : undefined);
  const activity = useResource(
    tab === "activity" && canUseTask
      ? `/api/tasks/${encodeURIComponent(task.id)}/activity`
      : null,
    strings,
  );
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  const refreshEvidence = async () => {
    if (!task || evaluating) return;
    setEvaluating(true);
    setActionError("");
    try {
      parseAction(
        await request("/api/actions", undefined, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-CFO-Token": snapshot.instance,
          },
          body: JSON.stringify({
            id: crypto.randomUUID(),
            kind: "evaluate",
            task_id: task.id,
            generation: task.generation,
          }),
        }),
      );
    } catch (error: unknown) {
      setActionError(message(error));
    } finally {
      setEvaluating(false);
    }
  };
  const actions = snapshot.actions
    .filter((action) => action.task_id === task?.id)
    .slice(-20)
    .reverse();
  return (
    <dialog
      ref={dialog}
      className="details-drawer"
      aria-labelledby="details-title"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <header className="drawer-header">
        <div>
          <div className="eyebrow">
            {node?.role || "Task"} / {task?.project || node?.harness || "Fleet"}
          </div>
          <h2 id="details-title">
            {task?.title ||
              node?.agent_type ||
              node?.native_id ||
              "Session details"}
          </h2>
        </div>
        <button
          className="icon-button"
          aria-label="Close details"
          onClick={onClose}
        >
          ×
        </button>
      </header>
      <div className="drawer-summary">
        <Badge phase={(taskOwner ? task?.phase : node?.phase) || "unknown"} />
        <span>{node?.harness || task?.harness || "Harness unreported"}</span>
        <span className="mono">{sessionModel(node, task)}</span>
        {taskOwner && task?.effort && <span>{task.effort} effort</span>}
        <span className="freshness">
          {age(node?.updated_at || task?.at || "")}
        </span>
      </div>
      <p className="drawer-reason">
        {task?.reason ||
          "Native lifecycle evidence. Session activity alone does not establish task completion."}
      </p>
      {runtime?.state && (
        <p className="drawer-reason">
          Runtime: {runtime.state} - {runtime.reason} - {age(runtime.at)}
        </p>
      )}
      <div className="drawer-tabs" role="tablist" aria-label="Task detail view">
        {["activity", "changes", "history", "terminal"].map((value) => (
          <button
            key={value}
            role="tab"
            aria-selected={tab === value}
            disabled={value !== "activity" && !canUseTask}
            onClick={() => setTab(value)}
          >
            {value[0].toUpperCase() + value.slice(1)}
          </button>
        ))}
        {canUseTask && (
          <button
            className="refresh-evidence"
            disabled={!connected || evaluating}
            onClick={() => {
              void refreshEvidence();
            }}
          >
            {evaluating ? "Queuing…" : "Refresh evidence"}
          </button>
        )}
      </div>
      <div className="drawer-content">
        {actionError && <ErrorBox error={actionError} />}
        {tab === "activity" ? (
          <>
            <dl className="facts">
              <div>
                <dt>Native session</dt>
                <dd className="mono">
                  {node?.native_id || task?.session || "Unknown / unlinked"}
                </dd>
              </div>
              <div>
                <dt>Parent relationship</dt>
                <dd>
                  {node?.parent
                    ? `${node.relation} · ${node.parent}`
                    : "Unknown / unlinked"}
                </dd>
              </div>
              <div>
                <dt>Reported root</dt>
                <dd>{node?.reported_root || "Not reported"}</dd>
              </div>
              <div>
                <dt>Evidence freshness</dt>
                <dd>{age(node?.updated_at || task?.at || "")}</dd>
              </div>
              {task?.head && (
                <div>
                  <dt>Evaluated commit</dt>
                  <dd className="mono">{task.head}</dd>
                </div>
              )}
              {task?.pr && (
                <div>
                  <dt>Pull request</dt>
                  <dd>
                    {/^https:\/\/github\.com\/[^/]+\/[^/]+\/pull\/\d+$/.test(
                      task.pr,
                    ) ? (
                      <a href={task.pr} target="_blank" rel="noreferrer">
                        Open pull request ↗
                      </a>
                    ) : (
                      task.pr
                    )}
                  </dd>
                </div>
              )}
            </dl>
            <section className="activity-section">
              <h3>Activity</h3>
              {activity.error && (
                <ErrorBox error={activity.error} retry={activity.reload} />
              )}
              {activity.data?.length ? (
                <ol className="activity-list">
                  {activity.data
                    .slice()
                    .reverse()
                    .map((line, i) => (
                      <li key={i}>
                        <span className="activity-dot" />
                        <p>{line}</p>
                      </li>
                    ))}
                </ol>
              ) : (
                <p className="muted">
                  {canUseTask && !activity.data
                    ? "Loading task activity…"
                    : "No task status records yet."}
                </p>
              )}
            </section>
            {actions.length > 0 && (
              <section className="activity-section">
                <h3>Durable actions</h3>
                <ol className="action-list">
                  {actions.map((action) => (
                    <li key={action.id}>
                      <div>
                        <strong>{action.kind}</strong>
                        <span className={`action-state ${action.status}`}>
                          {action.status}
                        </span>
                        <time>{age(action.updated_at)}</time>
                      </div>
                      <p>{action.message || "Waiting for execution"}</p>
                      {action.status === "uncertain" && (
                        <p className="warning-text">
                          Inspect activity and the terminal before sending
                          again. This action will not be replayed automatically.
                        </p>
                      )}
                    </li>
                  ))}
                </ol>
              </section>
            )}
          </>
        ) : task && tab === "changes" ? (
          <Changes task={task} onComment={setContext} />
        ) : task && tab === "history" ? (
          <History task={task} onComment={setContext} />
        ) : task && tab === "terminal" ? (
          <Terminal task={task} />
        ) : null}
      </div>
      {canUseTask && (
        <FeedbackForm
          task={task}
          snapshot={snapshot}
          context={context}
          clearContext={() => setContext(null)}
          connected={connected}
        />
      )}
    </dialog>
  );
}
