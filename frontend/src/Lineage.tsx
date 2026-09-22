import { useState } from "react";
import type { Session, Snapshot } from "./types";
import { age, Badge } from "./Board";
import { lineageRoots, sessionModel } from "./lineageTree";

export function Lineage({
  snapshot,
  onSelect,
}: {
  snapshot: Snapshot;
  onSelect: (session: Session) => void;
}) {
  const [mode, setMode] = useState("canvas");
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const toggle = (id: string) =>
    setCollapsed((prior) => {
      const next = new Set(prior);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const render = (node: Session, ancestors: Set<string>) => {
    if (ancestors.has(node.id))
      return (
        <li className="lineage-warning" key={node.id}>
          Cyclic link refused: {node.id}
        </li>
      );
    const seen = new Set([...ancestors, node.id]);
    const children = snapshot.sessions.filter(
      (child) => child.parent === node.id,
    );
    const parent = snapshot.sessions.find(
      (parent) => parent.id === node.parent,
    );
    const task = snapshot.tasks.find((task) => task.id === node.task_id);
    const isCollapsed = collapsed.has(node.id);
    return (
      <li key={node.id}>
        <div className={`lineage-node role-${node.role}`}>
          <div className="node-relation">
            {node.parent
              ? parent
                ? `${node.relation || "Reported"} child`
                : `${snapshot.retired.includes(node.parent) ? "Retired" : "Unreported"} parent · ${node.parent}`
              : node.role === "cfo"
                ? "Reported CFO session"
                : "Parent unknown / unlinked"}
          </div>
          <button className="node-select" onClick={() => onSelect(node)}>
            <div className="node-heading">
              <span className="role-icon" aria-hidden="true">
                {node.role === "cfo" ? "◆" : node.role === "goblin" ? "◈" : "◇"}
              </span>
              <span>
                <strong>
                  {node.role === "cfo"
                    ? "CFO"
                    : node.role === "goblin"
                      ? "Goblin"
                      : node.role === "worker"
                        ? "Background worker"
                        : node.agent_type || "Subagent"}
                </strong>
                <span className="node-task">
                  {task?.title || node.task_id || node.native_id}
                </span>
              </span>
              <Badge phase={node.runtime?.state || node.phase} />
            </div>
            <div className="node-meta">
              <span>
                {node.harness} / {sessionModel(node, task)}
              </span>
              <span>
                {node.runtime?.state ? `Native: ${node.phase} - ` : ""}
                {age(node.updated_at)}
              </span>
            </div>
          </button>
          {children.length > 0 && (
            <button
              className="collapse-node"
              aria-expanded={!isCollapsed}
              onClick={() => toggle(node.id)}
            >
              {isCollapsed ? "▸" : "▾"} {children.length}{" "}
              {children.length === 1 ? "child" : "children"}
            </button>
          )}
        </div>
        {!isCollapsed && children.length > 0 && (
          <ul className="lineage-children">
            {children.map((child) => render(child, seen))}
          </ul>
        )}
      </li>
    );
  };
  const dependencies = snapshot.tasks.flatMap((task) =>
    task.dependencies.map((parent) => ({ task, parent })),
  );
  return (
    <section className="lineage-panel">
      <div className="section-toolbar">
        <div>
          <h2>Orchestration</h2>
          <p>Relationships reported by native sessions</p>
        </div>
        <div className="segmented" aria-label="Lineage layout">
          {["canvas", "list"].map((value) => (
            <button
              key={value}
              aria-pressed={value === mode}
              onClick={() => setMode(value)}
            >
              {value === "canvas" ? "Canvas" : "Nested list"}
            </button>
          ))}
        </div>
      </div>
      <div className="lineage-legend">
        <span>
          <i className="legend-line" /> Spawned / delegated
        </span>
        <span>
          <i className="legend-line dependency-line" /> Task dependency
        </span>
        <span>Unlinked stays explicit</span>
      </div>
      {snapshot.sessions.length === 0 ? (
        <div className="empty-state">
          <h3>No native sessions reported</h3>
          <p>
            Install the verified harness hooks with{" "}
            <code>cfo hooks install</code>. Real parent relationships will
            appear as sessions report them.
          </p>
        </div>
      ) : (
        <div className={`lineage-space ${mode}`}>
          <ul className="lineage-roots">
            {lineageRoots(snapshot.sessions).map((node) =>
              render(node, new Set()),
            )}
          </ul>
        </div>
      )}
      {dependencies.length > 0 && (
        <div className="dependency-links">
          <h3>Task dependencies</h3>
          {dependencies.map(({ task, parent }) => (
            <div className="dependency-link" key={`${parent}-${task.id}`}>
              <span>{parent}</span>
              <span className="dependency-arrow" aria-label="blocks">
                ╌╌→
              </span>
              <span>{task.title}</span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
