import type { IconName } from "./Icon";

// How a tool call reads as one row of a reply: its icon, and the verb it is
// told by, as it runs and once it has run. The names are the tools of Claude
// Code, Codex and pi, matched without case, since pi names Claude Code's in
// lower case.
const TOOLS: Record<string, { icon: IconName; running: string; done: string }> = {
  read: { icon: "file", running: "Reading", done: "Read" },
  view_image: { icon: "images", running: "Viewing", done: "Viewed" },
  write: { icon: "edit", running: "Writing", done: "Wrote" },
  edit: { icon: "edit", running: "Editing", done: "Edited" },
  multiedit: { icon: "edit", running: "Editing", done: "Edited" },
  notebookedit: { icon: "edit", running: "Editing", done: "Edited" },
  apply_patch: { icon: "edit", running: "Editing", done: "Edited" },
  bash: { icon: "terminal", running: "Running", done: "Ran" },
  exec: { icon: "terminal", running: "Running", done: "Ran" },
  exec_command: { icon: "terminal", running: "Running", done: "Ran" },
  shell: { icon: "terminal", running: "Running", done: "Ran" },
  powershell: { icon: "terminal", running: "Running", done: "Ran" },
  bashoutput: { icon: "terminal", running: "Reading output", done: "Read output" },
  killshell: { icon: "terminal", running: "Stopping", done: "Stopped" },
  grep: { icon: "search", running: "Searching", done: "Searched" },
  glob: { icon: "search", running: "Finding", done: "Found" },
  find: { icon: "search", running: "Finding", done: "Found" },
  ls: { icon: "folder", running: "Listing", done: "Listed" },
  webfetch: { icon: "external", running: "Fetching", done: "Fetched" },
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

// The language of the command a call to tool ran, which names its card when
// the row opens to the whole command, or none for a tool that runs none.
const COMMAND_LANGUAGES: Record<string, string> = { bash: "bash", powershell: "powershell", exec: "shell", exec_command: "shell", shell: "shell" };

export const commandLanguage = (tool: string): string => COMMAND_LANGUAGES[tool.toLowerCase()] ?? "";
