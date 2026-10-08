import { useState } from "react";
import type { BoardActivity, Session, Snapshot, Task, TreeNode } from "./types";
import { activityDisplay, EFFECT_MS, playFrom, presentationShownOn, type ActivityEffect } from "./activity";
import { asksOverlord, nodeStatus } from "./workflow";
import { Avatar } from "./Avatar";
import { Chevron } from "./Chevron";
import { personaFor } from "./workflow";
import { lineageRoots, ownsTaskSession, projectSessions, sessionRole, sessionTitle, tasksWithoutSession } from "./lineageTree";
import { goblinName, taskName, withoutHarness } from "./task-words";
import { hasRunningChildren, isHeldByTree, isHelperHeld } from "./fleet-tree";
import { TreeUnder } from "./TreeUnder";

// child is a baby goblin of the task's goblin, by its id in the goblin's
// family tree.
export interface Selection { session?: string; task?: string; child?: string; train?: string }

// childSelection is what opening a goblin's baby goblin selects: a helper
// goblin's own task, since a helper is a goblin of its own, or else the baby
// goblin in its goblin's panel.
export const childSelection = (task: Task, child: TreeNode): Selection =>
  child.kind === "helper" ? { task: child.id.replace(/^helper:/, "") } : { task: task.id, child: child.id };

export function Lineage({ snapshot, project, selected, onSelect, effects, presentations, now }: {
  presentations:BoardActivity[];
  snapshot: Snapshot;
  now: number;
  effects: ActivityEffect[];
  project: string;
  selected: Selection | null;
  onSelect: (selection: Selection, source: HTMLElement) => void;
}) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const sessions = projectSessions(snapshot.sessions.filter((session) => !isHeldByTree(snapshot, session)), snapshot.tasks, project);
  const tasks = snapshot.tasks.filter((task) => !task.archived && (!project || task.project === project));
  const taskOnly = tasksWithoutSession(tasks.filter((task) => !isHelperHeld(snapshot, task.id)), snapshot.sessions);
  const taskOf = (node: Session) => snapshot.tasks.find((task) => task.id === node.task_id);
  // A helper no family tree holds, such as one whose parent is paused, hangs
  // under its parent's own session or, with none reported, its parent's task.
  const hasItem = (id: string) => sessions.some((node) => node.task_id === id && ownsTaskSession(node, taskOf(node))) || taskOnly.some((task) => task.id === id);
  const isHelperItem = (task?: Task) => !!task?.parent && hasItem(task.parent);
  const helperSessions = sessions.filter((node) => ownsTaskSession(node, taskOf(node)) && isHelperItem(taskOf(node)));
  const helperTasks = taskOnly.filter(isHelperItem);
  const byID = new Map(sessions.map((node) => [node.id, node]));
  const children = new Map<string, Session[]>();
  for (const node of sessions) if (node.parent && !helperSessions.includes(node)) {
    const siblings = children.get(node.parent) || [];
    siblings.push(node);
    children.set(node.parent, siblings);
  }
  const toggle = (id: string) => setCollapsed((prior) => {
    const next = new Set(prior);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });
  // helpersOf are the helpers that hang under task id's item.
  const helpersOf = (id: string, ancestors: Set<string>) => [
    ...helperSessions.filter((node) => taskOf(node)?.parent === id).map((node) => render(node, ancestors, true)),
    ...helperTasks.filter((task) => task.parent === id).map((task) => renderTask(task, true)),
  ];
  const render = (node: Session, ancestors: Set<string>, isHelper = false) => {
    if (ancestors.has(node.id)) return null;
    const seen = new Set([...ancestors, node.id]);
    const task = snapshot.tasks.find((task) => task.id === node.task_id);
    const owner = ownsTaskSession(node, task);
    const descendants = [...(children.get(node.id) || []).map((child) => render(child, seen)), ...(owner && task ? helpersOf(task.id, seen) : [])];
    const title = sessionTitle(node, task);
    const activity = activityDisplay(effects,node.id,node.parent);
    const glow = activity.received || activity.created;
    const isCollapsed = collapsed.has(node.id);
    const parent = byID.get(node.parent);
    const relation = isHelper ? "Helper goblin" : node.parent
      ? byID.has(node.parent) ? node.relation || "Reported child"
        : snapshot.retired.includes(node.parent) ? "Retired parent" : "Parent unreported"
      : node.role === "cfo" ? "" : "Parent unknown / unlinked";
    return <li key={node.id} className="workflow-branch">
      {activity.communication && <span key={activity.communication.id} ref={playFrom(activity.communication.expires - EFFECT_MS)} className="compact-communication-pulse" style={{ animationDuration: EFFECT_MS + "ms" }} aria-hidden="true" />}
      {activity.creation && <span key={activity.creation.id} ref={playFrom(activity.creation.expires - EFFECT_MS)} className="compact-creation-highlight" style={{ animationDuration: EFFECT_MS + "ms" }} aria-hidden="true" />}
      <div className="node-group">
        {relation && <p className="node-relation">{relation}{ancestors.size >= 3 && parent && " · Parent: " + sessionTitle(parent, snapshot.tasks.find((task) => task.id === parent.task_id))}</p>}
        <div className={"workflow-node role-" + node.role + (activity.created ? " node-enter" : "") + (selected?.session === node.id ? " selected" : "")}>
          {glow && <span key={glow.id} ref={playFrom(glow.expires - EFFECT_MS)} className="activity-glow" aria-hidden="true" />}
          <button className="node-select" aria-pressed={selected?.session === node.id}
            onClick={(event) => onSelect({ session: node.id }, event.currentTarget)}>
            <Avatar persona={personaFor(task, node)} small /><span className="node-role">{sessionRole(node)}</span>
            <strong>{title}</strong>{presentations.some(a=>presentationShownOn(a,node,task))&&<span className="browser-indicator">Browser active</span>}{task?.project && <span className="project-label">{task.project}</span>}
            {(!owner || task?.goblin_name) && node.role !== "cfo" && task?.title && <span className="node-task">Task: {withoutHarness(task.title)}</span>}
            <span className="node-status">{nodeStatus({ id: node.id, title, task, session: node, relation }, owner && asksOverlord(snapshot, task?.id || ""), snapshot.tasks, snapshot.merge_trains)}</span>
          </button>
          {descendants.length > 0 && <button className="node-disclosure" aria-expanded={!isCollapsed}
            aria-label={(isCollapsed ? "Expand" : "Collapse") + " children of " + title} onClick={() => toggle(node.id)}>
            <Chevron collapsed={isCollapsed} />
          </button>}
        </div>
      </div>
      {owner && task?.tree && hasRunningChildren(task.tree) && <TreeUnder goblin={task} tree={task.tree} title={title} now={now} selected={selected?.task === task.id ? selected.child : undefined} onOpen={(child, source) => onSelect(childSelection(task, child), source)} />}
      {!isCollapsed && descendants.length > 0 && <ul className="workflow-children" aria-label={"Children of " + title}>
        {descendants}
      </ul>}
    </li>;
  };
  const renderTask = (task: Task, isHelper = false) => {
    const title = goblinName(task);
    const helpers = helpersOf(task.id, new Set());
    return <li className="workflow-branch" key={"task:" + task.id}>
      <div className="node-group">
        <p className="node-relation">{isHelper ? "Helper goblin" : "Session unreported"}</p>
        <div className={"workflow-node" + (selected?.task === task.id ? " selected" : "")}>
          <button className="node-select" aria-pressed={selected?.task === task.id}
            onClick={(event) => onSelect({ task: task.id }, event.currentTarget)}>
            <Avatar persona={personaFor(task)} small /><span className="node-role">Task</span><strong>{title}</strong>{presentations.some(a=>presentationShownOn(a,undefined,task))&&<span className="browser-indicator">Browser active</span>}{task.project && <span className="project-label">{task.project}</span>}
            {task.goblin_name && <span className="node-task">Task: {taskName(task)}</span>}
            <span className="node-status">{nodeStatus({ id: task.id, title: task.title, task, relation: "" }, asksOverlord(snapshot, task.id), snapshot.tasks, snapshot.merge_trains)}</span>
          </button>
        </div>
      </div>
      {task.tree && hasRunningChildren(task.tree) && <TreeUnder goblin={task} tree={task.tree} title={title} now={now} selected={selected?.task === task.id ? selected.child : undefined} onOpen={(child, source) => onSelect(childSelection(task, child), source)} />}
      {helpers.length > 0 && <ul className="workflow-children" aria-label={"Children of " + title}>{helpers}</ul>}
    </li>;
  };
  return <section className="workflow-space" aria-label="Reported workflow">
    {sessions.length === 0 && taskOnly.length === 0 ? <div className="empty-state">
      <h2>No work reported yet</h2>
      <p>Tasks and native sessions will appear here as they report. Parent connections use reported evidence.</p>
    </div> : <ul className="workflow-roots">
      {lineageRoots(sessions).filter((node) => !helperSessions.includes(node)).map((node) => render(node, new Set()))}
      {taskOnly.filter((task) => !helperTasks.includes(task)).map((task) => renderTask(task))}
    </ul>}
  </section>;
}
