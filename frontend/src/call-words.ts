import { connectorName, toolWords } from "./connector-names.ts";
import type { IconName } from "./Icon.tsx";

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
  taskcreate: { icon: "task", running: "Adding a task", done: "Added a task", counted: ["task", "tasks"], tally: "Added" },
  taskupdate: { icon: "task", running: "Updating a task", done: "Updated a task", tally: "Updated tasks" },
  exitplanmode: { icon: "task", running: "Planning", done: "Planned" },
  skill: { icon: "sparkle", running: "Using a skill", done: "Used a skill" },
  toolsearch: { icon: "wrench", running: "Searching tools", done: "Searched tools", tally: "Loaded tools" },
};

// A connector's tool as its harness names it: mcp__server__tool.
const CONNECTOR_TOOL = /^mcp__(.+?)__(.+)$/;

// A subject the supervisor cut ends in three dots (fleettree's bounded).
const CUT = "...";

// A call's words: its icon and the verb it is told by. A connector's call
// also names its connector, whose logo its row draws, and its tool in words
// (action). A tool search that loaded tools by name lists them (loaded),
// each as its own row would name it.
export interface CallWords { icon: IconName; verb: string; connector?: string; action?: string; loaded?: string[] }

// callWords is how a call to tool reads, running or run. A connector's tool,
// mcp__server__tool, reads as its connector by name and the tool in words,
// as Supabase and List projects, the same running and run, since its row's
// mark says which. A tool search that loaded tools by name, as its query
// (subject) select:first,second names them, says how many, unless the
// supervisor cut the query, which then leaves out the name it broke off; a
// search by words says it searched. Any other tool keeps its own name.
export function callWords(tool: string, isRunning: boolean, subject = ""): CallWords {
  const key = tool.toLowerCase();
  const selected = key === "toolsearch" ? /^select:(.*)$/s.exec(subject.trim()) : null;
  if (selected) {
    const isCut = selected[1].endsWith(CUT);
    const names = selected[1].split(",").map((name) => name.trim()).filter(Boolean).slice(0, isCut ? -1 : undefined);
    const loaded = names.map((name) => {
      const words = callWords(name, false);
      return words.action ? words.verb + " · " + words.action : name;
    });
    return { icon: "wrench", verb: (isRunning ? "Loading " : "Loaded ") + (isCut ? "tools" : loaded.length + (loaded.length === 1 ? " tool" : " tools")), loaded };
  }
  const known = TOOLS[key];
  if (known) return { icon: known.icon, verb: isRunning ? known.running : known.done };
  const connector = CONNECTOR_TOOL.exec(tool);
  if (connector) {
    const name = connectorName(connector[1]);
    return { icon: "plug", verb: name, connector: name, action: toolWords(connector[2], name) };
  }
  return { icon: "sparkle", verb: tool };
}

// callTally is what a folded run of calls says on its one row: each kind of
// call counted with the verb its rows use, in the order the kinds first ran,
// such as "Read 2 files, ran 3 commands" (#2191). A connector's calls are
// counted together, as "used Supabase 3 times", and a tool it does not know
// is counted as used.
export function callTally(tools: string[]): string {
  const kinds = new Map<string, { verb: string; counted: [string, string]; count: number }>();
  for (const tool of tools) {
    const known = TOOLS[tool.toLowerCase()];
    const verb = known?.tally ?? (known ? callWords(tool, false).verb : "Used " + (callWords(tool, false).connector ?? tool));
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

// A goblin drives its own browser, the one its Screen view shows, through
// chrome-devtools-axi, so a command that runs it is a browser row (#2362).
const BROWSER_COMMAND = /\bchrome-devtools-axi\b/;

export const isBrowserCommand = (command: string): boolean => BROWSER_COMMAND.test(command);
