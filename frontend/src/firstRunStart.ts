import type { Setup } from "./types.ts";

export function startState(setup: Setup, picked: string): { agent: string; blocked: string } {
  const chosen = setup.agents.find((agent) => agent.id === (picked || setup.default_agent))
    ?? setup.agents.find((agent) => agent.installed && agent.signed_in)
    ?? setup.agents[0];
  const blocked = setup.problem || (!chosen ? "Run goblins setup to choose your CFO."
    : !chosen.installed || !chosen.signed_in ? (chosen.reason || "Setup needed") + ". Run goblins setup to continue." : "");
  return { agent: chosen?.id ?? "", blocked };
}

export type FirstRunChoice = "" | "started";

export function showsFirstRun({ cfoRuns, choice }: { cfoRuns: boolean; choice: FirstRunChoice }): boolean {
  return !cfoRuns && choice === "";
}
