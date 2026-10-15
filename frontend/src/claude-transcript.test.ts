import { test } from "node:test";
import assert from "node:assert/strict";
import { type AgentEntry, elapsed, parseAgentEntries, redraw, transcriptRows, workingRow } from "./claude-transcript.ts";

// eslint-disable-next-line no-control-regex
const plain = (row: string) => row.replace(/\x1b\[[0-9;]*m/g, "");
const entry = (fields: Partial<AgentEntry>): AgentEntry => ({ kind: "call", text: "", tool: "", subject: "", state: "", result: [], more: 0, ...fields });
const SUCCESS = "38;2;78;186;101", ERROR = "38;2;255;107;128", QUIET = "38;2;153;153;153";

test("a call's mark takes the colour of its outcome, as Claude Code draws it, with its tool in bold", () => {
  for (const [state, colour] of [["done", SUCCESS], ["failed", ERROR], ["running", QUIET]] as const) {
    const [row] = transcriptRows([entry({ tool: "Bash", subject: "go test ./...", state })], 80);
    assert.equal(plain(row), "● Bash(go test ./...)");
    assert.ok(row.startsWith(`\x1b[0;${colour}m●`), `${state}: ${JSON.stringify(row)}`);
    assert.ok(row.includes("\x1b[0;1mBash\x1b[0m(go test ./...)"), `${state} tool in bold: ${JSON.stringify(row)}`);
  }
});

test("what came back sits under the elbow, further lines under it and the rest folded into +N lines", () => {
  const rows = transcriptRows([entry({ tool: "Bash", subject: "go test", state: "done", result: ["=== RUN TestA", "--- PASS: TestA"], more: 4 })], 80);
  assert.deepEqual(rows.map(plain), ["● Bash(go test)", "  ⎿  === RUN TestA", "     --- PASS: TestA", "     … +4 lines"]);
  assert.ok(rows[1].startsWith(`\x1b[0;${QUIET}m  ⎿  === RUN TestA`), JSON.stringify(rows[1]));
  assert.ok(rows[3].startsWith("\x1b[0;2m     … +4 lines"), JSON.stringify(rows[3]));
  assert.equal(plain(transcriptRows([entry({ tool: "Bash", state: "done", result: ["a"], more: 1 })], 80)[2]), "     … +1 line");
});

test("a failed call's error is red", () => {
  const rows = transcriptRows([entry({ tool: "Read", subject: "gone.go", state: "failed", result: ["Error: File does not exist."] })], 80);
  assert.ok(rows[1].includes(`\x1b[0;${ERROR}mError: File does not exist.`), JSON.stringify(rows[1]));
});

test("a long path wraps where the terminal's row ends, and the rest of the row continues under the text", () => {
  const path = "internal/fleettree/a-very-long-folder-name/and-another-one/agent_terminal_windows_test.go";
  const rows = transcriptRows([entry({ tool: "Read", subject: path, state: "done", result: [path] })], 40).map(plain);
  const call = rows.slice(0, rows.findIndex((row) => row.startsWith("  ⎿")));
  assert.equal(call[0], "● Read(" + "internal/fleettree/a-very-long-folder-name/and-another-one/agent_terminal_windows_test.go)".slice(0, 33));
  for (const row of call.slice(0, -1)) assert.equal(row.length, 40, `a full row: ${row}`);
  for (const row of call.slice(1)) assert.ok(row.startsWith("  ") && !row.startsWith("   "), `under the text: ${row}`);
  assert.equal(call.join("").replace(/^● /, "").replaceAll("  ", ""), "Read(" + path + ")");
  const result = rows.slice(call.length);
  assert.ok(result[0].startsWith("  ⎿  ") && result[0].length === 40);
  for (const row of result.slice(1)) assert.ok(row.startsWith("     ") && !row.startsWith("      "), `under the result: ${row}`);
});

test("a reply wraps at spaces, keeps its paragraphs, and shows inline code and bold without their marks", () => {
  const rows = transcriptRows([entry({ kind: "reply", text: "The stall rule lives in `stall.go` and is **fine** as it stands.\n\nNext." })], 24).map(plain);
  assert.deepEqual(rows, ["● The stall rule lives", "  in stall.go and is", "  fine as it stands.", "  ", "  Next."]);
  const styled = transcriptRows([entry({ kind: "reply", text: "## 1. Found\nSee `a.go` and **b**." })], 40);
  assert.deepEqual(styled.map(plain), ["● 1. Found", "  See a.go and b."]);
  assert.ok(styled[0].includes("\x1b[0;1m1. Found"), JSON.stringify(styled[0]));
  assert.ok(styled[1].includes("\x1b[0;38;2;177;185;249ma.go\x1b[0m and \x1b[0;1mb\x1b[0m."), JSON.stringify(styled[1]));
});

test("a prompt is a block after > on Claude Code's grey, as wide as the terminal", () => {
  const rows = transcriptRows([entry({ kind: "prompt", text: "Map the monitor" }), entry({ kind: "reply", text: "Done." })], 20);
  assert.deepEqual(rows.map(plain), ["> Map the monitor   ", "", "● Done."]);
  assert.ok(rows[0].includes("48;2;55;55;55"), JSON.stringify(rows[0]));
  assert.ok(!rows[2].includes("48;2;55;55;55"));
});

test("the working line turns a star in Claude Code's orange and counts as Claude Code counts", () => {
  assert.equal(plain(workingRow(0, "12s")), "· Working… (12s)");
  assert.equal(plain(workingRow(4, "")), "✻ Working…");
  assert.ok(workingRow(1, "1s").startsWith("\x1b[0;38;2;215;119;87m✢ Working…"));
  const now = Date.parse("2026-10-15T18:00:00Z");
  for (const [started, want] of [["2026-10-15T17:59:48Z", "12s"], ["2026-10-15T17:56:55Z", "3m 5s"], ["2026-10-15T16:55:00Z", "1h 5m"], ["", ""], ["0001-01-01T00:00:00Z", ""]] as const) {
    assert.equal(elapsed(started, now), want, started);
  }
});

test("a redraw writes only from the first row that changed, and starts over once that row has left the screen", () => {
  const cases: [string[], string[], number, string | null][] = [
    [[], ["a", "b"], 10, "a\r\nb"],
    [["a", "b"], ["a", "b"], 10, ""],
    [["a", "b"], ["a", "b", "c"], 10, "\r\nc"],
    [["a", "b", "w1"], ["a", "b", "w2"], 10, "\r\x1b[Jw2"],
    [["a", "b", "w1"], ["a", "B", "c", "w2"], 10, "\x1b[1A\r\x1b[JB\r\nc\r\nw2"],
    [["a", "b", "", "w"], ["a", "b"], 10, "\x1b[2A\r\x1b[Jb"],
    [["a", "b"], [], 10, "\x1b[1A\r\x1b[J"],
    [["a", "b", "c", "d"], ["A", "b", "c", "d"], 3, null],
  ];
  for (const [previous, next, screen, want] of cases) assert.equal(redraw(previous, next, screen), want, JSON.stringify({ previous, next }));
});

test("entries are read from the supervisor's answer and an unknown kind is refused", () => {
  assert.deepEqual(parseAgentEntries([{ kind: "call", tool: "Bash", subject: "ls", state: "done", result: ["a"], more: 2 }, { kind: "reply", text: "hi" }]), [
    { kind: "call", text: "", tool: "Bash", subject: "ls", state: "done", result: ["a"], more: 2 },
    { kind: "reply", text: "hi", tool: "", subject: "", state: "", result: [], more: 0 },
  ]);
  assert.throws(() => parseAgentEntries([{ kind: "thinking" }]));
});
