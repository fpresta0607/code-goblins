import { array, object, string } from "./types.ts";

// The slash commands one session's harness publishes for itself, read from
// the harness's own record by the supervisor: its command files and skills.
// A harness that publishes none answers none, and the composer shows no list
// rather than guessing one.
export function parseCommands(value: unknown): string[] {
  return array(object(value).commands).map(string).filter((name) => !!name);
}

// slashOptions are the commands to offer while he types the command word at
// the start of the box: none once he has typed a space and gone on to its
// arguments, and those whose name starts with what he has typed.
export function slashOptions(commands: string[], draft: string): string[] {
  if (!draft.startsWith("/")) return [];
  const word = draft.slice(1);
  if (/\s/.test(word)) return [];
  if (!word) return commands.slice(0, 8);
  const prefix = word.toLowerCase();
  return commands.filter((name) => name.toLowerCase().startsWith(prefix)).slice(0, 8);
}
