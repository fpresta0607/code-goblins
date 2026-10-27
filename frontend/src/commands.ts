import { array, object, string } from "./types.ts";

// A slash command one session's harness offers, as the supervisor reads it
// (GET /api/commands): its name, what it does, and whether it is built into
// the harness or one of the session's own commands or skills.
export interface HarnessCommand { name: string; description: string; source: "builtin" | "command" | "skill" }

const SOURCES = ["builtin", "command", "skill"] as const;

// parseCommands reads the supervisor's answer. A harness that offers none
// answers none, and the composer shows no list rather than guessing one.
export function parseCommands(value: unknown): HarnessCommand[] {
  return array(object(value).commands).map((item) => {
    const each = object(item);
    const source = SOURCES.find((known) => known === each.source);
    if (!source) throw new Error("Invalid command source");
    return { name: string(each.name), description: string(each.description), source };
  }).filter((command) => !!command.name);
}

// slashOptions are the commands to offer while he types the command word at
// the start of the box, none once he has typed a space and gone on to its
// arguments: the one named what he typed, then those whose name starts with
// it, then those whose name holds it, then those whose description does,
// each group in the harness's order.
export function slashOptions(commands: HarnessCommand[], draft: string): HarnessCommand[] {
  if (!draft.startsWith("/")) return [];
  const word = draft.slice(1).toLowerCase();
  if (/\s/.test(word)) return [];
  const named = commands.filter((command) => command.name.toLowerCase().startsWith(word));
  const starts = [...named.filter((command) => command.name.toLowerCase() === word), ...named.filter((command) => command.name.toLowerCase() !== word)];
  const holds = commands.filter((command) => !starts.includes(command) && command.name.toLowerCase().includes(word));
  const describes = commands.filter((command) => !starts.includes(command) && !holds.includes(command) && command.description.toLowerCase().includes(word));
  return [...starts, ...holds, ...describes];
}
