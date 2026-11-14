import { useCallback, useEffect, useRef, useState } from "react";
import type { BoardActivity, Session, Snapshot, Task } from "./types";
import { activityDisplay, EFFECT_MS, playFrom, presentationShownOn, presentedPage, type ActivityEffect } from "./activity";
import { BrowserActive } from "./browser-active";
import { CEO_ROOT, isHelperHeld, isInProgress, lineageFamily, nodeStatus } from "./workflow";
import { Avatar } from "./Avatar";
import { Chevron } from "./Chevron";
import { personaFor } from "./workflow";
import { childSelection, lineageRoots, ownsTaskSession, sessionRole, sessionTitle, tasksWithoutSession, type Selection } from "./lineageTree";
import { goblinName, withoutHarness } from "./task-words";
import { hasRunningChildren } from "./fleet-tree";
import { TreeUnder } from "./TreeUnder";
import { cardFacts, TaskCard } from "./TaskCard";
import { CeoCardFacts } from "./ceo-card-facts";
import type { AfkToggleState } from "./useAfkToggle";

export function Lineage({ snapshot, project, selected, onSelect, onOpenCeo, afkToggle, effects, presentations, now, freshNow }: {
  presentations:BoardActivity[];
  snapshot: Snapshot;
  now: number;
  freshNow: number;
  effects: ActivityEffect[];
  project: string;
  selected: Selection | null;
  onSelect: (selection: Selection, source: HTMLElement) => void;
  // onOpenCeo opens the CEO's terminal, from its card's button.
  onOpenCeo: (source: HTMLElement) => void;
  afkToggle: AfkToggleState;
}) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  // A goblin's card is drawn again only when what it shows changed
  // (TaskCard), so the handler it is given stays the same between renders
  // and calls the latest onSelect.
  const latestSelect = useRef(onSelect);
  useEffect(() => { latestSelect.current = onSelect; });
  const selectTask = useCallback((task: Task, source: HTMLElement) => latestSelect.current({ task: task.id }, source), []);
  // The list draws the canvas's family, so a goblin hangs under the CEO here
  // whenever it does there.
  const family = lineageFamily(snapshot, project);
  const sessions = family.sessions;
  const ceoID = family.ceo?.ceo ? CEO_ROOT : family.ceo?.session?.id;
  const tasks = snapshot.tasks.filter((task) => isInProgress(task) && (!project || task.project === project));
  const taskOnly = tasksWithoutSession(tasks.filter((task) => !isHelperHeld(snapshot, task.id)), snapshot.sessions);
  const taskOf = (node: Session) => snapshot.tasks.find((task) => task.id === node.task_id);
  // A helper no family tree holds hangs under its parent's own session or,
  // with none reported, its parent's task.
  const hasItem = (id: string) => sessions.some((node) => node.task_id === id && ownsTaskSession(node, taskOf(node))) || taskOnly.some((task) => task.id === id);
  const isHelperItem = (task?: Task) => !!task?.parent && hasItem(task.parent);
  const helperSessions = sessions.filter((node) => ownsTaskSession(node, taskOf(node)) && isHelperItem(taskOf(node)));
  const helperTasks = taskOnly.filter(isHelperItem);
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
    ...helperSessions.filter((node) => taskOf(node)?.parent === id).map((node) => render(node, ancestors)),
    ...helperTasks.filter((task) => task.parent === id).map((task) => renderTask(task)),
  ];
  const render = (node: Session, ancestors: Set<string>) => {
    if (ancestors.has(node.id)) return null;
    const seen = new Set([...ancestors, node.id]);
    const task = snapshot.tasks.find((task) => task.id === node.task_id);
    const owner = ownsTaskSession(node, task);
    const descendants = [...(children.get(node.id) || []).map((child) => render(child, seen)), ...(owner && task ? helpersOf(task.id, seen) : []),
      ...(node.id === ceoID ? taskOnly.filter((item) => family.underCeo.has(item.id)).map((item) => renderTask(item)) : [])];
    const title = sessionTitle(node, task);
    const activity = activityDisplay(effects,node.id,node.parent);
    const glow = activity.received || activity.created;
    const isCollapsed = collapsed.has(node.id);
    const page = owner && task ? "" : presentedPage(presentations, (presentation) => presentationShownOn(presentation, node, task));
    const hasCeoFacts = node.role === "ceo" && snapshot.ceo_runs && !snapshot.ceo_closed;
    return <li key={node.id} className="workflow-branch">
      {activity.communication && <span key={activity.communication.id} ref={playFrom(activity.communication.expires - EFFECT_MS)} className="compact-communication-pulse" style={{ animationDuration: EFFECT_MS + "ms" }} aria-hidden="true" />}
      {activity.creation && <span key={activity.creation.id} ref={playFrom(activity.creation.expires - EFFECT_MS)} className="compact-creation-highlight" style={{ animationDuration: EFFECT_MS + "ms" }} aria-hidden="true" />}
      <div className="node-group">
        <div className={"workflow-node role-" + node.role + (owner && task ? " has-task" : "") + (activity.created ? " node-enter" : "") + (selected?.session === node.id ? " selected" : "")}>
          {glow && <span key={glow.id} ref={playFrom(glow.expires - EFFECT_MS)} className="activity-glow" aria-hidden="true" />}
          {owner && task ? <TaskCard task={task} {...cardFacts(task, snapshot, presentations)} selected={selected?.session === node.id || selected?.task === task.id} now={now}
            onSelect={selectTask} /> : <button className="node-select" aria-pressed={selected?.session === node.id}
            onClick={(event) => onSelect(node.id === CEO_ROOT ? {} : { session: node.id }, event.currentTarget)}>
            <Avatar persona={personaFor(task, node)} small /><span className="node-role">{sessionRole(node)}</span>
            <strong>{title}</strong>{task?.project && <span className="project-label">{task.project}</span>}
            {(!owner || task?.goblin_name) && node.role !== "ceo" && task?.title && <span className="node-task">Task: {withoutHarness(task.title)}</span>}
            <span className="node-status">{nodeStatus(node.id === CEO_ROOT && family.ceo ? family.ceo : { id: node.id, title, task, session: node, relation: node.relation }, false, snapshot.tasks, snapshot.merge_trains)}</span>
          </button>}
          {(page || hasCeoFacts) && <div className="node-facts"><BrowserActive name={title} screen="" page={page} />{hasCeoFacts && <CeoCardFacts snapshot={snapshot} now={freshNow} afkToggle={afkToggle} onOpenCeo={onOpenCeo} />}</div>}
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
  const renderTask = (task: Task) => {
    const title = goblinName(task);
    const helpers = helpersOf(task.id, new Set());
    return <li className="workflow-branch" key={"task:" + task.id}>
      <div className="node-group">
        <div className={"workflow-node has-task" + (selected?.task === task.id ? " selected" : "")}>
          <TaskCard task={task} {...cardFacts(task, snapshot, presentations)} selected={selected?.task === task.id} now={now} onSelect={selectTask} />
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
      {taskOnly.filter((task) => !helperTasks.includes(task) && !family.underCeo.has(task.id)).map((task) => renderTask(task))}
    </ul>}
  </section>;
}
