import { array, boolean, number, object, string, type Snapshot, type Task } from "./types.ts";
import { sessionEnd } from "./session-end.ts";
import { MAX_FONT_SIZE, MIN_FONT_SIZE } from "./terminalStream.ts";

// A Claude Code session read as messages, as the supervisor serves them at
// /api/messages (internal/fleettree/messages.go): a prompt, a reply, a
// compaction or an interrupt, each with an id that stays the same for its
// life and its seq, its place in the session.

export interface MessagePicture { id: string; media_type: string; bytes: number }

// A block of a reply: its text, thinking Claude Code recorded the words of,
// or a tool call with its id, its outcome, what came back, an edit's counts and diff,
// the sub-agent it started and the plan it wrote.
export interface MessageBlock {
  kind: "text" | "thinking" | "call";
  text: string; call_id: string; tool: string; subject: string; input: string; state: CallState; result: string; spilled: number;
  pictures: MessagePicture[]; edit: { added: number; removed: number; diff: string[] } | null; agent: { node: string; type: string } | null;
  plan: PlanStep[];
}
// A step of a plan a call wrote, Claude Code's todo list or Codex's plan.
export type PlanStatus = "pending" | "in_progress" | "completed";
export interface PlanStep { text: string; status: PlanStatus }
export type CallState = "running" | "done" | "failed" | "denied" | "interrupted" | "";

export interface SessionMessage {
  id: string; seq: number; kind: "prompt" | "reply" | "compaction" | "interrupt"; at: string;
  source: PromptSource; queued: boolean; text: string; output: string; errors: string; trigger: string;
  blocks: MessageBlock[]; pictures: MessagePicture[];
}
export type PromptSource = "overlord" | "ceo" | "answer" | "page" | "notice" | "slash" | "shell" | "";

const KINDS = ["prompt", "reply", "compaction", "interrupt"] as const;
const BLOCK_KINDS = ["text", "thinking", "call"] as const;
const SOURCES = ["overlord", "ceo", "answer", "page", "notice", "slash", "shell"] as const;
const STATES = ["running", "done", "failed", "denied", "interrupted"] as const;
const PLAN_STATES = ["pending", "in_progress", "completed"] as const;

function choice<T extends string>(value: unknown, choices: readonly T[], what: string): T {
  const found = choices.find((each) => each === value);
  if (!found) throw new Error("Invalid " + what);
  return found;
}

const pictures = (value: unknown): MessagePicture[] => array(value).map((item) => {
  const picture = object(item);
  return { id: string(picture.id), media_type: string(picture.media_type), bytes: number(picture.bytes) };
});

function block(value: unknown): MessageBlock {
  const each = object(value);
  const edit = each.edit == null ? null : object(each.edit);
  const agent = each.agent == null ? null : object(each.agent);
  return {
    kind: choice(each.kind, BLOCK_KINDS, "message block"), text: string(each.text), call_id: string(each.call_id), tool: string(each.tool), subject: string(each.subject), input: string(each.input),
    state: each.state == null ? "" : choice(each.state, STATES, "call state"), result: string(each.result), spilled: number(each.spilled), pictures: pictures(each.pictures),
    edit: edit && { added: number(edit.added), removed: number(edit.removed), diff: array(edit.diff).map(string) },
    agent: agent && { node: string(agent.node), type: string(agent.type) },
    plan: array(each.plan).map((item) => {
      const step = object(item);
      return { text: string(step.text), status: choice(step.status, PLAN_STATES, "plan step") };
    }),
  };
}

export function parseMessages(value: unknown): SessionMessage[] {
  return array(value).map((item) => {
    const each = object(item);
    return {
      id: string(each.id), seq: number(each.seq), kind: choice(each.kind, KINDS, "message"), at: string(each.at),
      source: each.source == null ? "" : choice(each.source, SOURCES, "prompt source"), queued: each.queued == null ? false : boolean(each.queued),
      text: string(each.text), output: string(each.output), errors: string(each.errors), trigger: string(each.trigger),
      blocks: array(each.blocks).map(block), pictures: pictures(each.pictures),
    };
  });
}

// SessionUsage is what a session's own conversation records it spent: its
// input, output and cached tokens, the dollars it named, and the context its
// last request carried against the model's window. A field the conversation
// does not record is 0, and the board leaves that stat out rather than
// inventing it. The model and effort the session runs are no part of it: the
// session's one record names them.
export interface SessionUsage {
  input_tokens: number; output_tokens: number; cache_read_tokens: number;
  cost_usd: number; context_tokens: number; context_window: number;
}

// A prompt its harness recorded and holds for a later turn. It has no place
// among the messages until the harness takes it, so it is listed apart.
// action is the board message that typed it, as the supervisor names it, or
// empty for one typed in the terminal.
export interface WaitingPrompt { id: string; at: string; source: PromptSource; text: string; sending: boolean; action: string }

export interface MessagePage { conversation: string; messages: SessionMessage[]; more: boolean; usage?: SessionUsage; waiting: WaitingPrompt[] }

export function parseWaiting(value: unknown): WaitingPrompt[] {
  return array(value).map((item) => {
    const each = object(item);
    return { id: string(each.id), at: string(each.at), source: each.source == null ? "" : choice(each.source, SOURCES, "prompt source"), text: string(each.text), sending: each.sending == null ? false : boolean(each.sending), action: each.action == null ? "" : string(each.action) };
  });
}

export function parseConversation(value: unknown): string {
  const conversation = string(value);
  if (!conversation) throw new Error("Invalid conversation");
  return conversation;
}

export function parseUsage(value: unknown): SessionUsage | undefined {
  if (value == null) return undefined;
  const each = object(value);
  return { input_tokens: number(each.input_tokens), output_tokens: number(each.output_tokens), cache_read_tokens: number(each.cache_read_tokens),
    cost_usd: number(each.cost_usd), context_tokens: number(each.context_tokens), context_window: number(each.context_window) };
}

// SessionReport is what the fleet's stream says of a session, as
// /api/messages sends it in a session event: the newest turn, whether that
// turn runs, ended, or was never reported by its harness, where the stream's
// head is, and why the conversation the board reads is not the current one or
// cannot be kept. It is the one thing the board draws a turn from, so a
// finished turn never draws a working indicator whatever a snapshot says.
export interface SessionReport {
  turn: string; state: TurnState; why: string; newest: number; head: number; stale: string; problem: string;
}
export type TurnState = "running" | "ended" | "unknown" | "none";
const TURN_STATES = ["running", "ended", "unknown", "none"] as const;

export function parseSession(value: unknown): SessionReport {
  const each = object(value);
  return {
    turn: string(each.turn), state: choice(each.state, TURN_STATES, "turn state"),
    why: each.why == null ? "" : string(each.why), newest: number(each.newest), head: number(each.head),
    stale: each.stale == null ? "" : string(each.stale), problem: each.problem == null ? "" : string(each.problem),
  };
}

// How a view stands against the stream it draws. Behind means the stream holds
// a message the view has not drawn, which is its own state: it is neither
// disconnected nor ended, and no clock infers it. Working means the stream
// says the newest turn runs and the view has drawn to the stream's head, so a
// turn that ended never draws an indicator and a view that has not caught up
// says it is behind instead.
export function viewStanding(report: SessionReport | null, messages: SessionMessage[]): { isBehind: boolean; isWorking: boolean } {
  if (!report) return { isBehind: false, isWorking: false };
  const newest = messages.length ? messages[messages.length - 1].seq : -1;
  const isBehind = report.newest > newest;
  return { isBehind, isWorking: !isBehind && report.state === "running" };
}

export function parsePage(value: unknown): MessagePage {
  const page = object(value);
  return { conversation: parseConversation(page.conversation), messages: parseMessages(page.messages), more: boolean(page.more), usage: parseUsage(page.usage), waiting: parseWaiting(page.waiting) };
}

// changeMessages puts each changed message in place of the one with its id,
// or adds it by its seq, so the list stays in the session's order.
export function changeMessages(held: SessionMessage[], changed: SessionMessage[]): SessionMessage[] {
  const byID = new Map(held.map((message) => [message.id, message]));
  for (const message of changed) byID.set(message.id, message);
  return [...byID.values()].sort((one, other) => one.seq - other.seq);
}

// pageMessages is what a view holds once a page of the newest messages
// arrives: the page alone when it begins another conversation, as /clear
// starts one, and otherwise the older messages the view already read before
// the page, so a stream renewed every ten minutes keeps them. more is
// whether older messages come before the first it holds.
export function pageMessages(held: MessagePage, page: MessagePage): MessagePage {
  if (held.conversation !== page.conversation) return page;
  const first = page.messages[0];
  if (!first || !held.messages.some((message) => message.id === first.id && message.seq === first.seq)) return page;
  const older = held.messages.filter((message) => message.seq < first.seq);
  return { conversation: page.conversation, messages: [...older, ...page.messages], more: older.length ? held.more : page.more, usage: page.usage, waiting: page.waiting };
}

// olderMessages puts a page read before the first message a view holds in
// front of it.
export function olderMessages(held: MessagePage, page: MessagePage): MessagePage {
  if (!held.conversation || held.conversation !== page.conversation) return held;
  const first = held.messages[0]?.seq ?? Infinity;
  return { conversation: held.conversation, messages: [...page.messages.filter((message) => message.seq < first), ...held.messages], more: page.more, usage: held.usage, waiting: held.waiting };
}

// messagesHarness says whether the board reads a harness's session as messages,
// which the supervisor serves from its session record without requiring hooks.
function messagesHarness(harness: string): boolean {
  return harness === "claude" || harness === "pi" || harness === "codex";
}

// messagesQuery names the session whose messages a panel shows, as
// /api/messages takes it: a supported goblin's current generation in a
// native terminal, or the CEO in its native terminal, since the Messages
// view's box types through that terminal. A paused
// goblin's conversation still reads. Empty for every other session, which
// keeps its Terminal and Task views alone: one in Herdr, a task
// not started yet, and one stopped or retired.
export function messagesQuery(snapshot: Pick<Snapshot, "ceo_terminal" | "ceo_harness"> & { ceo_terminal_since?: string }, task?: Task): string {
  if (!task) return snapshot.ceo_terminal && messagesHarness(snapshot.ceo_harness) ? new URLSearchParams({ ceo: snapshot.ceo_terminal, ...(snapshot.ceo_terminal_since ? { since: snapshot.ceo_terminal_since } : {}) }).toString() : "";
  const ended = sessionEnd(task);
  return messagesHarness(task.harness) && task.backend === "native" && task.generation && !task.archived && (!ended || ended === "paused") ? new URLSearchParams({ task: task.id, generation: task.generation }).toString() : "";
}

// A reply's text in the parts it is drawn in: a fenced code block as written,
// with the language its fence names, a heading, and the prose between them,
// which messageText draws with its bold, code, lists, tables and links.
export type TextPart = { kind: "prose"; text: string } | { kind: "heading"; text: string } | { kind: "code"; text: string; language: string };

const FENCE = /^[ \t]*(`{3,}|~{3,})[ \t]*([\w+#.-]*)/;
const HEADING = /^#{1,6}\s+(.+?)\s*#*\s*$/;

export function textParts(text: string): TextPart[] {
  const parts: TextPart[] = [];
  let prose: string[] = [];
  const flush = () => {
    if (prose.join("").trim()) parts.push({ kind: "prose", text: prose.join("\n") });
    prose = [];
  };
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  for (let at = 0; at < lines.length; at++) {
    const fence = FENCE.exec(lines[at]);
    const heading = HEADING.exec(lines[at]);
    if (fence) {
      flush();
      const code: string[] = [];
      // A fence closes on a line of the same mark at least as long; one left
      // open, as a reply cut short leaves it, runs to the end.
      for (at++; at < lines.length && !new RegExp("^[ \\t]*" + fence[1][0] + "{" + fence[1].length + ",}[ \\t]*$").test(lines[at]); at++) code.push(lines[at]);
      parts.push({ kind: "code", text: code.join("\n"), language: fence[2] });
    } else if (heading) {
      flush();
      parts.push({ kind: "heading", text: heading[1] });
    } else prose.push(lines[at]);
  }
  flush();
  return parts;
}

// isPowerShellBlock says whether a reply's code block is a PowerShell command
// its Run runs (#2051): its fence names powershell, pwsh or ps1, or, untagged,
// it starts with powershell or pwsh, after an optional "! ", the harness's
// shell-mode marker, accepted on purpose since #2051 asks for both forms. The
// server reads the block back from the session and decides the same way
// (powerShellCommand in internal/supervisor/code_run.go).
export function isPowerShellBlock(language: string, text: string): boolean {
  const command = text.replace(/^[ \t]*/, "").replace(/^! /, "");
  const isTagged = ["powershell", "pwsh", "ps1"].includes(language.toLowerCase());
  return !!command.trim() && (isTagged || !language && /^(powershell|pwsh)(\.exe)?([ \t\n]|$)/i.test(command));
}

// isWrittenReply says whether a reply's words are all written, so its code
// blocks may run: any reply but the newest message while the session works.
export function isWrittenReply(message: SessionMessage, messages: SessionMessage[], isWorking: boolean): boolean {
  return message.kind === "reply" && (!isWorking || message !== messages.at(-1));
}

// copyText is what the Copy of a message that is not a reply copies: its
// words as written, and a command with what it printed. Each text of a reply
// has a Copy of its own.
export function copyText(message: SessionMessage): string {
  return [message.text, message.output, message.errors].filter(Boolean).join("\n\n");
}

// A reply's blocks in the parts it is drawn in: its text and its thinking
// that hold words, each where it was written, and each run of calls made one
// after another as one stack. at is the first block's place in the reply.
export type ReplyPart = { kind: "text" | "thinking"; at: number; text: string } | { kind: "calls"; at: number; calls: MessageBlock[] };

// A run of a reply's calls as it shows: each call that finished as asked,
// two or more in a row, folded into one row that opens to them (#2191), and
// every other call its own row. A call still running, one that failed, was
// denied or was interrupted, a question, a plan and a baby goblin are never
// folded. A call with pictures folds as any other, since the folded row shows
// their thumbnails at its end. at is where each starts in the run.
export type CallRun = { kind: "fold"; at: number; calls: MessageBlock[] } | { kind: "call"; at: number; call: MessageBlock };
const QUESTIONS = new Set(["askuserquestion", "request_user_input"]);
const isFoldable = (call: MessageBlock) => (call.state === "done" || call.state === "") && !call.agent && !call.plan.length && !QUESTIONS.has(call.tool.toLowerCase());

export function callRuns(calls: MessageBlock[]): CallRun[] {
  const runs: CallRun[] = [];
  let start = 0;
  while (start < calls.length) {
    let end = start;
    while (end < calls.length && isFoldable(calls[end])) end++;
    if (end - start >= 2) {
      runs.push({ kind: "fold", at: start, calls: calls.slice(start, end) });
      start = end;
      continue;
    }
    runs.push({ kind: "call", at: start, call: calls[start] });
    start++;
  }
  return runs;
}

export function replyParts(blocks: MessageBlock[]): ReplyPart[] {
  const parts: ReplyPart[] = [];
  blocks.forEach((block, at) => {
    const last = parts.at(-1);
    if (block.kind !== "call") {
      // A block whose words are only whitespace, as pi writes "\n\n"
      // between the steps of a turn, is drawn as nothing: no prose row and no
      // Copy under it (the Overlord, 2026-11-07: "no copy button without
      // content").
      if (block.text.trim()) parts.push({ kind: block.kind, at, text: block.text });
    } else if (last?.kind === "calls") last.calls.push(block);
    else parts.push({ kind: "calls", at, calls: [block] });
  });
  return parts;
}

// The replies that end a turn, which alone show when they arrived (the
// Overlord, 2026-10-27: "We don't need to print the date after every
// response. We only need to print it at the end of a message"): the last
// reply before his next message or an interrupt, and the newest reply only
// once the session has stopped working. A message he sent that the harness
// queued into its turn goes on with that turn.
export function turnEnds(messages: SessionMessage[], isWorking: boolean): Set<string> {
  const ends = new Set<string>();
  let goesOn = isWorking;
  for (let at = messages.length - 1; at >= 0; at--) {
    const message = messages[at];
    if (message.kind === "reply") {
      if (!goesOn) ends.add(message.id);
      goesOn = true;
    } else if (message.kind === "interrupt" || message.kind === "prompt" && message.source !== "notice" && !message.queued) goesOn = false;
  }
  return ends;
}

// The message text's size, kept in this browser apart from the terminals',
// since a size the terminals take resizes their programs. Ctrl+Plus and
// Ctrl+Minus, the right button and the wheel, or the wheel with Ctrl and
// Shift, step it a pixel between the terminals' bounds, and Ctrl+0 restores
// it.
export const MESSAGE_FONT_KEY = "ceo-message-font-size";
export const DEFAULT_MESSAGE_FONT = 16;
export const MIN_MESSAGE_FONT = MIN_FONT_SIZE;
export const MAX_MESSAGE_FONT = MAX_FONT_SIZE;

export function storedMessageFont(): number {
  let size = 0;
  try { size = Number(localStorage.getItem(MESSAGE_FONT_KEY)); } catch { /* none kept */ }
  return size >= MIN_MESSAGE_FONT && size <= MAX_MESSAGE_FONT ? size : DEFAULT_MESSAGE_FONT;
}

export function storeMessageFont(size: number): void {
  try { localStorage.setItem(MESSAGE_FONT_KEY, String(size)); } catch { /* the size still applies to this view */ }
}

export function messageFontFor(key: string, current: number): number | null {
  if (key === "=" || key === "+") return Math.min(MAX_MESSAGE_FONT, current + 1);
  if (key === "-") return Math.max(MIN_MESSAGE_FONT, current - 1);
  if (key === "0") return DEFAULT_MESSAGE_FONT;
  return null;
}
