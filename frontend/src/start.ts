import type { Disk, Memory, Snapshot, Task } from "./types";

const gigabytes = (bytes: number) => Math.round(bytes / 2 ** 30 * 10) / 10;

// Free memory to one decimal, rounded down, so memory just under a mark never
// reads as the mark itself.
export function freeGigabytes(bytes: number): string {
  return (Math.floor(bytes / 2 ** 30 * 10) / 10).toFixed(1);
}

// A start needs both free memory and free commit (memory plus page file) to
// reach the marks, so the meter shows whichever is shorter, memory on a tie:
// how much of it is free, and out of how much. A machine that reports no
// commit limit has not reported commit, so memory alone decides.
export function tighter(memory: Memory): { isCommit: boolean; free: number; total: number } {
  return memory.commit_limit > 0 && memory.commit_available < memory.available
    ? { isCommit: true, free: memory.commit_available, total: memory.commit_limit }
    : { isCommit: false, free: memory.available, total: memory.total };
}

// The memory meter at the head of Tasks: the CFO starts the next queued task
// once memory reaches the mark, and nothing starts under the floor. The board
// itself starts nothing on its own.
export function meterState(memory: Memory): { tone: "ready" | "waiting" | "under"; text: string } {
  const { isCommit, free } = tighter(memory);
  if (free < memory.floor) return { tone: "under", text: `Under the ${gigabytes(memory.floor)} GB floor: nothing starts until ${isCommit ? "commit" : "memory"} frees.` };
  if (free < memory.next) return { tone: "waiting", text: `The CFO starts the next task at ${gigabytes(memory.next)} GB free.` };
  return { tone: "ready", text: "Enough memory: the CFO starts the next task." };
}

// The memory bar spans twice the mark at which the CFO starts the next task,
// or the machine's memory (or commit limit) if that is less, so the floor and
// the mark sit well apart and a full bar says the next task starts: the fill
// and both marks as percents of the bar.
export function meterScale(memory: Memory): { fill: number; floor: number; next: number } {
  const { free, total } = tighter(memory);
  const span = Math.min(total, 2 * memory.next);
  const percent = (bytes: number) => Math.min(100, Math.max(0, bytes / span * 100));
  return { fill: percent(free), floor: percent(memory.floor), next: percent(memory.next) };
}

// What a start or a resume needs while memory or commit is under the mark,
// naming commit when it is the tighter, or empty when both reach it.
export function memoryBlock(memory: Memory | null): string {
  if (!memory) return "";
  const { isCommit, free } = tighter(memory);
  return free < memory.next ? `${gigabytes(memory.next)} GB ${isCommit ? "of commit " : ""}free to keep the ${gigabytes(memory.floor)} GB floor` : "";
}

// Gigabytes to one decimal, to the nearest, for how much an app or a pool
// holds.
const held = (bytes: number) => (bytes / 2 ** 30).toFixed(1);

// The apps holding the most commit, in one line, while commit is the tighter.
export function holdersLine(memory: Memory): string {
  if (!tighter(memory).isCommit || !memory.holders.length) return "";
  return "Most commit: " + memory.holders.map((holder) => `${holder.name} ${held(holder.commit)} GB`).join(", ");
}

// The kernel's paged pool is under 2 GB on a healthy machine; past 4 GB a
// driver is leaking memory, which only a reboot frees.
const PAGED_POOL_WARNING = 4 * 2 ** 30;

export function poolWarning(memory: Memory): string {
  return memory.paged_pool > PAGED_POOL_WARNING ? `Paged pool ${held(memory.paged_pool)} GB: Windows is holding this in its kernel paged pool, memory no goblin can use; restarting the PC frees it.` : "";
}

// The disk meter under the memory meter: under the floor no goblin or gate
// test run starts, and under the lower mark the CFO is woken.
export function diskState(disk: Disk): { tone: "ready" | "waiting" | "under"; text: string } {
  if (disk.free < disk.wake) return { tone: "under", text: `Under the ${gigabytes(disk.wake)} GB mark: the CFO is woken, and nothing starts until disk frees.` };
  if (disk.free < disk.floor) return { tone: "waiting", text: `Under the ${gigabytes(disk.floor)} GB floor: no goblin or gate test run starts until disk frees.` };
  return { tone: "ready", text: "Enough disk for the next start." };
}

// The disk bar spans twice the floor, or the whole drive if that is less, so
// both marks sit well inside it: the fill and both marks as percents of the
// bar.
export function diskScale(disk: Disk): { fill: number; wake: number; floor: number } {
  const span = Math.min(disk.total || 2 * disk.floor, 2 * disk.floor) || 1;
  const percent = (bytes: number) => Math.min(100, Math.max(0, bytes / span * 100));
  return { fill: percent(disk.free), wake: percent(disk.wake), floor: percent(disk.floor) };
}

// What a start or a resume needs while the disk is under the floor, or empty
// when it is not.
export function diskBlock(disk: Disk | null): string {
  return disk && disk.free < disk.floor ? `${gigabytes(disk.floor)} GB of free disk (${freeGigabytes(disk.free)} GB free)` : "";
}

// Why a queued task's Start cannot run now, or empty when it can. The
// supervisor checks all of it again; this only saves a refused click.
export function startBlock(task: Task, memory: Memory | null, anotherStarting: boolean, disk: Disk | null = null): string {
  if (task.starting) return "Starting";
  if (queueBlock(task)) return queueBlock(task);
  if (memoryBlock(memory)) return "Needs " + memoryBlock(memory);
  if (diskBlock(disk)) return "Needs " + diskBlock(disk);
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
  return memory && memoryBlock(memory) ? `Next at ${gigabytes(memory.next)} GB` : "Next up";
}
