import type { Memory, Task } from "./types";

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

// Why a queued task's Start cannot run now, or empty when it can. The
// supervisor checks all of it again; this only saves a refused click.
export function startBlock(task: Task, memory: Memory | null, anotherStarting: boolean): string {
  if (task.starting) return "Starting";
  if (!task.brief) return "No brief yet: the CFO writes one before it can start";
  if (memory && memory.available < memory.floor) return `Under the ${gigabytes(memory.floor)} GB memory floor`;
  if (anotherStarting) return "Another task is starting";
  return "";
}

// The chip on the top queued task, the one the CFO starts next.
export function nextChip(memory: Memory | null): string {
  return memory && memory.available < memory.next ? `Next, at ${gigabytes(memory.next)} GB free` : "Next up";
}
