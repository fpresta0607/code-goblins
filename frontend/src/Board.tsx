// Board column ordering and card selection adapted from Cline Kanban's
// kanban-board.tsx. Copyright 2026 Cline Bot Inc., Apache-2.0.
// CFO changes: evidence-derived columns, semantic buttons, no drag transitions.
import type { Task } from "./types";

const BOARD_COLUMN_ORDER = [
  "queued",
  "working",
  "review",
  "blocked",
  "done",
] as const;
const titles = {
  queued: "Queued",
  working: "In progress",
  review: "Review & delivery",
  blocked: "Needs attention",
  done: "Delivered",
};
export function taskColumn(task: Task): (typeof BOARD_COLUMN_ORDER)[number] {
  if (task.phase === "unavailable") return "blocked";
  if (
    task.phase === "queued" ||
    task.phase === "blocked" ||
    task.phase === "done"
  )
    return task.phase;
  if (
    task.phase === "review" ||
    task.phase === "ready" ||
    task.phase === "merged"
  )
    return "review";
  return "working";
}
export function age(timestamp: string): string {
  if (!timestamp || timestamp.startsWith("0001")) return "No evidence";
  const seconds = Math.max(
    0,
    Math.floor((Date.now() - Date.parse(timestamp)) / 1000),
  );
  if (!Number.isFinite(seconds)) return "Unknown freshness";
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}
export function Badge({ phase }: { phase: string }) {
  return (
    <span className={`badge phase-${phase}`}>
      {phase === "unknown"
        ? "Unknown"
        : phase === "ready"
          ? "Checks passed"
          : phase === "merged"
            ? "Verify landed content"
            : phase || "Unknown"}
    </span>
  );
}
export function KanbanBoard({
  tasks,
  onCardSelect,
}: {
  tasks: Task[];
  onCardSelect: (id: string) => void;
}) {
  return (
    <div className="board" aria-label="Task board">
      {BOARD_COLUMN_ORDER.map((column) => {
        const cards = tasks.filter((task) => taskColumn(task) === column);
        return (
          <section
            className="board-column"
            key={column}
            aria-labelledby={`column-${column}`}
          >
            <header className="column-header">
              <span className={`column-dot ${column}`} />
              <h2 id={`column-${column}`}>{titles[column]}</h2>
              <span className="count">{cards.length}</span>
            </header>
            <div className="column-cards">
              {cards.map((task) => (
                <button
                  className="task-card"
                  key={task.id}
                  onClick={() => onCardSelect(task.id)}
                >
                  <div className="card-top">
                    <span className="project-label">
                      {task.project || "Unassigned project"}
                    </span>
                    <span className="task-mark" aria-hidden="true">
                      ↗
                    </span>
                  </div>
                  <h3>{task.title}</h3>
                  <p className="card-reason">
                    {task.reason || "Awaiting dispatch"}
                  </p>
                  {task.dependencies.length > 0 && (
                    <p className="dependency">
                      Depends on {task.dependencies.join(", ")}
                    </p>
                  )}
                  <div className="card-tags">
                    <Badge phase={task.phase} />
                    {task.phase !== "done" &&
                      task.runtime?.state &&
                      !["busy", "active", "idle", "parked"].includes(
                        task.runtime.state,
                      ) && (
                        <span className="harness-label">
                          Worker {task.runtime.state}
                        </span>
                      )}
                    {task.harness && (
                      <span className="harness-label">{task.harness}</span>
                    )}
                  </div>
                  <footer>
                    <span className="mono">
                      {task.model && task.model !== "default"
                        ? task.model
                        : task.id}
                    </span>
                    <span>{age(task.at)}</span>
                  </footer>
                </button>
              ))}
              {cards.length === 0 && (
                <div className="empty-column">
                  <span aria-hidden="true">○</span>
                  <p>
                    {column === "blocked"
                      ? "Nothing needs a decision"
                      : column === "done"
                        ? "Verified deliveries appear here"
                        : "No tasks in this stage"}
                  </p>
                </div>
              )}
            </div>
          </section>
        );
      })}
    </div>
  );
}
