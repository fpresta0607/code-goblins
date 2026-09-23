import { useState } from "react";
import type { Session, Snapshot, Task } from "./types";
import { nodeStatus } from "./workflow";
import { Avatar } from "./Avatar";
import { Chevron } from "./Chevron";
import { personaFor } from "./workflow";
import { lineageRoots, ownsTaskSession, projectSessions, sessionRole, sessionTitle, tasksWithoutSession } from "./lineageTree";

export interface Selection { session?: string; task?: string }

export function Lineage({ snapshot, project, selected, onSelect }: {
  snapshot: Snapshot;
  project: string;
  selected: Selection | null;
  onSelect: (selection: Selection, source: HTMLElement) => void;
}) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const sessions = projectSessions(snapshot.sessions, snapshot.tasks, project);
  const tasks = snapshot.tasks.filter((task) => !project || task.project === project);
  const taskOnly = tasksWithoutSession(tasks, snapshot.sessions);
  const byID = new Map(sessions.map((node) => [node.id, node]));
  const children = new Map<string, Session[]>();
  for (const node of sessions) if (node.parent) {
    const siblings = children.get(node.parent) || [];
    siblings.push(node);
    children.set(node.parent, siblings);
  }
  const toggle = (id: string) => setCollapsed((prior) => {
    const next = new Set(prior);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });
  const needsDecision = (task?: Task) => task && snapshot.decisions.some((decision) => decision.key === task.id && decision.kind !== "heartbeat");
  const render = (node: Session, ancestors: Set<string>) => {
    if (ancestors.has(node.id)) return <li className="lineage-warning" key={node.id}>Cyclic link refused: {node.native_id || node.id}</li>;
    const descendants = children.get(node.id) || [];
    const seen = new Set([...ancestors, node.id]);
    const task = snapshot.tasks.find((task) => task.id === node.task_id);
    const owner = ownsTaskSession(node, task);
    const title = sessionTitle(node, task);
    const isCollapsed = collapsed.has(node.id);
    const parent = byID.get(node.parent);
    const relation = node.parent
      ? byID.has(node.parent) ? node.relation || "Reported child"
        : snapshot.retired.includes(node.parent) ? "Retired parent" : "Parent unreported"
      : node.role === "cfo" ? "" : "Parent unknown / unlinked";
    return <li key={node.id} className="workflow-branch">
      <div className="node-group">
        {relation && <p className="node-relation">{relation}{ancestors.size >= 3 && parent && " · Parent: " + sessionTitle(parent, snapshot.tasks.find((task) => task.id === parent.task_id))}</p>}
        <div className={"workflow-node role-" + node.role + (selected?.session === node.id ? " selected" : "")}>
          <button className="node-select" aria-pressed={selected?.session === node.id}
            onClick={(event) => onSelect({ session: node.id }, event.currentTarget)}>
            <Avatar persona={personaFor(task, node)} small /><span className="node-role">{sessionRole(node)}</span>
            <strong>{title}</strong>{task?.project && <span className="project-label">{task.project}</span>}
            {!owner && node.role !== "cfo" && task?.title && <span className="node-task">Task: {task.title}</span>}
            <span className="node-status">{nodeStatus({ id: node.id, title, task, session: node, relation })}
              {owner && needsDecision(task) && <span className="decision-indicator">Decision waiting</span>}
            </span>
          </button>
          {descendants.length > 0 && <button className="node-disclosure" aria-expanded={!isCollapsed}
            aria-label={(isCollapsed ? "Expand" : "Collapse") + " children of " + title} onClick={() => toggle(node.id)}>
            <Chevron collapsed={isCollapsed} />
          </button>}
        </div>
      </div>
      {!isCollapsed && descendants.length > 0 && <ul className="workflow-children" aria-label={"Children of " + title}>
        {descendants.map((child) => render(child, seen))}
      </ul>}
    </li>;
  };
  return <section className="workflow-space" aria-label="Reported workflow">
    {sessions.length === 0 && taskOnly.length === 0 ? <div className="empty-state">
      <h2>No work reported yet</h2>
      <p>Tasks and native sessions will appear here as they report. Parent connections use reported evidence.</p>
    </div> : <ul className="workflow-roots">
      {lineageRoots(sessions).map((node) => render(node, new Set()))}
      {taskOnly.map((task) => <li className="workflow-branch" key={"task:" + task.id}>
        <div className="node-group">
          <p className="node-relation">Session unreported</p>
          <div className={"workflow-node" + (selected?.task === task.id ? " selected" : "")}>
            <button className="node-select" aria-pressed={selected?.task === task.id}
              onClick={(event) => onSelect({ task: task.id }, event.currentTarget)}>
              <span className="node-role">Task</span><strong>{task.title || task.id}</strong>{task.project && <span className="project-label">{task.project}</span>}
              <span className="node-status">{nodeStatus({ id: task.id, title: task.title, task, relation: "" })}
                {needsDecision(task) && <span className="decision-indicator">Decision waiting</span>}
              </span>
            </button>
          </div>
        </div>
      </li>)}
    </ul>}
  </section>;
}
