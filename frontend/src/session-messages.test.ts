import { test } from "node:test";
import assert from "node:assert/strict";
import { callRuns, changeMessages, copyText, DEFAULT_MESSAGE_FONT, MAX_MESSAGE_FONT, messageFontFor, messagesQuery, MIN_MESSAGE_FONT, isPowerShellBlock, isWrittenReply, olderMessages, pageMessages, parseMessages, parsePage, parseSession, parseWaiting, replyParts, textParts, turnEnds, viewStanding, type SessionMessage, type SessionReport } from "./session-messages.ts";
import type { Task } from "./types.ts";

// A message as the supervisor serves it (internal/fleettree/messages.go).
const served = (seq: number, fields: Record<string, unknown> = {}) => ({ id: "m" + seq, seq, kind: "prompt", at: "2026-10-22T09:00:00Z", source: "overlord", text: "prompt " + seq, ...fields });
const held = (...seqs: number[]): SessionMessage[] => parseMessages(seqs.map((seq) => served(seq)));
const seqs = (messages: SessionMessage[]) => messages.map((message) => message.seq);

test("structured tool updates retain full input and ordinary wrapper prose", () => {
  const prose = "The expression text(await ...) is JavaScript.";
  const input = 'text(await tools.exec_command({cmd:"git status"}));\ntext("second line");';
  const call = { kind: "call", tool: "exec", subject: "exec", input, state: "running" };
  const before = parseMessages([served(0, { text: prose }), served(1, { kind: "reply", blocks: [call, { kind: "text", text: prose }] })]);

  const after = changeMessages(before, parseMessages([served(1, { kind: "reply", blocks: [{ ...call, state: "failed", result: "Script failed" }, { kind: "text", text: prose }] })]));

  assert.deepEqual(after.map((message) => [message.id, message.seq]), [["m0", 0], ["m1", 1]]);
  assert.equal(after[0].text, prose);
  assert.deepEqual(after[1].blocks.map((block) => block.kind), ["call", "text"]);
  assert.equal(after[1].blocks[0].input, input);
  assert.equal(after[1].blocks[0].subject, "exec");
  assert.equal(after[1].blocks[0].state, "failed");
  assert.equal(after[1].blocks[0].result, "Script failed");
  assert.equal(after[1].blocks[1].text, prose);
});

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
  assert.deepEqual(parsePage({ conversation: "a", messages: [served(4)], more: true }).more, true);
  assert.throws(() => parsePage({ conversation: "a", messages: [] }), "more is required");
  for (const conversation of [undefined, "", 4]) assert.throws(() => parsePage({ conversation, messages: [], more: false }));
});

test("a changed message takes the place of the one with its id, and a new one its place by seq", () => {
  const changed = parseMessages([served(1, { text: "edited" }), served(3), served(2)]);
  const next = changeMessages(held(0, 1, 4), changed);
  assert.deepEqual(seqs(next), [0, 1, 2, 3, 4]);
  assert.equal(next[1].text, "edited");
});

test("a page of the same conversation keeps the older messages read before it, and another conversation's replaces them", () => {
  const before = { conversation: "a", messages: held(10, 11, 12, 13), more: true, waiting: [] };
  const renewed = pageMessages(before, { conversation: "a", messages: held(12, 13, 14), more: true, waiting: [] });
  assert.deepEqual(seqs(renewed.messages), [10, 11, 12, 13, 14]);
  assert.equal(renewed.more, true, "the older messages' own more");
  const cleared = parseMessages([served(0, { id: "new0" })]);
  assert.deepEqual(pageMessages(before, { conversation: "b", messages: cleared, more: false, waiting: [] }), { conversation: "b", messages: cleared, more: false, waiting: [] });
  assert.deepEqual(pageMessages(before, { conversation: "b", messages: held(12, 13), more: false, waiting: [] }), { conversation: "b", messages: held(12, 13), more: false, waiting: [] }, "colliding IDs do not mix conversations");
  assert.deepEqual(pageMessages(before, { conversation: "b", messages: [], more: false, waiting: [] }), { conversation: "b", messages: [], more: false, waiting: [] }, "an empty conversation holds nothing");
  assert.deepEqual(pageMessages({ conversation: "", messages: [], more: false, waiting: [] }, { conversation: "a", messages: held(3, 4), more: true, waiting: [] }), { conversation: "a", messages: held(3, 4), more: true, waiting: [] });
});

test("a page says what its harness holds now, and a page of older messages leaves that alone", () => {
  const prompt = { id: "q1", at: "2026-11-05T12:00:00Z", source: "overlord", text: "check the phone too", sending: false, action: "a1" };
  const waiting = parseWaiting([prompt]);
  assert.deepEqual(waiting, [prompt]);
  assert.deepEqual(parsePage({ conversation: "a", messages: [], more: false, waiting: [prompt] }).waiting, waiting);
  assert.deepEqual(parsePage({ conversation: "a", messages: [], more: false }).waiting, [], "a page with none leaves it out");
  assert.deepEqual(parseWaiting([{ ...prompt, source: undefined }])[0].source, "");
  assert.equal(parseWaiting([{ ...prompt, sending: undefined }])[0].sending, false);
  assert.throws(() => parseWaiting([{ ...prompt, source: "elsewhere" }]));
  assert.deepEqual(parseWaiting([{ ...prompt, sending: true }]), [{ ...prompt, sending: true }]);
  assert.throws(() => parseWaiting([{ ...prompt, sending: "yes" }]));
  assert.equal(parseWaiting([{ ...prompt, action: undefined }])[0].action, "", "a prompt typed in the terminal names no board message");
  assert.throws(() => parseWaiting([{ ...prompt, action: 1 }]));
  const before = { conversation: "a", messages: held(10, 11, 12), more: true, waiting };
  assert.deepEqual(pageMessages(before, { conversation: "a", messages: held(11, 12, 13), more: true, waiting: [] }).waiting, [], "a renewed stream's page ends a wait");
  assert.deepEqual(pageMessages({ ...before, waiting: [] }, { conversation: "a", messages: held(11, 12), more: true, waiting }).waiting, waiting, "and starts one");
  assert.deepEqual(olderMessages(before, { conversation: "a", messages: held(8, 9), more: false, waiting: [] }).waiting, waiting);
});

test("an older page goes before the first message held, and what it repeats is dropped", () => {
  const prior = { conversation: "a", messages: held(5, 6), more: true, waiting: [] };
  const next = olderMessages(prior, { conversation: "a", messages: held(2, 3, 4, 5), more: false, waiting: [] });
  assert.deepEqual(seqs(next.messages), [2, 3, 4, 5, 6]);
  assert.equal(next.more, false);
  assert.deepEqual(olderMessages(prior, { conversation: "b", messages: held(2, 3), more: false, waiting: [] }), prior);
});

test("a newest page clears abandoned usage while older pagination keeps current usage", () => {
  const before = parsePage({ conversation: "a", messages: [served(0), served(1, { kind: "reply" })], more: false,
    usage: { input_tokens: 12000, context_tokens: 24000, context_window: 100000, model: "recorded-model" } });
  const reset = parsePage({ conversation: "b", messages: [served(0)], more: false });

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
  assert.equal(messagesQuery({ cfo_terminal: "t-7", cfo_harness: "pi" }), "cfo=t-7", "a pi CEO");
  assert.equal(messagesQuery(cfo, task({ harness: "codex" })), "task=cg-one&generation=s1", "a Codex goblin reads its rollout without hooks");
  for (const [why, other] of [["a goblin in Herdr", task({ backend: "herdr" })], ["a task not started", task({ generation: "" })], ["a stopped goblin", task({ phase: "stopped" })], ["a retired goblin", task({ archived: true })]] as const) {
    assert.equal(messagesQuery(cfo, other), "", why);
  }
  assert.equal(messagesQuery({ cfo_terminal: "", cfo_harness: "claude" }), "", "the CEO in Herdr");
  assert.equal(messagesQuery({ cfo_terminal: "t-7", cfo_harness: "codex" }), "cfo=t-7", "a Codex CEO reads its rollout without hooks");
  assert.equal(messagesQuery({ cfo_terminal: "t-7", cfo_harness: "" }), "", "a CEO that has not registered its harness");
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

// A fence the view counts is one the server counts (codeFences in
// internal/supervisor/code_run.go), so the fence a Run names is the one the
// server reads back: only spaces and tabs may stand before a fence's mark.
test("only spaces and tabs indent a fence, so the view counts the fences the server counts", () => {
  for (const indent of [" ", "\t", " \t "]) {
    assert.deepEqual(textParts(indent + "```powershell\nGet-Date\n" + indent + "```"), [{ kind: "code", text: "Get-Date", language: "powershell" }], JSON.stringify(indent));
  }
  for (const indent of [" ", "\v", "　", "﻿"]) {
    const text = indent + "```text\nfoo\n```\n```powershell\nA\n```\n```powershell\nB\n```";
    const fences = textParts(text).filter((part) => part.kind === "code");
    assert.deepEqual(fences, [{ kind: "code", text: "```powershell\nA", language: "" }, { kind: "code", text: "B", language: "powershell" }], JSON.stringify(indent));
  }
});

test("a reply draws each run of calls as one stack between its words, and leaves out blocks with no words", () => {
  // Arrange
  const [reply] = parseMessages([served(0, { kind: "reply", blocks: [
    { kind: "thinking", text: "Plan it" }, { kind: "text", text: "Reading." }, { kind: "call", tool: "Read", state: "done" }, { kind: "call", tool: "Grep", state: "done" },
    { kind: "thinking", text: "" }, { kind: "text", text: "\n\n" }, { kind: "text", text: "Found it." }, { kind: "call", tool: "Edit", state: "running" },
  ] })]);
  const [read, grep, , , , edit] = reply.blocks.slice(2);

  // Act
  const parts = replyParts(reply.blocks);

  // Assert
  assert.deepEqual(parts, [
    { kind: "thinking", at: 0, text: "Plan it" }, { kind: "text", at: 1, text: "Reading." }, { kind: "calls", at: 2, calls: [read, grep] },
    { kind: "text", at: 6, text: "Found it." }, { kind: "calls", at: 7, calls: [edit] },
  ]);
  assert.deepEqual(replyParts([]), []);
});

test("Copy copies a command with what it printed, and a compaction its summary", () => {
  const [command, compaction] = parseMessages([
    served(1, { source: "shell", text: "git status", output: "clean", errors: "warning" }),
    served(2, { kind: "compaction", source: undefined, text: "The summary", trigger: "auto" }),
  ]);
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

test("a call's plan keeps each step's words and state, and a state the board does not draw is refused", () => {
  // Arrange
  const call = (status: string) => served(4, { kind: "reply", source: undefined, text: undefined, blocks: [
    { kind: "call", tool: "TodoWrite", state: "done", plan: [{ text: "Read the touch handlers", status: "completed" }, { text: "Fix the swipe", status }] },
  ] });

  // Act
  const [reply] = parseMessages([call("in_progress")]);

  // Assert
  assert.deepEqual(reply.blocks[0].plan, [{ text: "Read the touch handlers", status: "completed" }, { text: "Fix the swipe", status: "in_progress" }]);
  assert.deepEqual(parseMessages([served(5, { kind: "reply", source: undefined, blocks: [{ kind: "text", text: "Done." }] })])[0].blocks[0].plan, []);
  assert.throws(() => parseMessages([call("blocked")]));
});

test("a disjoint newest page replaces held history so older pagination can fill the gap", () => {
  const previous = { conversation: "a", messages: held(...Array.from({ length: 50 }, (_, index) => index)), more: false, waiting: [] };
  const newest = { conversation: "a", messages: held(...Array.from({ length: 50 }, (_, index) => index + 100)), more: true, waiting: [] };
  const next = pageMessages(previous, newest);
  assert.deepEqual(next, newest);
  const recovered = olderMessages(next, { conversation: "a", messages: held(...Array.from({ length: 50 }, (_, index) => index + 50)), more: true, waiting: [] });
  assert.deepEqual(seqs(recovered.messages), Array.from({ length: 100 }, (_, index) => index + 50));
});

// What the fleet's stream says of a session, as /api/messages sends it in a
// session event: the newest turn, whether it runs, ended or was never
// reported, and where the head of the stream is.
const reported = (fields: Record<string, unknown> = {}) => ({ turn: "m0", state: "running", newest: 1, head: 3, ...fields });

test("a session that produced a newer turn than the view drew is behind, not working", () => {
  // Arrange: he drew the prompt and its first reply; the stream holds a later
  // message the view has not drawn.
  const messages = held(0, 1);

  // Act
  const standing = viewStanding(parseSession(reported({ newest: 4 })) as SessionReport, messages);

  // Assert: behind is its own state, and no indicator is drawn on a turn the
  // view has not caught up with.
  assert.deepEqual(standing, { isBehind: true, isWorking: false });
});

test("a drawn turn the stream says ended never draws a working indicator, whatever the snapshot says", () => {
  // Arrange
  const messages = held(0, 1);

  // Act
  const standing = viewStanding(parseSession(reported({ state: "ended", newest: 1 })) as SessionReport, messages);

  // Assert
  assert.deepEqual(standing, { isBehind: false, isWorking: false });
});

test("a turn the stream says runs is working once the view has drawn to its end", () => {
  const standing = viewStanding(parseSession(reported({ newest: 1 })) as SessionReport, held(0, 1));
  assert.deepEqual(standing, { isBehind: false, isWorking: true });
});

test("a turn no harness reported draws no indicator and says why", () => {
  const report = parseSession(reported({ state: "unknown", why: "no hooks", newest: 1 }));
  assert.deepEqual(viewStanding(report as SessionReport, held(0, 1)), { isBehind: false, isWorking: false });
  assert.equal((report as SessionReport).why, "no hooks");
});

test("a session the board hears nothing of yet is neither behind nor working", () => {
  assert.deepEqual(viewStanding(null, held(0, 1)), { isBehind: false, isWorking: false });
});

test("a session event carries why its record is stale or its stream cannot be kept, and says neither when it omits them", () => {
  // Arrange
  const cases = [
    { what: "a stale record", value: reported({ stale: "The record names cfo-9." }), stale: "The record names cfo-9.", problem: "" },
    { what: "a stream that cannot be kept", value: reported({ problem: "disk full" }), stale: "", problem: "disk full" },
    { what: "a current record", value: reported(), stale: "", problem: "" },
  ];
  for (const { what, value, stale, problem } of cases) {
    // Act
    const report = parseSession(value);

    // Assert
    assert.deepEqual({ stale: report.stale, problem: report.problem }, { stale, problem }, what);
  }
  assert.throws(() => parseSession(reported({ stale: 9 })), /Invalid/, "a stale reason that is not words");
});

test("a session event without the fields the board draws is refused", () => {
  for (const [what, value] of [["an unknown turn state", reported({ state: "maybe" })], ["a newest that is not a number", reported({ newest: "4" })]] as const) {
    assert.throws(() => parseSession(value), /Invalid/, what);
  }
});

// A reply's blocks as the supervisor serves them, each call by its tool and
// state, with any other fields it needs.
const reply = (blocks: Record<string, unknown>[]) => parseMessages([served(9, { kind: "reply", blocks })])[0].blocks;
const call = (tool = "Bash", state = "done", fields: Record<string, unknown> = {}) => ({ kind: "call", tool, subject: tool, state, ...fields });
const shape = (blocks: ReturnType<typeof reply>) => callRuns(blocks).map((run) => run.kind === "fold" ? "fold " + run.at + "+" + run.calls.length : run.call.state + " " + run.at);

for (const [name, blocks, want] of [
  ["five finished commands fold into one row", [call(), call(), call(), call(), call()], ["fold 0+5"]],
  ["one finished call stays its own row", [call("Read")], ["done 0"]],
  ["the newest call still running stays a live row beside the folded ones", [call("Read"), call("Read"), call(), call(), call(), call("Bash", "running")], ["fold 0+5", "running 5"]],
  ["a failed call is never folded and splits the run", [call(), call(), call("Bash", "failed"), call(), call()], ["fold 0+2", "failed 2", "fold 3+2"]],
  ["denied and interrupted calls stay rows", [call("Bash", "denied"), call("Bash", "interrupted")], ["denied 0", "interrupted 1"]],
  ["a call with pictures, a question, a plan or a baby goblin stays its row", [
    call("view_image", "done", { pictures: [{ id: "p1", media_type: "image/png", bytes: 1 }] }), call("AskUserQuestion"), call("request_user_input"),
    call("TodoWrite", "done", { plan: [{ text: "Read", status: "completed" }] }), call("Task", "done", { agent: { node: "subagent:a", type: "Explore" } }),
  ], ["done 0", "done 1", "done 2", "done 3", "done 4"]],
  ["a call whose outcome was never recorded folds with finished ones", [call("Read", "done", { state: undefined }), call("Read")], ["fold 0+2"]],
] as const) {
  test("calls: " + name, () => {
    assert.deepEqual(shape(reply([...blocks])), want);
  });
}

test("reply text breaks a run of calls, and a blank text between calls does not", () => {
  const blocks = reply([call(), call(), { kind: "text", text: "Checked." }, call(), { kind: "text", text: "\n\n" }, call(), call()]);

  const runs = replyParts(blocks).map((part) => part.kind === "calls" ? shape(part.calls) : [part.kind]);

  assert.deepEqual(runs, [["fold 0+2"], ["text"], ["fold 0+3"]]);
});

test("a call keeps the id the supervisor served, and one served without an id has none", () => {
  const blocks = reply([call("Bash", "done", { call_id: "toolu_9" }), call("Read")]);

  const ids = blocks.map((block) => block.call_id);

  assert.deepEqual(ids, ["toolu_9", ""]);
});

for (const [language, text, isPowerShell] of [
  ["powershell", "Get-Date", true],
  ["pwsh", "Get-ChildItem", true],
  ["PS1", "Write-Output ok", true],
  ["powershell", "! Get-Date", true],
  ["", "powershell -NoProfile -Command Get-Date", true],
  ["", "pwsh.exe -c 'Get-Date'", true],
  ["", "! powershell -NoProfile", true],
  ["", "  ! pwsh", true],
  ["", "Get-Date", false],
  ["", "powershellish --help", false],
  ["bash", "powershell -c ls", false],
  ["python", "powershell = 1", false],
  ["powershell", "  ", false],
  ["", "", false],
] as const) {
  test(`a ${language || "untagged"} block holding ${JSON.stringify(text)} ${isPowerShell ? "is" : "is not"} a PowerShell command a reply's Run runs`, () => {
    // Act
    const isRunnable = isPowerShellBlock(language, text);

    // Assert
    assert.equal(isRunnable, isPowerShell);
  });
}

test("only a reply whose words are all written may run its code blocks: never the newest message while the session works", () => {
  // Arrange
  const message = (id: string, kind: "prompt" | "reply"): SessionMessage => ({ id, seq: 0, kind, at: "2026-11-09T12:00:00Z", source: "", queued: false, text: "", output: "", errors: "", trigger: "", blocks: [], pictures: [] });
  const earlier = message("r1", "reply"), asked = message("p1", "prompt"), newest = message("r2", "reply");
  const talk = [earlier, asked, newest];

  // Act
  const working = talk.map((each) => isWrittenReply(each, talk, true));
  const stopped = talk.map((each) => isWrittenReply(each, talk, false));
  const askedLast = [earlier, asked].map((each, _, all) => isWrittenReply(each, all, true));

  // Assert
  assert.deepEqual(working, [true, false, false]);
  assert.deepEqual(stopped, [true, false, true]);
  assert.deepEqual(askedLast, [true, false]);
});
