import type { Session, Task } from "./types.ts";

export function ownsTaskSession(node?: Session, task?: Task): boolean {
  return (
    !node ||
    (node.role === "goblin" &&
      node.id === task?.session &&
      node.generation === task.generation)
  );
}

export function sessionModel(node?: Session, task?: Task): string {
  return (
    node?.model ||
    (ownsTaskSession(node, task) ? task?.model : "") ||
    "Model unreported"
  );
}

export function sessionRole(node: Session): string {
  const roles: Record<string, string> = {
    cfo: "Supervisor", goblin: "Goblin", worker: "Background worker", subagent: "Subagent",
  };
  return roles[node.role] || "Agent session";
}

export function sessionTitle(node: Session, task?: Task): string {
  if (node.role === "cfo") return "CFO";
  if (node.role === "goblin") return task?.title || node.task_id || node.native_id;
  return node.agent_type || node.native_id || sessionRole(node);
}

export function tasksWithoutSession(tasks: Task[], sessions: Session[]): Task[] {
  return tasks.filter((task) => !sessions.some((node) => ownsTaskSession(node, task)));
}

// Project filtering retains reported relatives, never inferred directory/time links.
export function projectSessions(sessions: Session[], tasks: Task[], project: string): Session[] {
  if (!project) return sessions;
  const taskIDs = new Set(tasks.filter((task) => task.project === project).map((task) => task.id));
  const included = new Set(sessions.filter((node) => taskIDs.has(node.task_id)).map((node) => node.id));
  // Include descendants of matching task sessions before adding their ancestors.
  // Adding descendants of the CFO ancestor would pull in unrelated projects.
  for (let changed = true; changed;) {
    changed = false;
    for (const node of sessions) if (!included.has(node.id) && included.has(node.parent)) {
      included.add(node.id);
      changed = true;
    }
  }
  const byID = new Map(sessions.map((node) => [node.id, node]));
  for (const id of [...included]) {
    const visited = new Set<string>();
    let parent = byID.get(id)?.parent;
    while (parent && byID.has(parent) && !visited.has(parent)) {
      visited.add(parent);
      included.add(parent);
      parent = byID.get(parent)?.parent;
    }
  }
  return sessions.filter((node) => included.has(node.id));
}

export function lineageRoots(sessions: Session[]): Session[] {
  const byID = new Map(sessions.map((node) => [node.id, node]));
  const roots = sessions.filter(
    (node) => !node.parent || !byID.has(node.parent),
  );
  const reached = new Set<string>();
  const visit = (id: string) => {
    if (reached.has(id)) return;
    reached.add(id);
    sessions
      .filter((node) => node.parent === id)
      .forEach((node) => visit(node.id));
  };
  roots.forEach((node) => visit(node.id));
  // Invalid replayed cycles must remain visible without recursive rendering.
  for (const node of sessions)
    if (!reached.has(node.id)) {
      roots.push(node);
      visit(node.id);
    }
  return roots;
}
