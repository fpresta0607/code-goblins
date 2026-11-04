import { array, object, string } from "./types.ts";

// A slash command one session's harness offers, as the supervisor reads it
// (GET /api/commands): its name, what it does, whether it is built into the
// harness or one of the session's own commands or skills, and the sigil the
// harness takes it behind: "/" for a command, "$" for a Codex skill, which
// Codex takes as a mention.
export interface HarnessCommand { name: string; description: string; source: "builtin" | "command" | "skill"; sigil: "/" | "$" }

const SOURCES = ["builtin", "command", "skill"] as const;
const SIGILS = ["/", "$"] as const;

// parseCommands reads the supervisor's answer. A harness that offers none
// answers none, and the composer shows no list rather than guessing one.
export function parseCommands(value: unknown): HarnessCommand[] {
  return array(object(value).commands).map((item) => {
    const each = object(item);
    const source = SOURCES.find((known) => known === each.source);
    if (!source) throw new Error("Invalid command source");
    const sigil = SIGILS.find((known) => known === each.sigil);
    if (!sigil) throw new Error("Invalid command sigil");
    return { name: string(each.name), description: string(each.description), source, sigil };
  }).filter((command) => !!command.name);
}

// slashOptions are the commands to offer while he types the command word at
// the start of the box, none once he has typed a space and gone on to its
// arguments: after a / every command and skill the harness takes, and after
// a $ the skills Codex takes as mentions. The one named what he typed comes
// first, then those whose name starts with it, then those whose name holds
// it, then those whose description does, each group in the harness's order.
export function slashOptions(commands: HarnessCommand[], draft: string): HarnessCommand[] {
  const sigil = SIGILS.find((known) => draft.startsWith(known));
  if (!sigil) return [];
  const word = draft.slice(1).toLowerCase();
  if (/\s/.test(word)) return [];
  const offered = sigil === "/" ? commands : commands.filter((command) => command.sigil === sigil);
  const named = offered.filter((command) => command.name.toLowerCase().startsWith(word));
  const starts = [...named.filter((command) => command.name.toLowerCase() === word), ...named.filter((command) => command.name.toLowerCase() !== word)];
  const holds = offered.filter((command) => !starts.includes(command) && command.name.toLowerCase().includes(word));
  const describes = offered.filter((command) => !starts.includes(command) && !holds.includes(command) && command.description.toLowerCase().includes(word));
  return [...starts, ...holds, ...describes];
}

// sendable is a message as its harness sends it whole on Enter. Codex keeps
// its own list of skills open while the text ends in a $ word, and Enter then
// completes that word instead of sending the message, so a message to Codex
// that ends in one ends in a space.
export function sendable(harness: string, text: string): string {
  return harness === "codex" && /(^|\s)\$\S*$/.test(text) ? text + " " : text;
}
