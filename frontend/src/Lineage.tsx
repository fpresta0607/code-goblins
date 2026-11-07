import { useState } from "react";
import type { BoardActivity, Session, Snapshot, Task } from "./types";
import { activityDisplay, EFFECT_MS, playFrom, presentationShownOn, type ActivityEffect } from "./activity";
import { CFO_ROOT, isHelperHeld, isInProgress, lineageFamily, nodeStatus } from "./workflow";
import { Avatar } from "./Avatar";
import { Chevron } from "./Chevron";
import { personaFor } from "./workflow";
import { childSelection, lineageRoots, ownsTaskSession, sessionRole, sessionTitle, tasksWithoutSession, taskSessionDetails, type Selection } from "./lineageTree";
import { goblinName, withoutHarness } from "./task-words";
import { hasRunningChildren } from "./fleet-tree";
import { TreeUnder } from "./TreeUnder";
import { cardFacts, TaskCard } from "./TaskCard";
import { CfoCardFacts } from "./cfo-card-facts";
import type { AfkToggleState } from "./useAfkToggle";

export function Lineage({ snapshot, project, selected, onSelect, onOpenCfo, afkToggle, effects, presentations, now, freshNow }: {
  presentations:BoardActivity[];
  snapshot: Snapshot;
  now: number;
  freshNow: number;
  effects: ActivityEffect[];
  project: string;
  selected: Selection | null;
  onSelect: (selection: Selection, source: HTMLElement) => void;
  // onOpenCfo opens the CFO's terminal, from its card's button.
  onOpenCfo: (source: HTMLElement) => void;
  afkToggle: AfkToggleState;
}) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  // The list draws the canvas's family, so a goblin hangs under the CFO here
  // whenever it does there.
  const family = lineageFamily(snapshot, project);
  const sessions = family.sessions;
  const cfoID = family.cfo?.cfo ? CFO_ROOT : family.cfo?.session?.id;
  const tasks = snapshot.tasks.filter((task) => isInProgress(task) && (!project || task.project === project));
  const taskOnly = tasksWithoutSession(tasks.filter((task) => !isHelperHeld(snapshot, task.id)), snapshot.sessions);
  const taskOf = (node: Session) => snapshot.tasks.find((task) => task.id === node.task_id);
  // A helper no family tree holds hangs under its parent's own session or,
  // with none reported, its parent's task.
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
    const descendants = [...(children.get(node.id) || []).map((child) => render(child, seen)), ...(owner && task ? helpersOf(task.id, seen) : []),
      ...(node.id === cfoID ? taskOnly.filter((item) => family.underCfo.has(item.id)).map((item) => renderTask(item)) : [])];
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
        <div className={"workflow-node role-" + node.role + (owner && task ? " has-task" : "") + (activity.created ? " node-enter" : "") + (selected?.session === node.id ? " selected" : "")}>
          {glow && <span key={glow.id} ref={playFrom(glow.expires - EFFECT_MS)} className="activity-glow" aria-hidden="true" />}
          {owner && task ? <TaskCard task={task} {...cardFacts(task, snapshot, presentations)} selected={selected?.session === node.id || selected?.task === task.id} now={now}
            onSelect={(_, source) => onSelect({ task: task.id }, source)} /> : <button className="node-select" aria-pressed={selected?.session === node.id}
            onClick={(event) => onSelect(node.id === CFO_ROOT ? {} : { session: node.id }, event.currentTarget)}>
            <Avatar persona={personaFor(task, node)} small /><span className="node-role">{sessionRole(node)}</span>
            <strong>{title}</strong>{presentations.some(a=>presentationShownOn(a,node,task))&&<span className="browser-indicator">Browser active</span>}{task?.project && <span className="project-label">{task.project}</span>}
            {(!owner || task?.goblin_name) && node.role !== "cfo" && task?.title && <span className="node-task">Task: {withoutHarness(task.title)}</span>}
            <span className="node-status">{nodeStatus(node.id === CFO_ROOT && family.cfo ? family.cfo : { id: node.id, title, task, session: node, relation }, false, snapshot.tasks, snapshot.merge_trains)}</span>
          </button>}
          {node.role === "cfo" && snapshot.cfo_runs && !snapshot.cfo_closed && <div className="node-facts"><CfoCardFacts snapshot={snapshot} now={freshNow} afkToggle={afkToggle} onOpenCfo={onOpenCfo} /></div>}
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
        <p className="node-relation">{isHelper ? "Helper goblin · " : ""}{taskSessionDetails(task)}</p>
        <div className={"workflow-node has-task" + (selected?.task === task.id ? " selected" : "")}>
          <TaskCard task={task} {...cardFacts(task, snapshot, presentations)} selected={selected?.task === task.id} now={now} onSelect={(_, source) => onSelect({ task: task.id }, source)} />
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
      {taskOnly.filter((task) => !helperTasks.includes(task) && !family.underCfo.has(task.id)).map((task) => renderTask(task))}
    </ul>}
  </section>;
}
