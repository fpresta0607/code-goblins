import { request } from "./api";
import { goblinKey } from "./connector-list";
import { parseConnections } from "./connections";
import type { Task } from "./types";

export type Connections = ReturnType<typeof parseConnections>;

// The supervisor checks two goblins' connections at a time, and a check that
// waits for its turn can time out, so the page reads no more than that many
// goblins at once and the rest wait here.
const AT_ONCE = 2;
// How often a check that is still running is read again.
const POLL_MS = 2000;

export interface ConnectorReader {
  // read reads a goblin once for each reason given, such as the end of its
  // latest repair; again reads it whatever was read before; recheck has the
  // supervisor check it anew first.
  read: (task: Task, reason: string) => void;
  again: (task: Task) => void;
  recheck: (task: Task) => void;
  stop: () => void;
}

// connectorReader reads goblins' connections for the Connectors page, each
// until its check has ended and then no more, so a page left open asks
// nothing. A goblin asked for while it is being read is read again once that
// reading ends. onRead is given every reading, and onFailure a read the
// supervisor refused, which ends that goblin's reading until it is asked for
// again.
export function connectorReader(instance: string, onRead: (task: Task, connections: Connections) => void, onFailure: (task: Task, error: unknown, isRecheck: boolean) => void): ConnectorReader {
  const controller = new AbortController();
  const waiting: { task: Task; isRecheck: boolean }[] = [];
  const reading = new Set<string>();
  const reasons = new Map<string, string>();
  const timers = new Set<ReturnType<typeof setTimeout>>();
  const pause = () => new Promise<void>((resolve) => {
    const timer = setTimeout(() => { timers.delete(timer); resolve(); }, POLL_MS);
    timers.add(timer);
  });
  const check = async (task: Task, isRecheck: boolean) => {
    const path = "/api/connections?task=" + encodeURIComponent(task.id) + "&generation=" + encodeURIComponent(task.generation);
    try {
      let connections = parseConnections(isRecheck
        ? await request("/api/connections/check", controller.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ task: task.id, generation: task.generation }) })
        : await request(path, controller.signal));
      if (!controller.signal.aborted) onRead(task, connections);
      while (connections.checking && !controller.signal.aborted) {
        await pause();
        connections = parseConnections(await request(path, controller.signal));
        if (!controller.signal.aborted) onRead(task, connections);
      }
    } catch (error: unknown) {
      if (!controller.signal.aborted) onFailure(task, error, isRecheck);
    }
  };
  const next = () => {
    while (reading.size < AT_ONCE && !controller.signal.aborted) {
      const at = waiting.findIndex((each) => !reading.has(goblinKey(each.task)));
      if (at < 0) return;
      const [{ task, isRecheck }] = waiting.splice(at, 1);
      const key = goblinKey(task);
      reading.add(key);
      void check(task, isRecheck).finally(() => { reading.delete(key); next(); });
    }
  };
  const want = (task: Task, isRecheck: boolean) => {
    const queued = waiting.find((each) => goblinKey(each.task) === goblinKey(task));
    if (queued) queued.isRecheck ||= isRecheck;
    else waiting.push({ task, isRecheck });
    next();
  };
  return {
    read: (task, reason) => {
      if (reasons.get(goblinKey(task)) === reason) return;
      reasons.set(goblinKey(task), reason);
      want(task, false);
    },
    again: (task) => want(task, false),
    recheck: (task) => want(task, true),
    stop: () => { controller.abort(); timers.forEach(clearTimeout); },
  };
}
