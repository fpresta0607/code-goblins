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
