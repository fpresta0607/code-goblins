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
