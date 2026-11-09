import { at } from "./session-board";

// A conversation whose replies run several tools between their texts: the
// first runs five commands, the second reads two files and runs three
// commands while a fourth still runs, and the third has a failed command
// between two that passed (#2191).
const call = (id: string, tool: string, subject: string, state = "done", result = "ok") => ({ kind: "call", call_id: id, tool, subject, state, result: state === "running" ? "" : result });
export const COMMANDS = ["git status --short", "git log --oneline -3", "go vet ./internal/fleettree/", "npm run lint", "git diff --stat"];
export const FIVE_COMMANDS = { id: "reply-1", seq: 1, at, kind: "reply", blocks: [
  { kind: "text", text: "Checking the branch before the push." },
  ...COMMANDS.map((command, index) => call("bash-" + index, "Bash", command)),
  { kind: "text", text: "All five checks pass." },
] };
export const RUNNING = { id: "reply-2", seq: 2, at, kind: "reply", blocks: [
  { kind: "text", text: "Reading the reader, then building." },
  call("read-0", "Read", "frontend/src/message-call.tsx"), call("read-1", "Read", "frontend/src/call-words.ts"),
  call("bash-5", "Bash", "npm run typecheck"), call("bash-6", "Bash", "npm test"), call("bash-7", "Bash", "git status"),
  call("bash-8", "Bash", "npm run build", "running"),
] };
export const FAILED = { id: "reply-3", seq: 3, at, kind: "reply", blocks: [
  { kind: "text", text: "Running the checks again." },
  call("bash-9", "Bash", "npm run lint"), call("bash-10", "Bash", "npm run typecheck"),
  call("bash-11", "Bash", "npm test", "failed", "1 failing test"),
  call("bash-12", "Bash", "git status"), call("bash-13", "Bash", "git diff"),
  { kind: "text", text: "One test fails." },
] };
export const CONVERSATION = { conversation: "fixture-conversation", more: false, messages: [{ id: "prompt-0", seq: 0, at, kind: "prompt", source: "overlord", text: "Check the branch" }, FIVE_COMMANDS, RUNNING, FAILED] };

// Replies whose running call finishes and joins the finished calls beside it:
// one on the right of a command that passed, and one on the left of two
// files read, as parallel calls settle each on its own.
const PROMPT = { id: "prompt-0", seq: 0, at, kind: "prompt", source: "overlord", text: "Check the branch" };
export const conversationOf = (reply: object) => ({ conversation: "fixture-conversation", more: false, messages: [PROMPT, reply] });
export const JOINS_RIGHT = { id: "reply-4", seq: 1, at, kind: "reply", blocks: [call("bash-14", "Bash", "git status"), call("bash-15", "Bash", "npm test", "running")] };
export const JOINS_RIGHT_WITHOUT_IDS = { ...JOINS_RIGHT, blocks: JOINS_RIGHT.blocks.map((block) => ({ ...block, call_id: undefined })) };
export const JOINS_LEFT = { id: "reply-5", seq: 1, at, kind: "reply", blocks: [
  call("bash-16", "Bash", "npm run build", "running"), call("read-2", "Read", "frontend/src/message-calls.tsx"), call("read-3", "Read", "frontend/src/Disclosure.tsx"),
] };
