import { createRoot } from "react-dom/client";
import { Board } from "../../src/Board";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Overlord's crowded column: a goblin with a long title that waits on
// another goblin and has a pull request, beside queued work with a long title
// and a finished task with its pull request.
const long = "The board's terminal: Ctrl+C copies a selection or interrupts, Ctrl+V pastes (bracketed), and the agents' keys reach the program";
const now = Date.parse("2026-09-30T12:00:00Z");
const task = (fields: Record<string, unknown>) => ({ project: "code-goblins", verified: false, generation: "g1", since: "2026-09-29T08:00:00Z", ...fields });
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", cfo_runs: true,
  memory: { available: 5.6e9, total: 32e9, floor: 4e9, next: 5e9 },
  tasks: [
    task({ id: "cg-board-kill-with-a-long-goblin-name", title: "cg-board-kill-with-a-long-goblin-name", phase: "working", activity: "working: gate review" }),
    task({ id: "cg-terminal-input", title: long, phase: "waiting", waiting_on: "cg-board-kill-with-a-long-goblin-name", pr: "https://github.com/fpresta0607/code-goblins/pull/205" }),
    task({ id: "cg-defender-safe-build", title: "cg-defender-safe-build", phase: "waiting", waiting_on: "deploy" }),
    task({ id: "queued-long", title: long, phase: "queued", generation: "", brief: true, since: "2026-09-30T11:00:00Z" }),
    task({ id: "finished:cg-orchestration-pulse", title: "Orchestration pulses end smoothly", phase: "done", archived: true, merged: true, verified: true, generation: "", pr: "https://github.com/fpresta0607/code-goblins/pull/198", branch: "fix/orchestration-pulse-end" }),
  ],
});

createRoot(document.getElementById("root")!).render(
  <Board snapshot={snapshot} now={now} presentations={[]} onSelect={() => {}} onTerminal={() => {}} onOpenCfo={() => {}} onStartCfo={() => {}} cardStart={() => ({ blocked: "", problem: "", onStart: () => {} })} />,
);
