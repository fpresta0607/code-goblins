import type { Memory, Snapshot, Task } from "./types";

const gigabytes = (bytes: number) => Math.round(bytes / 2 ** 30 * 10) / 10;

// Free memory to one decimal, rounded down, so memory just under a mark never
// reads as the mark itself.
export function freeGigabytes(bytes: number): string {
  return (Math.floor(bytes / 2 ** 30 * 10) / 10).toFixed(1);
}

// The memory meter at the head of Tasks: the CFO starts the next queued task
// once memory reaches the mark, and nothing starts under the floor. The board
// itself starts nothing on its own.
export function meterState(memory: Memory): { tone: "ready" | "waiting" | "under"; text: string } {
  if (memory.available < memory.floor) return { tone: "under", text: `Under the ${gigabytes(memory.floor)} GB floor: nothing starts until memory frees.` };
  if (memory.available < memory.next) return { tone: "waiting", text: `The CFO starts the next task at ${gigabytes(memory.next)} GB free.` };
  return { tone: "ready", text: "Enough memory: the CFO starts the next task." };
}

// The memory bar spans twice the mark at which the CFO starts the next task,
// or the machine's memory if that is less, so the floor and the mark sit well
// apart and a full bar says the next task starts: the fill and both marks as
// percents of the bar.
export function meterScale(memory: Memory): { fill: number; floor: number; next: number } {
  const span = Math.min(memory.total, 2 * memory.next);
  const percent = (bytes: number) => Math.min(100, Math.max(0, bytes / span * 100));
  return { fill: percent(memory.available), floor: percent(memory.floor), next: percent(memory.next) };
}

// Why a queued task's Start cannot run now, or empty when it can. The
// supervisor checks all of it again; this only saves a refused click.
export function startBlock(task: Task, memory: Memory | null, anotherStarting: boolean): string {
  if (task.starting) return "Starting";
  if (queueBlock(task)) return queueBlock(task);
  if (memory && memory.available < memory.next) return `Needs ${gigabytes(memory.next)} GB free to keep the ${gigabytes(memory.floor)} GB floor`;
  if (anotherStarting) return "Another task is starting";
  return "";
}

export function queueBlock(task: Task): string {
  return task.phase === "queued" && task.dependencies.length ? task.reason || "Waiting on " + task.dependencies.join(", ") : "";
}

// A Start's refusal on its card, the snapshot revision it arrived at, and
// whether its cause passes by itself where the board sees it, as memory under
// the floor or another Start running do.
export interface Refusal { reason: string; revision: number; passing: boolean }

// Whether a refusal still stands: a passing one lapses once a newer snapshot
// shows its task's Start no longer blocked, as once the task that was starting
// is up; any other stays until its Start is pressed again.
export function refusalStands(refusal: Refusal, snapshot: Snapshot, blocked: string): boolean {
  return !refusal.passing || snapshot.revision <= refusal.revision || blocked !== "";
}

// A Start the supervisor accepted, and the revision from which every snapshot
// shows it rather than a failure of the task's last Start.
export interface AcceptedStart { id: string; revision: number }

// What the board does about an accepted Start: open the goblin's terminal once
// its session is up, stop waiting once a snapshot of this start shows it
// failed, and otherwise wait, as for an older snapshot that still carries the
// last Start's failure.
export function startOutcome(accepted: AcceptedStart, snapshot: Snapshot): "open" | "failed" | "wait" {
  const task = snapshot.tasks.find((candidate) => candidate.id === accepted.id);
  if (task?.generation) return "open";
  if (task?.start_error && snapshot.revision >= accepted.revision) return "failed";
  return "wait";
}

// The chip on the top queued task, the one the CFO starts next.
export function nextChip(memory: Memory | null): string {
  return memory && memory.available < memory.next ? `Next, at ${gigabytes(memory.next)} GB free` : "Next up";
}
