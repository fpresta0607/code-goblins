import type { Setup } from "./types.ts";

// startState is the project the first-run page's Start uses, the one picked
// while the folder still holds it or else the folder's only one, and what
// Start still needs, in plain words, or "" when it can start the CFO. Only
// Claude Code can be the CFO today, so its reason blocks Start.
export function startState(setup: Setup, picked: string): { project: string; blocked: string } {
  const project = setup.checkouts.includes(picked) ? picked : setup.checkouts.length === 1 ? setup.checkouts[0] : "";
  const claude = setup.agents.find((agent) => agent.id === "claude");
  const blocked = !setup.projects_root ? "Enter the folder that holds your projects."
    : setup.problem ? setup.problem
      : !project ? "Pick the project the CFO starts in."
        : !claude ? "This board cannot start Claude Code."
          : claude.reason;
  return { project, blocked };
}

// FirstRunChoice is his last say on the first-run page: none, a CFO he
// started that the board has not seen run yet, or the board without a CFO.
// It is one choice, so Start the CFO, which sets none, also drops a start
// that ended before the board saw it run.
export type FirstRunChoice = "" | "started" | "board";

// showsFirstRun is whether the board's root shows the first-run page: whenever
// no CFO runs, which is how the installer and its shortcut open it, unless he
// just started one or chose to see the board without a CFO.
export function showsFirstRun({ cfoRuns, choice }: { cfoRuns: boolean; choice: FirstRunChoice }): boolean {
  return !cfoRuns && choice === "";
}
