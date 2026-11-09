import type { IconName } from "./Icon";

// How a tool call reads as one row of a reply: its icon, and the verb it is
// told by, as it runs and once it has run. The names are the tools of Claude
// Code, Codex and pi, matched without case, since pi names Claude Code's in
// lower case. A folded run of calls counts each kind by what it was called
// on (counted), such as files or commands, else by times, and with its own
// verb (tally) where the row's verb reads wrong before a count.
const TOOLS: Record<string, { icon: IconName; running: string; done: string; counted?: [string, string]; tally?: string }> = {
  read: { icon: "file", running: "Reading", done: "Read", counted: ["file", "files"] },
  view_image: { icon: "images", running: "Viewing", done: "Viewed", counted: ["image", "images"] },
  write: { icon: "edit", running: "Writing", done: "Wrote", counted: ["file", "files"] },
  edit: { icon: "edit", running: "Editing", done: "Edited", counted: ["file", "files"] },
  multiedit: { icon: "edit", running: "Editing", done: "Edited", counted: ["file", "files"] },
  notebookedit: { icon: "edit", running: "Editing", done: "Edited", counted: ["file", "files"] },
  apply_patch: { icon: "edit", running: "Editing", done: "Edited", counted: ["file", "files"] },
  bash: { icon: "terminal", running: "Running", done: "Ran", counted: ["command", "commands"] },
  exec: { icon: "terminal", running: "Running", done: "Ran", counted: ["command", "commands"] },
  exec_command: { icon: "terminal", running: "Running", done: "Ran", counted: ["command", "commands"] },
  shell: { icon: "terminal", running: "Running", done: "Ran", counted: ["command", "commands"] },
  powershell: { icon: "terminal", running: "Running", done: "Ran", counted: ["command", "commands"] },
  bashoutput: { icon: "terminal", running: "Reading output", done: "Read output" },
  killshell: { icon: "terminal", running: "Stopping", done: "Stopped", counted: ["shell", "shells"] },
  grep: { icon: "search", running: "Searching", done: "Searched" },
  glob: { icon: "search", running: "Finding", done: "Found", tally: "Searched for files" },
  find: { icon: "search", running: "Finding", done: "Found", tally: "Searched for files" },
  ls: { icon: "folder", running: "Listing", done: "Listed", counted: ["folder", "folders"] },
  webfetch: { icon: "external", running: "Fetching", done: "Fetched", counted: ["page", "pages"] },
  websearch: { icon: "search", running: "Searching the web", done: "Searched the web" },
  web_search: { icon: "search", running: "Searching the web", done: "Searched the web" },
  todowrite: { icon: "task", running: "Updating the plan", done: "Updated the plan" },
  update_plan: { icon: "task", running: "Updating the plan", done: "Updated the plan" },
  exitplanmode: { icon: "task", running: "Planning", done: "Planned" },
  skill: { icon: "sparkle", running: "Using a skill", done: "Used a skill" },
};

// callWords is the icon and verb of a call to tool, running or run. An MCP
// tool, mcp__server__tool, is called on its server; any other tool keeps its
// own name.
export function callWords(tool: string, isRunning: boolean): { icon: IconName; verb: string } {
  const known = TOOLS[tool.toLowerCase()];
  if (known) return { icon: known.icon, verb: isRunning ? known.running : known.done };
  const mcp = /^mcp__(.+?)__(.+)$/.exec(tool);
  if (mcp) return { icon: "plug", verb: (isRunning ? "Calling " : "Called ") + mcp[1] + " " + mcp[2] };
  return { icon: "sparkle", verb: tool };
}

// callTally is what a folded run of calls says on its one row: each kind of
// call counted with the verb its rows use, in the order the kinds first ran,
// such as "Read 2 files, ran 3 commands" (#2191). A tool it does not know is
// counted as used.
export function callTally(tools: string[]): string {
  const kinds = new Map<string, { verb: string; counted: [string, string]; count: number }>();
  for (const tool of tools) {
    const known = TOOLS[tool.toLowerCase()];
    const verb = known?.tally ?? (known || tool.startsWith("mcp__") ? callWords(tool, false).verb : "Used " + tool);
    const counted = known?.counted ?? ["time", "times"];
    const kind = kinds.get(verb + " " + counted[0]) ?? { verb, counted, count: 0 };
    kind.count++;
    kinds.set(verb + " " + counted[0], kind);
  }
  return [...kinds.values()].map(({ verb, counted, count }, index) => {
    const phrase = verb + " " + count + " " + counted[count === 1 ? 0 : 1];
    return index ? phrase[0].toLowerCase() + phrase.slice(1) : phrase;
  }).join(", ");
}

// The language of the command a call to tool ran, which names its card when
// the row opens to the whole command, or none for a tool that runs none.
const COMMAND_LANGUAGES: Record<string, string> = { bash: "bash", powershell: "powershell", exec: "javascript", exec_command: "shell", shell: "shell" };

export const commandLanguage = (tool: string): string => COMMAND_LANGUAGES[tool.toLowerCase()] ?? "";
