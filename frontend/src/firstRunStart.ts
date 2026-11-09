import type { Setup } from "./types.ts";

// startState is the agent the first-run page's Start uses and what Start
// still needs, in plain words, or "" when it can start the CEO. The agent is
// the tab he picked, or else the one the quick start remembered, or else the
// one the supervisor recommends, so the board never asks again what the
// terminal was told.
// Start needs no project and no projects folder: the CEO starts in its home.
// Only a folder he looked at that offers no project holds Start back, since
// Start would record it.
export function startState(setup: Setup, picked: string, lookedAtFolder: boolean): { agent: string; blocked: string } {
  const known = (id: string) => setup.agents.some((agent) => agent.id === id);
  const agent = known(picked) ? picked : known(setup.agent) ? setup.agent : setup.agents.find((each) => each.recommended)?.id ?? "";
  const chosen = setup.agents.find((each) => each.id === agent);
  const blocked = !chosen ? "This board offers no agent to start the CEO as."
    : chosen.reason ? chosen.reason
      : lookedAtFolder ? setup.problem
        : "";
  return { agent, blocked };
}

// FirstRunChoice is his last say on the first-run page: none, an active setup,
// a CEO he started that the board has not seen run yet, or the board without a CEO.
// The board's Start the CEO clears this choice, also dropping a start that
// ended before the board saw it run.
export type FirstRunChoice = "" | "setup" | "started" | "board";

// showsFirstRun is whether the board's root shows the first-run page: whenever
// no CEO runs in a home that has had none, which is how the installer and its
// shortcut open it, unless he just started one or chose to see the board
// without a CEO. A home with a saved harness or whose CEO was closed stays,
// with Reopen on the CEO's bar. An active setup keeps its selection and error
// until it succeeds or he leaves, even while Start saves and rolls back a choice.
export function showsFirstRun({ ceoRuns, ceoClosed, ceoConfigured, choice }: { ceoRuns: boolean; ceoClosed: boolean; ceoConfigured: boolean; choice: FirstRunChoice }): boolean {
  return choice === "setup" || !ceoRuns && !ceoClosed && !ceoConfigured && choice === "";
}
