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
    ["mcp__docs__fetch", false, { icon: "plug", verb: "Called docs fetch" }],
    ["mcp__docs__fetch", true, { icon: "plug", verb: "Calling docs fetch" }],
    ["SomethingNew", false, { icon: "sparkle", verb: "SomethingNew" }],
  ];
  for (const [tool, isRunning, want] of cases) assert.deepEqual(callWords(tool, isRunning), want, tool + (isRunning ? " running" : ""));
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
    [["mcp__docs__fetch", "mcp__docs__fetch"], "Called docs fetch 2 times"],
    [["SomethingNew", "TodoWrite"], "Used SomethingNew 1 time, updated the plan 1 time"],
  ];
  for (const [tools, want] of cases) assert.equal(callTally(tools), want, tools.join(" "));
});
