import type { PauseCondition, Task } from "./types.ts";

// What the board says about a task, in words a person reads: the goblins and
// the supervisor write for the fleet, with state words, semicolon chains,
// hashes, paths and links, and those records stay as they are. The board
// translates them as it shows them, and keeps the words it changed one click
// away behind Details.

// A backlog row's title, and the note on what it waits for, can end with the
// harness it runs in, as "; Claude Code", which the card's harness mark
// already shows.
const HARNESS_SUFFIX = /;\s*(?:Claude Code|Codex|Pi|Kimi)\s*$/i;

export function withoutHarness(text: string): string {
  return text.replace(HARNESS_SUFFIX, "").trim();
}

// A goblin's report starts with its state, which the status above it says.
const STATE_WORD = /^(?:(?:working|blocked|failed|done|lifecycle-[a-z]+):|waiting on [^\s:]+:|Waiting on the CFO:)\s*/;

export function reportBody(report: string): string {
  const body = report.replace(STATE_WORD, "").trim();
  // A done report names only its pull request, which its badge links.
  return /^PR https:\/\/\S+$/.test(body) ? "" : body;
}

const HEX = "(?=[0-9a-f]*\\d)(?=[0-9a-f]*[a-f])[0-9a-f]{7,40}";
// A commit hash goes with the word that points at it, and one alone in
// brackets with its brackets; one inside a file name stays in the name.
const HASH = new RegExp(`(?:\\s+(?:at|to|from|as|of))?\\s+${HEX}(?![\\w-]|\\.\\w)|\\s*\\(${HEX}\\)`, "g");
const PULL_REQUEST = /https:\/\/github\.com\/[^\s/]+\/[^\s/]+\/pull\/(\d+)[^\s)]*/g;
const RUN = /https:\/\/github\.com\/[^\s/]+\/[^\s/]+\/actions\/runs\/[^\s)]*/g;
const LINK = /https?:\/\/([^\s/)]+)[^\s)]*/g;
// A Windows path, or a path of at least two slashes or one ending in a file
// name; a date or a branch name is neither.
const WINDOWS_PATH = /\b[A-Za-z]:\\[^\s;,)"']*/g;
const SLASH_PATH = /(?<![\w:/.-])(?:\.{1,2}\/|\/)?(?:[\w.-]+\/){2,}[\w.-]*|(?<![\w:/.-])[\w.-]+\/[\w-]+\.[A-Za-z]\w{0,4}\b/g;

// lastName is the name a path ends with, keeping a sentence's full stop out
// of it; a date written with slashes has no letter and stays as it is.
function lastName(path: string): string {
  if (!/[A-Za-z]/.test(path)) return path;
  const stop = /\.$/.test(path) ? "." : "";
  const name = path.slice(0, path.length - stop.length).split(/[\\/]/).filter(Boolean).at(-1) || "";
  return name + stop;
}

function tidy(text: string): string {
  return text.replace(/\s+options:\s[\s\S]*$/, "")
    .replace(PULL_REQUEST, "PR #$1").replace(RUN, "a CI run").replace(LINK, "$1")
    .replace(WINDOWS_PATH, lastName).replace(SLASH_PATH, lastName)
    .replace(HASH, "")
    .replace(/\s+/g, " ").replace(/\(\s*\)/g, "").replace(/\(\s+/g, "(").replace(/\s+\)/g, ")").replace(/\s+([,.;:!?])/g, "$1").trim();
}

// A sentence starts with a capital unless its first word is a name, such as
// a task id or a file name, and ends with its punctuation.
function sentence(text: string): string {
  const trimmed = text.trim().replace(/[,;:]$/, "");
  if (!trimmed) return "";
  const cased = /^[a-z]+(?:\s|$)/.test(trimmed) ? trimmed[0].toUpperCase() + trimmed.slice(1) : trimmed;
  return /[.!?…]$/.test(cased) ? cased : cased + ".";
}

// plainText is raw text in sentence case, one sentence where a semicolon
// chained two, with no hash, path or link in its prose.
export function plainText(raw: string): string {
  return tidy(raw).split(/;\s+/).map(sentence).filter(Boolean).join(" ");
}

// summary is a text's whole sentences up to limit characters, and at least
// its first, which a runaway one has cut at a word.
export function summary(text: string, limit = 280): string {
  let kept = "";
  for (const part of text.split(/(?<=[.!?])\s+/)) {
    const next = kept ? kept + " " + part : part;
    if (next.length > limit) {
      if (kept) break;
      return part.slice(0, part.lastIndexOf(" ", limit - 1)).replace(/[,;:]$/, "") + "…";
    }
    kept = next;
  }
  return kept;
}

// A list item is a short label: a path, a hash, a process id and anything
// after a semicolon are left to Details.
export function listItem(item: string): string {
  const label = item.split(";")[0].replace(WINDOWS_PATH, "").replace(SLASH_PATH, "").replace(/\bpid \d+\b/g, "");
  return sentence(tidy(label)).replace(/\.$/, "");
}

// Windows can take a while to end a stopped goblin's programs; the sentence
// names them, never their process ids.
export function teardownSentence(labels: string[]): string {
  const names = [...new Set(labels.map((label) => label.replace(/\s+pid \d+$/, "")))];
  if (!names.length) return "";
  return "Windows is still closing " + (names.length === 1 ? names[0] : names.slice(0, -1).join(", ") + " and " + names.at(-1)) + ".";
}

// A title read inside a sentence starts with a small article.
const inSentence = (title: string) => title.replace(/^(?:A|An|The)\b/, (article) => article.toLowerCase());

function when(at: string): string {
  const date = new Date(at);
  return Number.isFinite(date.getTime()) ? date.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }) : "its set time";
}

// What resumes a paused goblin, from its pause's condition.
function resumes(pause: PauseCondition | undefined, tasks: Task[]): string {
  const byItself = "It resumes by itself ";
  const [kind, target] = pause?.until.split(/:(.*)/s) || [];
  switch (pause?.reason) {
    case "memory": return byItself + "once 5 GB of memory is free.";
    case "allowance": return byItself + "when the allowance resets, " + when(pause.until) + ".";
    case "question": return byItself + "when you answer its question.";
    case "ci": return byItself + "when its CI run finishes.";
    case "deploy": return byItself + "when its deploy finishes.";
    case "dependency": {
      if (kind === "pr") return byItself + "when " + plainText(target).replace(/\.$/, "") + " merges.";
      if (kind === "date") return byItself + "on " + when(target) + ".";
      const awaited = tasks.find((candidate) => candidate.id === target);
      return byItself + "when " + (awaited ? inSentence(withoutHarness(awaited.title) || awaited.id) : "the task it waits on") + " finishes.";
    }
  }
  return "It stays paused until you resume it.";
}

const FAILED_ACTION: Record<string, string> = {
  pause: "The pause did not finish, so the goblin is not paused. Its work is kept. Try Pause again.",
  resume: "The goblin did not start again. Its work is kept. Try Resume again.",
  stop: "The stop did not finish. Its work is kept. Try Stop again.",
};

// The same words, give or take a capital and a full stop.
const same = (one: string, other: string) => {
  const bare = (text: string) => text.replace(/\s+/g, " ").replace(/[.!?]$/, "").trim().toLowerCase();
  return bare(one) === bare(other);
};

// reportSaid is a goblin's report as the panel says it, and the report as
// written when the sentence leaves anything out.
export function reportSaid(report: string): { sentence: string; details: string[] } {
  const body = reportBody(report);
  const sentence = summary(plainText(body));
  return { sentence, details: same(sentence, body) ? [] : [report] };
}

export interface Summary {
  // sentence is what the panel says under the task's status.
  sentence: string;
  // details are the raw words behind Details, empty when the sentence
  // already says them.
  details: string[];
  // isFailure marks a failure, which links to its log.
  isFailure: boolean;
}

// taskSummary is the one sentence the panel says under a task's status, which
// never repeats the status: why it failed and what to do, what resumes it, or
// its goblin's latest report.
export function taskSummary(task: Task, tasks: Task[]): Summary {
  const record = task.lifecycle;
  const teardown = teardownSentence(task.teardown);
  const join = (...sentences: string[]) => sentences.filter(Boolean).join(" ");
  if (record?.phase === "failed" && FAILED_ACTION[record.action]) return { sentence: join(FAILED_ACTION[record.action], teardown), details: [...record.problems, ...task.teardown], isFailure: true };
  if (task.phase === "paused") return { sentence: join(resumes(record?.pause, tasks), record && !record.handoff_saved ? "The goblin's last saved notes are kept." : "", teardown), details: [...(record?.problems || []), ...task.teardown], isFailure: false };
  if (["pausing", "resuming", "stopping", "stopped"].includes(task.phase)) return { sentence: teardown, details: task.teardown, isFailure: false };
  // A queued task's status says it all; the Overlord wants no wait line.
  if (task.phase === "queued") return { sentence: "", details: [], isFailure: false };
  // A failed or blocked task with no report of its own says its evidence.
  const said = reportSaid(reportBody(task.activity) || !["failed", "blocked"].includes(task.phase) ? task.activity : task.reason);
  return { sentence: join(said.sentence, teardown), details: [...said.details, ...task.teardown], isFailure: task.phase === "failed" };
}
