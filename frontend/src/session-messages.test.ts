import { test } from "node:test";
import assert from "node:assert/strict";
import { changeMessages, copyText, DEFAULT_MESSAGE_FONT, MAX_MESSAGE_FONT, messageFontFor, messagesQuery, MIN_MESSAGE_FONT, olderMessages, pageMessages, parseMessages, parsePage, replyParts, textParts, turnEnds, type SessionMessage } from "./session-messages.ts";
import type { Task } from "./types.ts";

// A message as the supervisor serves it (internal/fleettree/messages.go).
const served = (seq: number, fields: Record<string, unknown> = {}) => ({ id: "m" + seq, seq, kind: "prompt", at: "2026-10-22T09:00:00Z", source: "overlord", text: "prompt " + seq, ...fields });
const held = (...seqs: number[]): SessionMessage[] => parseMessages(seqs.map((seq) => served(seq)));
const seqs = (messages: SessionMessage[]) => messages.map((message) => message.seq);

test("a reply's blocks keep their text, thinking and calls, with an edit's counts and a sub-agent's node", () => {
  const [reply] = parseMessages([served(3, { kind: "reply", source: undefined, text: undefined, blocks: [
    { kind: "thinking", text: "Look at the reader first" },
    { kind: "text", text: "Reading the file" },
    { kind: "call", call_id: "toolu_1", tool: "Edit", subject: "src/a.ts", state: "done", edit: { added: 2, removed: 1, diff: ["+a", "-b", " c"] } },
    { kind: "call", call_id: "toolu_2", tool: "Agent", subject: "find the handlers", state: "running", agent: { node: "subagent:toolu_2", type: "Explore" } },
    { kind: "call", call_id: "toolu_3", tool: "Read", subject: "shot.png", state: "done", pictures: [{ id: "p1", media_type: "image/png", bytes: 120 }] },
  ] })]);
  assert.equal(reply.kind, "reply");
  assert.deepEqual(reply.blocks.map((block) => block.kind), ["thinking", "text", "call", "call", "call"]);
  assert.deepEqual(reply.blocks[2].edit, { added: 2, removed: 1, diff: ["+a", "-b", " c"] });
  assert.equal(reply.blocks[2].agent, null);
  assert.deepEqual(reply.blocks[3].agent, { node: "subagent:toolu_2", type: "Explore" });
  assert.equal(reply.blocks[3].state, "running");
  assert.deepEqual(reply.blocks[4].pictures, [{ id: "p1", media_type: "image/png", bytes: 120 }]);
});

test("a prompt keeps its source, whether it was queued, and what its command printed", () => {
  const [answer, command, shell] = parseMessages([served(0, { source: "answer", queued: true }), served(1, { source: "slash", text: "/cost", output: "Total cost: $0.12" }), served(2, { source: "shell", text: "git status", output: "clean", errors: "warning" })]);
  assert.equal(answer.source, "answer");
  assert.equal(answer.queued, true);
  assert.deepEqual([command.source, command.text, command.output], ["slash", "/cost", "Total cost: $0.12"]);
  assert.deepEqual([shell.output, shell.errors], ["clean", "warning"]);
});

for (const [what, value] of [["an unknown kind", served(0, { kind: "summary" })], ["an unknown source", served(0, { source: "robot" })], ["an unknown call state", served(0, { kind: "reply", blocks: [{ kind: "call", state: "maybe" }] })], ["a seq that is not a number", served(0, { seq: "1" })]] as const) {
  test(`a message with ${what} is refused`, () => {
    assert.throws(() => parseMessages([value]));
  });
}

test("a page says whether older messages come before it", () => {
  assert.deepEqual(parsePage({ messages: [served(4)], more: true }).more, true);
  assert.throws(() => parsePage({ messages: [] }), "more is required");
});

test("a changed message takes the place of the one with its id, and a new one its place by seq", () => {
  const changed = parseMessages([served(1, { text: "edited" }), served(3), served(2)]);
  const next = changeMessages(held(0, 1, 4), changed);
  assert.deepEqual(seqs(next), [0, 1, 2, 3, 4]);
  assert.equal(next[1].text, "edited");
});

test("a page of the same conversation keeps the older messages read before it, and another conversation's replaces them", () => {
  const before = { messages: held(10, 11, 12, 13), more: true };
  const renewed = pageMessages(before, { messages: held(12, 13, 14), more: true });
  assert.deepEqual(seqs(renewed.messages), [10, 11, 12, 13, 14]);
  assert.equal(renewed.more, true, "the older messages' own more");
  const cleared = parseMessages([served(0, { id: "new0" })]);
  assert.deepEqual(pageMessages(before, { messages: cleared, more: false }), { messages: cleared, more: false });
  assert.deepEqual(pageMessages(before, { messages: [], more: false }), { messages: [], more: false }, "an empty conversation holds nothing");
  assert.deepEqual(pageMessages({ messages: [], more: false }, { messages: held(3, 4), more: true }), { messages: held(3, 4), more: true });
});

test("an older page goes before the first message held, and what it repeats is dropped", () => {
  const next = olderMessages({ messages: held(5, 6), more: true }, { messages: held(2, 3, 4, 5), more: false });
  assert.deepEqual(seqs(next.messages), [2, 3, 4, 5, 6]);
  assert.equal(next.more, false);
});

test("a newest page clears abandoned usage while older pagination keeps current usage", () => {
  const before = parsePage({ messages: [served(0), served(1, { kind: "reply" })], more: false,
    usage: { input_tokens: 12000, context_tokens: 24000, context_window: 100000, model: "recorded-model" } });
  const reset = parsePage({ messages: [served(0)], more: false });

  const next = pageMessages(before, reset);

  assert.deepEqual(next.messages, reset.messages);
  assert.equal(next.usage, undefined, "the surviving prompt does not keep the abandoned reply's statistics");
  assert.deepEqual(olderMessages(before, reset).usage, before.usage, "older pagination is not an authoritative usage reset");
});

const task = (fields: Partial<Task>) => ({ id: "cg-one", harness: "claude", backend: "native", generation: "s1", phase: "working", archived: false, ...fields }) as Task;

test("supported native sessions have messages for the current goblin generation and CFO host", () => {
  const cfo = { cfo_terminal: "t-7", cfo_harness: "claude" };
  assert.equal(messagesQuery(cfo, task({})), "task=cg-one&generation=s1");
  assert.equal(messagesQuery(cfo, task({ harness: "pi" })), "task=cg-one&generation=s1", "a pi goblin's conversation reads too");
  assert.equal(messagesQuery(cfo, task({ phase: "paused" })), "task=cg-one&generation=s1", "a paused goblin's conversation still reads");
  assert.equal(messagesQuery(cfo), "cfo=t-7");
  assert.equal(messagesQuery({ ...cfo, cfo_terminal_since: "restart-2" }), "cfo=t-7&since=restart-2");
  assert.equal(messagesQuery({ cfo_terminal: "t-7", cfo_harness: "pi" }), "cfo=t-7", "a pi CFO");
  assert.equal(messagesQuery(cfo, task({ harness: "codex" })), "task=cg-one&generation=s1", "a Codex goblin reads its rollout without hooks");
  for (const [why, other] of [["a goblin in Herdr", task({ backend: "herdr" })], ["a task not started", task({ generation: "" })], ["a stopped goblin", task({ phase: "stopped" })], ["a retired goblin", task({ archived: true })]] as const) {
    assert.equal(messagesQuery(cfo, other), "", why);
  }
  assert.equal(messagesQuery({ cfo_terminal: "", cfo_harness: "claude" }), "", "the CFO in Herdr");
  assert.equal(messagesQuery({ cfo_terminal: "t-7", cfo_harness: "codex" }), "cfo=t-7", "a Codex CFO reads its rollout without hooks");
  assert.equal(messagesQuery({ cfo_terminal: "t-7", cfo_harness: "" }), "", "a CFO that has not registered its harness");
});

test("a reply's code blocks, headings and prose are drawn apart, a code block exactly as written with the language its fence names", () => {
  const text = "## The plan\n\nFirst **read** it:\n\n```go\nfunc a() {\n\n    return\n}\n```\nThen `b`.\n~~~\nopen to the end\n  indented";
  assert.deepEqual(textParts(text), [
    { kind: "heading", text: "The plan" },
    { kind: "prose", text: "\nFirst **read** it:\n" },
    { kind: "code", text: "func a() {\n\n    return\n}", language: "go" },
    { kind: "prose", text: "Then `b`." },
    { kind: "code", text: "open to the end\n  indented", language: "" },
  ]);
  assert.deepEqual(textParts("````\n```\nstill code\n````"), [{ kind: "code", text: "```\nstill code", language: "" }], "a shorter fence inside a longer one is code");
  assert.deepEqual(textParts("``` c++ \nint a;\n```"), [{ kind: "code", text: "int a;", language: "c++" }], "a language with symbols, spaced from its fence");
  assert.deepEqual(textParts(""), []);
  assert.deepEqual(textParts("#hashtag is prose"), [{ kind: "prose", text: "#hashtag is prose" }]);
});

test("a reply draws each run of calls as one stack between its words, and leaves out blocks with no words", () => {
  // Arrange
  const [reply] = parseMessages([served(0, { kind: "reply", blocks: [
    { kind: "thinking", text: "Plan it" }, { kind: "text", text: "Reading." }, { kind: "call", tool: "Read", state: "done" }, { kind: "call", tool: "Grep", state: "done" },
    { kind: "thinking", text: "" }, { kind: "text", text: "Found it." }, { kind: "call", tool: "Edit", state: "running" },
  ] })]);
  const [read, grep, , , edit] = reply.blocks.slice(2);

  // Act
  const parts = replyParts(reply.blocks);

  // Assert
  assert.deepEqual(parts, [
    { kind: "thinking", at: 0, text: "Plan it" }, { kind: "text", at: 1, text: "Reading." }, { kind: "calls", at: 2, calls: [read, grep] },
    { kind: "text", at: 5, text: "Found it." }, { kind: "calls", at: 6, calls: [edit] },
  ]);
  assert.deepEqual(replyParts([]), []);
});

test("Copy copies a reply's text blocks, and a command with what it printed", () => {
  const [reply, command, compaction] = parseMessages([
    served(0, { kind: "reply", blocks: [{ kind: "text", text: "One" }, { kind: "call", tool: "Bash", state: "done" }, { kind: "thinking", text: "hidden" }, { kind: "text", text: "Two" }] }),
    served(1, { source: "shell", text: "git status", output: "clean", errors: "warning" }),
    served(2, { kind: "compaction", source: undefined, text: "The summary", trigger: "auto" }),
  ]);
  assert.equal(copyText(reply), "One\n\nTwo");
  assert.equal(copyText(command), "git status\n\nclean\n\nwarning");
  assert.equal(copyText(compaction), "The summary");
});

test("the message text steps a pixel at a time between the terminals' bounds and Ctrl+0 restores it", () => {
  assert.equal(messageFontFor("=", 16), 17);
  assert.equal(messageFontFor("+", 16), 17);
  assert.equal(messageFontFor("-", 16), 15);
  assert.equal(messageFontFor("0", 22), DEFAULT_MESSAGE_FONT);
  assert.equal(messageFontFor("=", MAX_MESSAGE_FONT), MAX_MESSAGE_FONT);
  assert.equal(messageFontFor("-", MIN_MESSAGE_FONT), MIN_MESSAGE_FONT);
  assert.equal(messageFontFor("a", 16), null);
});

test("only the last reply of each turn ends it, and the newest only once the session stops working", () => {
  // Arrange: two turns, the first of three replies with a compaction and a
  // notice inside it, then an interrupted turn, then one he queued into the
  // last turn.
  const reply = (seq: number) => served(seq, { kind: "reply", source: undefined, text: undefined, blocks: [{ kind: "text", text: "reply " + seq }] });
  const messages = parseMessages([
    served(0), reply(1), served(2, { kind: "compaction", source: undefined, text: "summary" }), reply(3), served(4, { source: "notice" }), reply(5),
    served(6), reply(7), served(8, { kind: "interrupt", source: undefined, text: undefined }),
    served(9), reply(10), served(11, { queued: true }), reply(12),
  ]);

  // Act
  const finished = turnEnds(messages, false);
  const working = turnEnds(messages, true);

  // Assert
  assert.deepEqual([...finished].sort(), ["m12", "m5", "m7"]);
  assert.deepEqual([...working].sort(), ["m5", "m7"]);
  assert.deepEqual([...turnEnds([], false)], []);
});
