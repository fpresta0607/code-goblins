import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Board } from "../../src/Board";
import { QueuedTasks } from "../../src/QueuedTasks";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Overlord's crowded column: a goblin with a long title that waits on
// another goblin and has a pull request, beside queued work with a long title,
// a finished task with its pull request and a goblin with its browser active,
// each running Codex, so every card carries its harness mark, under a CFO
// running Claude Code.
const long = "The board's terminal: Ctrl+C copies a selection or interrupts, Ctrl+V pastes (bracketed), and the agents' keys reach the program";
const now = Date.parse("2026-09-30T12:00:00Z");
const task = (fields: Record<string, unknown>) => ({ project: "code-goblins", harness: "codex", model: "gpt-6-astra", effort: "xhigh", verified: false, generation: "g1", since: "2026-09-29T08:00:00Z", ...fields });
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude",
  memory: { available: 5.6e9, total: 32e9, floor: 4e9, next: 5e9 },
  tasks: [
    task({ id: "cg-board-kill-with-a-long-goblin-name", title: "cg-board-kill-with-a-long-goblin-name", phase: "working", activity: "working: gate review" }),
    task({ id: "cg-terminal-input", title: long, phase: "waiting", waiting_on: "cg-board-kill-with-a-long-goblin-name", pr: "https://github.com/fpresta0607/code-goblins/pull/205" }),
    task({ id: "cg-defender-safe-build", title: "cg-defender-safe-build", phase: "waiting", waiting_on: "deploy" }),
    task({ id: "queued-long", title: long, phase: "queued", generation: "", brief: true, since: "2026-09-30T11:00:00Z" }),
    task({ id: "finished:cg-orchestration-pulse", title: "Orchestration pulses end smoothly", phase: "done", archived: true, merged: true, verified: true, generation: "", pr: "https://github.com/fpresta0607/code-goblins/pull/198", branch: "fix/orchestration-pulse-end" }),
  ],
});

// With ?board=<px> the board sits in a board region that wide, as it does
// beside an open panel, which leaves it at least 280 px. With ?queue the queue
// shows in the CFO's panel instead, stacked under the board as on a phone,
// where the app's 8 px margin leaves the pane 16 px narrower than the screen.
// The board is stacked, so each card spans the board's whole width. Escape
// closes the selection and hands focus back to where it was chosen from, as
// the app's panel does.
const search = new URLSearchParams(location.search);
const width = Number(search.get("board")) || undefined;
const presentations = [{ id: "p1", kind: "browser", task_id: "cg-board-kill-with-a-long-goblin-name", generation: "g1", source: "", target: "", state: "active", url: "", at: "", until: "" }];
function Fixture() {
  const [selected, setSelected] = useState<string>();
  const returnFocus = useRef<HTMLElement>(null);
  const select = (chosen: { id: string }, source: HTMLElement) => { returnFocus.current = source; setSelected(chosen.id); };
  useEffect(() => {
    const close = (event: KeyboardEvent) => { if (event.key === "Escape") { setSelected(undefined); returnFocus.current?.focus(); } };
    document.addEventListener("keydown", close);
    return () => document.removeEventListener("keydown", close);
  }, []);
  const board = <Board snapshot={snapshot} layout="stacked" selected={selected} now={now} presentations={presentations} onSelect={select} onTerminal={() => {}} onOpenCfo={() => {}} onOpenCommand={() => {}} onStartCfo={() => {}} cardStart={() => ({ blocked: "", problem: "", onStart: () => {} })} />;
  if (search.has("queue")) return <aside className="context-pane" style={{ width: "calc(100vw - 16px)" }}><div className="panel-content"><section className="cfo-queue" aria-label="Queued tasks">
    <QueuedTasks snapshot={snapshot} selected={selected} now={now} presentations={presentations} cardStart={() => ({ blocked: "", problem: "", onStart: () => {} })} onSelect={select} />
  </section></div></aside>;
  return width ? <main className="canvas-region" style={{ width }}>{board}</main> : board;
}
createRoot(document.getElementById("root")!).render(<Fixture />);
