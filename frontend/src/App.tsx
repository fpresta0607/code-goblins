import { useState } from "react";
import { useRuntimeStream } from "./stream";
import { age, KanbanBoard } from "./Board";
import { Lineage } from "./Lineage";
import { Details } from "./Details";

export function App() {
  const { snapshot, connection, error } = useRuntimeStream();
  const [view, setView] = useState("board");
  const [search, setSearch] = useState("");
  const [project, setProject] = useState("");
  const [selected, setSelected] = useState<{
    task?: string;
    session?: string;
  } | null>(null);
  const tasks = snapshot?.tasks ?? [];
  const filtered = tasks.filter(
    (task) =>
      (!project || task.project === project) &&
      `${task.title} ${task.id} ${task.harness} ${task.model}`
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  const node =
    snapshot?.sessions.find((node) => node.id === selected?.session) ||
    snapshot?.sessions.find(
      (node) =>
        node.id === tasks.find((task) => task.id === selected?.task)?.session,
    );
  const task = tasks.find(
    (task) => task.id === (selected?.task || node?.task_id),
  );
  const decisions =
    snapshot?.decisions.filter((decision) => decision.kind !== "heartbeat") ??
    [];
  const active = tasks.filter((task) => task.phase === "working").length;
  const awaiting = tasks.filter((task) =>
    ["blocked", "review", "ready", "merged"].includes(task.phase),
  ).length;
  const selectTask = (id: string) => setSelected({ task: id });
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <a className="brand" href="/" aria-label="CFO native board">
          <img src="/favicon.svg" width="36" height="36" alt="" />
          <span>
            CFO<span className="brand-caption">CODE GOBLINS</span>
          </span>
        </a>
        <div className="workspace-label">CONTROL ROOM</div>
        <nav aria-label="Primary navigation">
          {[
            { id: "board", icon: "▦", label: "Task board" },
            { id: "lineage", icon: "⑂", label: "Orchestration" },
            { id: "decisions", icon: "◇", label: "Decision inbox" },
          ].map((item) => (
            <button
              key={item.id}
              className={view === item.id ? "selected" : ""}
              aria-current={view === item.id ? "page" : undefined}
              onClick={() => setView(item.id)}
            >
              <span className="nav-icon" aria-hidden="true">
                {item.icon}
              </span>
              {item.label}
              {item.id === "decisions" && decisions.length > 0 && (
                <span className="nav-count">{decisions.length}</span>
              )}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="native-status">
            <span
              className={`live-dot ${connection !== "Live" ? "offline" : ""}`}
            />
            <div>
              <strong>Native supervisor</strong>
              <small>
                {connection === "Live"
                  ? "Runs independently of this browser"
                  : connection}
              </small>
            </div>
          </div>
          <a
            className="notice-link"
            href="/assets/NOTICE.txt"
            target="_blank"
            rel="noreferrer"
          >
            Source & license notices ↗
          </a>
        </div>
      </aside>
      <main className="main-content">
        <header className="topbar">
          <div>
            <span className="breadcrumb">Fleet</span>
            <span className="breadcrumb-divider">/</span>
            <span>
              {view === "board"
                ? "Overview"
                : view === "lineage"
                  ? "Orchestration"
                  : "Decisions"}
            </span>
          </div>
          <div className="connection" role="status">
            <span
              className={`live-dot ${connection !== "Live" ? "offline" : ""}`}
            />
            {connection}
            <span className="connection-detail">
              {snapshot
                ? `Updated ${age(snapshot.at)}`
                : "Waiting for native evidence"}
            </span>
          </div>
        </header>
        <div className="page-content">
          <div className="page-heading">
            <div className="eyebrow">CFO / NATIVE SUPERVISION</div>
            <h1>
              {view === "board"
                ? "The fleet, at a glance."
                : view === "lineage"
                  ? "Follow the work."
                  : "Your next decisions."}
            </h1>
            <p>
              {view === "board"
                ? "Every task, its current evidence, and what happens next."
                : view === "lineage"
                  ? "CFO, goblins and their reported child sessions, connected."
                  : "Durable signals that need the CFO’s attention."}
            </p>
          </div>
          {(error || snapshot?.error) && (
            <div className="connection-banner" role="alert">
              {error || snapshot?.error}
            </div>
          )}
          {!snapshot ? (
            <div className="empty-state initial-loading" role="status">
              <span className="loading-orbit" aria-hidden="true">
                ◌
              </span>
              <h2>Connecting to the supervisor</h2>
              <p>
                {error || "Loading durable tasks and native session evidence…"}
              </p>
              <code>cfo serve</code>
            </div>
          ) : (
            <>
              <div className="fleet-strip">
                <div>
                  <span className="metric-value">
                    {tasks.length.toString().padStart(2, "0")}
                  </span>
                  <span>tasks in view</span>
                </div>
                <div>
                  <span className="metric-value mint">
                    {active.toString().padStart(2, "0")}
                  </span>
                  <span>in progress</span>
                </div>
                <div>
                  <span className="metric-value amber">
                    {awaiting.toString().padStart(2, "0")}
                  </span>
                  <span>awaiting evidence or decision</span>
                </div>
                <div className="strip-note">
                  <span className="live-dot" />
                  {snapshot.sessions.length} reported sessions
                  <span className="muted">
                    {snapshot.inbox
                      ? `${snapshot.inbox} events awaiting ingestion`
                      : "Evidence-driven progression"}
                  </span>
                </div>
              </div>
              {view === "board" ? (
                <>
                  <div className="board-toolbar">
                    <div className="board-title">
                      <h2>Task board</h2>
                      <span className="muted">{filtered.length} tasks</span>
                    </div>
                    <div className="board-filters">
                      <label>
                        <span className="sr-only">Filter by project</span>
                        <select
                          name="project"
                          aria-label="Filter by project"
                          value={project}
                          onChange={(event) => setProject(event.target.value)}
                        >
                          <option value="">All projects</option>
                          {Array.from(
                            new Set(tasks.map((task) => task.project)),
                          )
                            .filter(Boolean)
                            .sort()
                            .map((name) => (
                              <option key={name}>{name}</option>
                            ))}
                        </select>
                      </label>
                      <label className="search-field">
                        <span aria-hidden="true">⌕</span>
                        <input
                          name="task-search"
                          aria-label="Search tasks"
                          placeholder="Search tasks…"
                          value={search}
                          onChange={(event) => setSearch(event.target.value)}
                        />
                      </label>
                    </div>
                  </div>
                  {tasks.length === 0 && (
                    <div className="first-run">
                      <h3>Your fleet is ready to observe.</h3>
                      <p>
                        Registered CFO tasks appear here. Install native hooks
                        with <code>cfo hooks install &lt;harness&gt;</code> and
                        start sessions through the existing CFO workflow.
                      </p>
                    </div>
                  )}
                  <KanbanBoard tasks={filtered} onCardSelect={selectTask} />
                </>
              ) : view === "lineage" ? (
                <Lineage
                  snapshot={snapshot}
                  onSelect={(session) => setSelected({ session: session.id })}
                />
              ) : (
                <section className="decisions">
                  <div className="section-toolbar">
                    <h2>Decision inbox</h2>
                    <span className="muted">
                      Acknowledgment remains with the CFO
                    </span>
                  </div>
                  {decisions.length === 0 ? (
                    <div className="empty-state">
                      <span className="empty-glyph">◇</span>
                      <h3>No decisions waiting</h3>
                      <p>
                        Native supervision continues when you close this board.
                      </p>
                    </div>
                  ) : (
                    decisions
                      .slice()
                      .reverse()
                      .map((decision) => (
                        <article className="decision-card" key={decision.seq}>
                          <div>
                            <span className="badge phase-blocked">
                              {decision.kind}
                            </span>
                            <time>{age(decision.time)}</time>
                          </div>
                          <h3>{decision.key}</h3>
                          <p>{decision.detail}</p>
                          {tasks.some((task) => task.id === decision.key) && (
                            <button onClick={() => selectTask(decision.key)}>
                              Inspect task ↗
                            </button>
                          )}
                        </article>
                      ))
                  )}
                </section>
              )}
              <footer className="page-footer">
                <span>
                  Native Go supervisor · Herdr workers · React + TypeScript
                </span>
                <span>
                  {snapshot.healthy
                    ? "Controller healthy"
                    : "Controller health pending"}{" "}
                  · Reconciled {age(snapshot.reconciled)}
                </span>
              </footer>
              {snapshot.issues.length > 0 && (
                <details className="event-issues">
                  <summary>
                    {snapshot.issues.length} recent event diagnostics
                  </summary>
                  <ul>
                    {snapshot.issues.map((issue, i) => (
                      <li key={i}>{issue}</li>
                    ))}
                  </ul>
                </details>
              )}
            </>
          )}
        </div>
      </main>
      {snapshot && selected && (task || node) && (
        <Details
          key={`${selected.task || ""}:${selected.session || ""}`}
          task={task}
          node={node}
          snapshot={snapshot}
          connected={connection === "Live"}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  );
}
