import { test } from "node:test";
import assert from "node:assert/strict";
import { allRows, goblinRows, groupByGroup, groupByLevel, levelSummary, needsLook, oldestCheck, projectRows, searchRows } from "./connector-rows.ts";
import { isAnyChecking, parseConnectorSnapshot } from "./connector-snapshot.ts";

const AT = "2026-11-25T10:00:00Z";
const item = (level: string, group: string, kind: string, name: string, status: string, extra: object = {}) => ({ id: kind + ":" + name, level, group, kind, name, status, checked_at: AT, ...extra });
const snapshot = () => parseConnectorSnapshot({
  machine: {
    checked_at: AT, items: [
      item("pc", "cli", "cli", "gh", "verified", { detail: "Signed in", source: "gh auth status" }),
      item("pc", "cli", "cli", "gcloud", "not_checked", { detail: "Its status command did not answer in 20 s." }),
      item("pc", "environment", "credential", "ACME_API_KEY", "everywhere"),
      item("account", "account_connector", "connector", "claude.ai Supabase", "not_given"),
    ],
  },
  projects: [
    { name: "acme", path: "C:/work/acme", checked_at: AT, items: [
      item("project", "service", "service", "supabase", "set", { target: "Supabase project …k3tv", credentials: [{ name: "SUPABASE_URL", is_resolved: true }, { name: "SUPABASE_KEY", is_resolved: false }] }),
      item("project", "mcp", "mcp", "supabase", "not_given", { target: "Supabase project …k3tv" }),
    ] },
    { name: "globex", path: "C:/work/globex", checked_at: AT, items: [
      item("project", "service", "service", "supabase", "missing", { target: "Supabase project …q7wk", actions: ["store:SUPABASE_KEY"] }),
    ] },
    { name: "initech", path: "C:/work/initech", checked_at: "0001-01-01T00:00:00Z", checking: true, items: [] },
  ],
  goblins: [
    { task: "t-1", generation: "g-1", project: "acme", harness: "claude", is_confirmed: true, checked_at: AT, items: [
      item("goblin", "failed", "mcp", "acme-docs", "failed", { detail: "Connection closed" }),
      item("goblin", "carried", "service", "github", "set", { grant: "revoke" }),
      item("goblin", "loaded", "mcp", "acme-search", "verified", { tools: ["search_pages"] }),
    ] },
    { task: "t-2", generation: "g-2", project: "globex", harness: "codex", is_confirmed: false, note: "Codex does not record which servers loaded.", checked_at: AT, items: [] },
  ],
});
const nameOf = (task: string) => ({ "t-1": "Zeke - Touch Wrangler", "t-2": "Ivy - Window Painter" })[task] ?? task;

test("every row carries the level its item is of, and a project's or goblin's row carries where it is from", () => {
  // Arrange
  const rows = allRows(snapshot(), nameOf);

  // Act
  const found = rows.map((row) => [row.level, row.name, row.project, row.task, row.origin]);

  // Assert
  assert.deepEqual(found, [
    ["pc", "gh", "", "", ""], ["pc", "gcloud", "", "", ""], ["pc", "ACME_API_KEY", "", "", ""], ["account", "Supabase", "", "", ""],
    ["project", "Supabase", "acme", "", "acme"], ["project", "Supabase", "acme", "", "acme"], ["project", "Supabase", "globex", "", "globex"],
    ["goblin", "Acme Docs", "acme", "t-1", "Zeke - Touch Wrangler in acme"], ["goblin", "GitHub", "acme", "t-1", "Zeke - Touch Wrangler in acme"], ["goblin", "Acme Search", "acme", "t-1", "Zeke - Touch Wrangler in acme"],
  ]);
});

test("same-named services of two projects are two rows, each with its project and its own target", () => {
  // Arrange
  const rows = allRows(snapshot(), nameOf).filter((row) => row.level === "project" && row.group === "service");

  // Act
  const found = rows.map((row) => [row.name, row.project, row.target, row.key]);

  // Assert
  assert.deepEqual(found.map(([name, project, target]) => [name, project, target]), [["Supabase", "acme", "Supabase project …k3tv"], ["Supabase", "globex", "Supabase project …q7wk"]]);
  assert.equal(new Set(found.map(([, , , key]) => key)).size, 2);
});

test("two goblins of one project that carry the same item are two rows with their own keys", () => {
  // Arrange
  const view = snapshot();
  const twin = { ...view.goblins[0], task: "t-3" };

  // Act
  const rows = allRows({ ...view, goblins: [view.goblins[0], twin] }, nameOf).filter((row) => row.level === "goblin" && row.connection === "mcp:acme-docs");

  // Assert
  assert.deepEqual(rows.map((row) => [row.project, row.task]), [["acme", "t-1"], ["acme", "t-3"]]);
  assert.equal(new Set(rows.map((row) => row.key)).size, 2);
});

test("a search finds items at every level, the words in any order, and groups them by level", () => {
  // Arrange
  const rows = allRows(snapshot(), nameOf);

  // Act
  const byName = groupByLevel(searchRows(rows, "supabase"));
  const byProjectAndTarget = searchRows(rows, "K3TV supabase");
  const byGoblin = searchRows(rows, "zeke");
  const byCredential = searchRows(rows, "supabase_key");
  const byTool = searchRows(rows, "search_pages");

  // Assert
  assert.deepEqual(byName.map(({ level, rows: found }) => [level, found.map((row) => row.project)]), [["account", [""]], ["project", ["acme", "acme", "globex"]]]);
  assert.deepEqual(byProjectAndTarget.map((row) => [row.group, row.project]), [["service", "acme"], ["mcp", "acme"]]);
  assert.deepEqual(byGoblin.map((row) => row.name), ["Acme Docs", "GitHub", "Acme Search"]);
  assert.deepEqual(byCredential.map((row) => row.project), ["acme"]);
  assert.deepEqual(byTool.map((row) => row.level), ["goblin"]);
  assert.deepEqual(searchRows(rows, "   "), []);
  assert.deepEqual(searchRows(rows, "nothing like this"), []);
});

test("Needs a look holds the items in trouble from every level, a check that did not finish among them, and none that are only quiet", () => {
  // Arrange
  const rows = allRows(snapshot(), nameOf);

  // Act
  const trouble = needsLook(rows).map((row) => [row.level, row.name, row.word]);

  // Assert
  assert.deepEqual(trouble, [["pc", "gcloud", "Not checked"], ["pc", "ACME_API_KEY", "Every goblin has it"], ["project", "Supabase", "Missing"], ["goblin", "Acme Docs", "Failed to load"]]);
});

test("a level's rows are grouped in the order that level is read in", () => {
  // Arrange
  const rows = allRows(snapshot(), nameOf).filter((row) => row.level === "goblin");

  // Act
  const groups = groupByGroup(rows, "goblin").map((group) => [group.title, group.rows.length]);

  // Assert
  assert.deepEqual(groups, [["Failed to load", 1], ["Loaded by its session", 1], ["Carries", 1]]);
});

test("the projects and goblins lists say how much each holds, what needs a look and whether it was checked", () => {
  // Arrange
  const view = snapshot();

  // Act
  const projects = projectRows(view).map((row) => [row.name, row.about, row.word, row.tone, row.isChecking]);
  const goblins = goblinRows(view, nameOf).map((row) => [row.name, row.about, row.word, row.tone]);

  // Assert
  assert.deepEqual(projects, [
    ["acme", "1 service · 1 MCP server · 1 goblin", "Checked", "quiet", false],
    ["globex", "1 service · 1 goblin", "1 to look at", "trouble", false],
    ["initech", "0 services · no goblin running", "Not checked yet", "quiet", true],
  ]);
  assert.deepEqual(goblins, [
    ["Zeke - Touch Wrangler", "acme · claude", "1 server failed to load", "trouble"],
    ["Ivy - Window Painter", "globex · codex · not confirmed by its session", "As its project", "quiet"],
  ]);
});

test("the home tiles count what each level holds and what needs a look", () => {
  // Arrange
  const view = snapshot();

  // Act
  const summary = levelSummary(view);

  // Assert
  assert.equal(summary.pc, "2 CLI tools, 0 harness accounts, 1 account connector, 0 user-config servers · 2 to look at");
  assert.equal(summary.projects, "Services and MCP servers, each checked once per project · 1 to look at");
  assert.equal(summary.goblins, "What each session loaded · 1 server failed to load");
});

test("the snapshot says whether any level is being checked and when its oldest check ended", () => {
  // Arrange
  const view = snapshot();

  // Act and Assert
  assert.equal(isAnyChecking(view), true);
  assert.equal(oldestCheck(view), AT);
  assert.equal(isAnyChecking({ ...view, projects: view.projects.map((project) => ({ ...project, isChecking: false })) }), false);
  assert.equal(oldestCheck({ machine: { items: [], checkedAt: "0001-01-01T00:00:00Z", isChecking: false, error: "" }, projects: [], goblins: [] }), "");
});

test("a snapshot with a level that is not one of the four is refused rather than drawn", () => {
  // Arrange
  const broken = { machine: { items: [item("planet", "cli", "cli", "gh", "set")] }, projects: [], goblins: [] };

  // Act and Assert
  assert.throws(() => parseConnectorSnapshot(broken), /Invalid connector level/);
});
