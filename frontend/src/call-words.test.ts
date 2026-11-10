import { test } from "node:test";
import assert from "node:assert/strict";
import { callTally, callWords, commandLanguage } from "./call-words.ts";

test("a call reads with its tool's icon and verb, as it runs and once it has run, for every harness's tools", () => {
  const cases: [string, boolean, ReturnType<typeof callWords>][] = [
    ["Read", false, { icon: "file", verb: "Read" }],
    ["read", true, { icon: "file", verb: "Reading" }],
    ["Edit", false, { icon: "edit", verb: "Edited" }],
    ["apply_patch", true, { icon: "edit", verb: "Editing" }],
    ["Bash", true, { icon: "terminal", verb: "Running" }],
    ["exec_command", false, { icon: "terminal", verb: "Ran" }],
    ["PowerShell", false, { icon: "terminal", verb: "Ran" }],
    ["Grep", false, { icon: "search", verb: "Searched" }],
    ["TodoWrite", false, { icon: "task", verb: "Updated the plan" }],
    ["update_plan", false, { icon: "task", verb: "Updated the plan" }],
    ["SomethingNew", false, { icon: "sparkle", verb: "SomethingNew" }],
  ];
  for (const [tool, isRunning, want] of cases) assert.deepEqual(callWords(tool, isRunning), want, tool + (isRunning ? " running" : ""));
});

// The Overlord, 2026-11-10, of a row that read "Called claude_ai_Supabase
// list_projects" beside a plug: the connector by its proper name with its
// logo, and the tool in words (#2302).
test("a connector's call reads as its connector by name and its tool in words, the same running and run", () => {
  const cases: [string, string, string][] = [
    ["mcp__claude_ai_Supabase__list_projects", "Supabase", "List projects"],
    ["mcp__claude_ai_Supabase__execute_sql", "Supabase", "Execute SQL"],
    ["mcp__plugin_supabase_supabase__execute_sql", "Supabase", "Execute SQL"],
    ["mcp__claude_ai_Acme_Billing__create_invoice", "Acme Billing", "Create invoice"],
    ["mcp__docs__fetch", "Docs", "Fetch"],
    ["mcp__chrome-devtools__take_snapshot", "Chrome DevTools", "Take snapshot"],
    ["mcp__claude_ai_PrecisionDocs__precisiondocs_add_parcels", "PrecisionDocs", "Add parcels"],
  ];
  for (const [tool, connector, action] of cases) {
    for (const isRunning of [false, true]) assert.deepEqual(callWords(tool, isRunning), { icon: "plug", verb: connector, connector, action }, tool);
  }
});

test("a tool search reads with a wrench: how many tools it loaded by name, each as its row names it, or that it searched", () => {
  // Arrange
  const select = "select:mcp__claude_ai_Supabase__list_projects,mcp__claude_ai_Supabase__list_branches, Read ,mcp__docs__fetch";
  const cut = "select:mcp__claude_ai_Supabase__list_projects,mcp__claude_ai_Supabase__list_branches,mcp__clau...";

  // Act and assert
  assert.deepEqual(callWords("ToolSearch", false, select), { icon: "wrench", verb: "Loaded 4 tools", loaded: ["Supabase · List projects", "Supabase · List branches", "Read", "Docs · Fetch"] });
  assert.deepEqual(callWords("ToolSearch", true, select), { icon: "wrench", verb: "Loading 4 tools", loaded: ["Supabase · List projects", "Supabase · List branches", "Read", "Docs · Fetch"] });
  assert.deepEqual(callWords("ToolSearch", false, "select:Read"), { icon: "wrench", verb: "Loaded 1 tool", loaded: ["Read"] });
  assert.deepEqual(callWords("ToolSearch", false, cut), { icon: "wrench", verb: "Loaded tools", loaded: ["Supabase · List projects", "Supabase · List branches"] });
  assert.deepEqual(callWords("ToolSearch", false, "notebook jupyter"), { icon: "wrench", verb: "Searched tools" });
  assert.deepEqual(callWords("toolsearch", true, ""), { icon: "wrench", verb: "Searching tools" });
  assert.deepEqual(callWords("Grep", false, "select:Read"), { icon: "search", verb: "Searched" }, "only a tool search loads tools");
});

test("a call that ran a command names the command's language, and any other call none", () => {
  const cases: [string, string][] = [["Bash", "bash"], ["PowerShell", "powershell"], ["exec", "javascript"], ["exec_command", "shell"], ["shell", "shell"], ["Read", ""], ["mcp__docs__fetch", ""]];
  for (const [tool, want] of cases) assert.equal(commandLanguage(tool), want, tool);
});

test("a folded run of calls counts each kind with the verb its rows use, in the order the kinds first ran", () => {
  const cases: [string[], string][] = [
    [["Bash", "Bash", "Bash", "Bash", "Bash"], "Ran 5 commands"],
    [["Read", "Read", "Bash", "exec_command", "Bash"], "Read 2 files, ran 3 commands"],
    [["Bash", "Read", "Bash"], "Ran 2 commands, read 1 file"],
    [["Edit", "apply_patch", "Write"], "Edited 2 files, wrote 1 file"],
    [["Grep", "Grep", "Glob"], "Searched 2 times, searched for files 1 time"],
    [["mcp__docs__fetch", "mcp__docs__fetch"], "Used Docs 2 times"],
    [["mcp__claude_ai_Supabase__list_projects", "mcp__claude_ai_Supabase__execute_sql", "Read"], "Used Supabase 2 times, read 1 file"],
    [["Read", "mcp__claude_ai_Supabase__list_projects"], "Read 1 file, used Supabase 1 time"],
    [["ToolSearch", "ToolSearch", "mcp__docs__fetch"], "Loaded tools 2 times, used Docs 1 time"],
    [["SomethingNew", "TodoWrite"], "Used SomethingNew 1 time, updated the plan 1 time"],
  ];
  for (const [tools, want] of cases) assert.equal(callTally(tools), want, tools.join(" "));
});
