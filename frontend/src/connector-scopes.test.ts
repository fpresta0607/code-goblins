import { test } from "node:test";
import assert from "node:assert/strict";
import { allRows } from "./connector-rows.ts";
import { carriersWords, goblinScope, listWords, pcScope, projectScope, treeProjects, yoursCount } from "./connector-scopes.ts";
import { parseConnectorSnapshot } from "./connector-snapshot.ts";

const AT = "2026-11-25T10:00:00Z";
const item = (level: string, group: string, kind: string, name: string, status: string, extra: object = {}) => ({ id: kind + ":" + name, level, group, kind, name, status, checked_at: AT, ...extra });
const services = (count: number, troubled: number[] = []) => Array.from({ length: count }, (_, at) => item("project", "service", "service", "service-" + (at + 1), troubled.includes(at + 1) ? "missing" : "set"));
const project = (name: string, items: object[]) => ({ name, path: "C:/work/" + name, checked_at: AT, items });
const goblin = (task: string, name: string, items: object[]) => ({ task, generation: task + "-1", project: name, harness: "claude", is_confirmed: true, checked_at: AT, items });
const snapshot = () => parseConnectorSnapshot({
  machine: {
    checked_at: AT, items: [
      item("pc", "cli", "cli", "gh", "verified"), item("pc", "cli", "cli", "vercel", "set"),
      item("pc", "environment", "credential", "ACME_API_KEY", "everywhere"),
      item("pc", "built_in", "connector", "claude-in-chrome", "set"),
      item("account", "harness", "harness", "claude", "verified"), item("account", "harness", "harness", "codex", "set"),
      item("account", "account_connector", "connector", "claude.ai Supabase", "not_given"),
      item("account", "user_config", "mcp", "notes", "not_given"),
    ],
  },
  projects: [
    project("quiet-1", services(2)),
    project("acme", [...services(8, [7, 8]), item("project", "service", "service", "supabase", "set"), item("project", "mcp", "mcp", "supabase", "not_given"), item("project", "mcp", "mcp", "docs", "failed")]),
    project("quiet-2", []),
    project("globex", services(1)),
    project("quiet-3", services(3, [2])),
    project("quiet-4", services(1)),
    { name: "fresh", path: "C:/work/fresh", checked_at: "0001-01-01T00:00:00Z", items: [] },
  ],
  goblins: [
    goblin("t-1", "acme", [item("goblin", "carried", "service", "service-1", "set", { grant: "revoke" }), item("goblin", "carried", "service", "supabase", "set", { grant: "revoke" }), item("goblin", "not_given", "service", "service-2", "not_given", { grant: "grant" }), item("goblin", "loaded", "mcp", "search", "verified"), item("goblin", "failed", "mcp", "docs", "failed")]),
    goblin("t-2", "acme", [item("goblin", "carried", "service", "service-1", "set", { grant: "revoke" })]),
    goblin("t-3", "globex", []),
  ],
});
const nameOf = (task: string) => ({ "t-1": "Zeke", "t-2": "Ivy", "t-3": "Fern" })[task] ?? task;

test("This PC counts what every goblin gets, the harness sign-ins with it, and leaves your own Claude and Codex out", () => {
  // Arrange
  const view = snapshot();

  // Act
  const pc = pcScope(view);

  // Assert
  assert.equal(pc.total, 6);
  assert.equal(pc.words, "2 tools, 2 harness sign-ins, 1 credential and 1 built-in");
  assert.deepEqual(pc.groups.map((group) => [group.title, group.count]), [["Command-line tools", 2], ["Harness sign-ins", 2], ["User environment credentials", 1], ["Built in to every goblin", 1]]);
  assert.equal(yoursCount(view), 2);
});

test("the tree lists projects with a goblin first, each with what it keeps private and what its repository holds, its goblins under it with what each carries", () => {
  // Arrange
  const view = snapshot();

  // Act
  const { shown, more } = treeProjects(view, nameOf);

  // Assert
  assert.deepEqual(shown.map((each) => [each.name, each.about, each.isTroubled]), [
    ["acme", "9 private · 2 in repo", true], ["globex", "1 private", false], ["quiet-1", "2 private", false], ["quiet-2", "0 private", false],
  ]);
  assert.deepEqual(shown[0].goblins.map((each) => [each.name, each.carries, each.isTroubled]), [["Zeke", 2, true], ["Ivy", 1, false]]);
  assert.equal(more, 3);
});

test("the tree lists every project with a goblin, past the four it shows at least, and says a project not checked yet", () => {
  // Arrange
  const view = snapshot();
  view.goblins.push(...["quiet-1", "quiet-2", "quiet-3", "fresh"].map((name, at) => ({ ...view.goblins[2], task: "t-" + (at + 4), project: name })));

  // Act
  const { shown, more } = treeProjects(view, nameOf);

  // Assert
  assert.deepEqual(shown.map((each) => each.name), ["quiet-1", "acme", "quiet-2", "globex", "quiet-3", "fresh"]);
  assert.equal(shown[5].about, "not checked yet");
  assert.equal(more, 1);
});

test("a project's page puts its repository's servers apart from its private services, trouble first, says who carries each and folds the quiet ones past six", () => {
  // Arrange
  const view = snapshot();
  const rows = allRows(view, nameOf);

  // Act
  const { servers, services: shown, folded } = projectScope(view, rows, "acme", nameOf);

  // Assert
  assert.deepEqual(servers.map((row) => [row.name, row.word, row.about]), [["Docs", "Failed", ""], ["Supabase", "Not given", "also a service below"]]);
  assert.deepEqual(shown.map((row) => [row.name, row.word, row.about]), [
    ["Service 7", "Missing", "no goblin carries it"], ["Service 8", "Missing", "no goblin carries it"],
    ["Service 1", "Set", "Zeke, Ivy carry it"], ["Service 2", "Set", "no goblin carries it"], ["Service 3", "Set", "no goblin carries it"], ["Service 4", "Set", "no goblin carries it"],
  ]);
  assert.deepEqual(folded.map((row) => [row.name, row.about]), [["Service 5", "no goblin carries it"], ["Service 6", "no goblin carries it"], ["Supabase", "Zeke carries it · also a server above"]]);
});

test("a project shows every service that needs a look even past six", () => {
  // Arrange
  const view = parseConnectorSnapshot({ machine: { checked_at: AT, items: [] }, projects: [project("busy", services(9, [1, 2, 3, 4, 5, 6, 7]))], goblins: [] });

  // Act
  const { services: shown, folded } = projectScope(view, allRows(view, nameOf), "busy", nameOf);

  // Assert
  assert.equal(shown.length, 7);
  assert.deepEqual(folded.map((row) => row.name), ["Service 8", "Service 9"]);
});

test("a goblin's page parts what it is granted, what it is not and what its session holds, its failures first", () => {
  // Arrange
  const rows = allRows(snapshot(), nameOf);

  // Act
  const { granted, notGiven, session } = goblinScope(rows, "t-1");

  // Assert
  assert.deepEqual(granted.map((row) => [row.name, row.grant]), [["Service 1", "revoke"], ["Supabase", "revoke"]]);
  assert.deepEqual(notGiven.map((row) => [row.name, row.grant]), [["Service 2", "grant"]]);
  assert.deepEqual(session.map((row) => [row.name, row.word]), [["Docs", "Failed to load"], ["Search", "Loaded"]]);
});

for (const [names, words] of [[[], "no goblin carries it"], [["Zeke"], "Zeke carries it"], [["Zeke", "Ivy", "Fern"], "Zeke, Ivy, Fern carry it"]] as const) {
  test(`carriers of ${names.length} goblins read "${words}"`, () => {
    assert.equal(carriersWords([...names]), words);
  });
}

for (const [parts, words] of [[[], ""], [["one"], "one"], [["one", "two"], "one and two"], [["one", "two", "three"], "one, two and three"]] as const) {
  test(`a list of ${parts.length} reads "${words}"`, () => {
    assert.equal(listWords([...parts]), words);
  });
}
