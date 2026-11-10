import { test } from "node:test";
import assert from "node:assert/strict";
import { askKey, goblinKey, listConnectors, matchesConnector, toolGroups, usageWords } from "./connector-list.ts";
import { parseConnections } from "./connections.ts";

const entries = (...list: object[]) => parseConnections({ entries: list }).entries;
const ZEKE = goblinKey({ id: "cg-touch", generation: "cg-touch-1" });
const IVY = goblinKey({ id: "cg-other", generation: "cg-other-1" });
const withheld = { id: "withheld:supabase", name: "supabase", kind: "mcp", status: "withheld", grant: "grant" };

test("a connector two goblins report is listed once, by its proper name, with each goblin's use of it", () => {
  // Arrange
  const readings = [
    { goblin: ZEKE, entries: entries({ id: "mcp:claude.ai Supabase", name: "claude.ai Supabase", kind: "mcp", status: "connected" }, { id: "service:github", name: "github", kind: "service", status: "connected", grant: "revoke" }) },
    { goblin: IVY, entries: entries({ id: "mcp:claude.ai Supabase", name: "claude.ai Supabase", kind: "mcp", status: "connected" }, { id: "service:github", name: "github", kind: "service", status: "withheld", grant: "grant" }) },
  ];

  // Act
  const connectors = listConnectors(readings);

  // Assert
  assert.deepEqual(connectors.map(({ name, type, status }) => [name, type, status]), [["GitHub", "Repository service", "connected"], ["Supabase", "Claude connector", "connected"]]);
  assert.deepEqual(connectors[0].uses.map((use) => [use.goblin, use.isCarried]), [[ZEKE, true], [IVY, false]]);
  assert.equal(connectors[1].reported, "claude.ai Supabase");
});

test("a connector's type says how it reaches a goblin, and the same name of another kind is its own connector", () => {
  // Arrange
  const reading = { goblin: ZEKE, entries: entries(
    { id: "mcp:supabase", name: "supabase", kind: "mcp", status: "connected" },
    { id: "mcp:claude.ai Supabase", name: "claude.ai Supabase", kind: "mcp", status: "connected" },
    { id: "mcp:plugin:supabase:supabase", name: "plugin:supabase:supabase", kind: "mcp", status: "connected" },
    { id: "service:supabase", name: "supabase", kind: "service", status: "connected", grant: "revoke" },
    { id: "connector:crawler", name: "crawler", kind: "connector", status: "provided" },
    { id: "credential:SUPABASE_ACCESS_TOKEN", name: "SUPABASE_ACCESS_TOKEN", kind: "credential", status: "provided" },
  ) };

  // Act
  const connectors = listConnectors([reading]);

  // Assert
  assert.deepEqual(connectors.map(({ name, type }) => [name, type]), [
    ["Crawler", "Built in"], ["Supabase", "Claude connector"], ["Supabase", "MCP server"], ["Supabase", "Plugin"], ["Supabase", "Repository service"], ["SUPABASE_ACCESS_TOKEN", "Credential"],
  ]);
});

test("a connector is yours once a goblin carries it, in trouble when it fails for one, and on offer when none does", () => {
  const cases: [string, object[][], string, string][] = [
    ["every goblin's works", [[{ id: "mcp:docs", name: "docs", kind: "mcp", status: "connected" }], [{ id: "mcp:docs", name: "docs", kind: "mcp", status: "probe_succeeded" }]], "connected", ""],
    ["one goblin's needs a sign-in", [[{ id: "mcp:docs", name: "docs", kind: "mcp", status: "connected" }], [{ id: "mcp:docs", name: "docs", kind: "mcp", status: "unauthorized", detail: "Sign-in required." }]], "trouble", "Sign in: Sign-in required."],
    ["one is still being checked", [[{ id: "mcp:docs", name: "docs", kind: "mcp", status: "checking" }]], "checking", ""],
    ["no goblin carries it", [[withheld], [withheld]], "available", ""],
    ["the one that fails is not carried", [[{ id: "service:docs", name: "docs", kind: "service", status: "withheld", detail: "Not granted.", grant: "grant" }], [{ id: "service:docs", name: "docs", kind: "service", status: "connected", grant: "revoke" }]], "connected", ""],
  ];
  for (const [name, goblins, status, trouble] of cases) {
    const [connector] = listConnectors(goblins.map((list, index) => ({ goblin: String(index), entries: entries(...list) })));
    assert.equal(connector.status, status, name);
    assert.equal(connector.trouble, trouble, name);
  }
  assert.equal(listConnectors([{ goblin: ZEKE, entries: entries({ id: "mcp:docs", name: "docs", kind: "mcp", status: "unauthorized" }) }])[0].troubleWord, "Sign in");
});

test("what he just asked of a goblin's connector shows at once, for that goblin alone", () => {
  // Arrange
  const readings = [{ goblin: ZEKE, entries: entries(withheld) }, { goblin: IVY, entries: entries(withheld) }];

  // Act
  const [given] = listConnectors(readings, new Map([[askKey(ZEKE, "withheld:supabase"), true]]));
  const [taken] = listConnectors([{ goblin: ZEKE, entries: entries({ ...withheld, id: "mcp:supabase", status: "connected", grant: "revoke" }) }], new Map([[askKey(ZEKE, "mcp:supabase"), false]]));

  // Assert
  assert.deepEqual(given.uses.map((use) => use.isCarried), [true, false]);
  assert.equal(given.status, "checking", "a connector just given waits for its check");
  assert.deepEqual(given.uses.map(usageWords), ["Checking", "Not connected"]);
  assert.equal(taken.status, "available");
});

test("a variable no goblin carries is left out, and one a goblin carries is listed by its own name", () => {
  // Arrange
  const readings = [{ goblin: ZEKE, entries: entries(
    { id: "credential:GITHUB_TOKEN", name: "GITHUB_TOKEN", kind: "credential", status: "withheld" },
    { id: "credential:OPENROUTER_API_KEY", name: "OPENROUTER_API_KEY", kind: "credential", status: "provided" },
  ) }];

  // Act and assert
  assert.deepEqual(listConnectors(readings).map((connector) => connector.name), ["OPENROUTER_API_KEY"]);
});

test("a search finds a connector by its name as shown or as reported, or by its type, without case", () => {
  // Arrange
  const [connector] = listConnectors([{ goblin: ZEKE, entries: entries({ id: "mcp:claude.ai Supabase", name: "claude.ai Supabase", kind: "mcp", status: "connected" }) }]);

  // Act and assert
  for (const [search, want] of [["", true], ["  ", true], ["supa", true], ["SUPABASE", true], ["claude.ai", true], ["connector", true], ["github", false]] as const) {
    assert.equal(matchesConnector(connector, search), want, JSON.stringify(search));
  }
});

test("what a goblin's use says: not connected, its trouble, what a probe proves, or its state", () => {
  const cases: [object, string][] = [
    [{ id: "service:a", name: "a", kind: "service", status: "withheld", grant: "grant" }, "Not connected"],
    [{ id: "mcp:a", name: "a", kind: "mcp", status: "connected" }, "Connected"],
    [{ id: "mcp:a", name: "a", kind: "mcp", status: "checking" }, "Checking"],
    [{ id: "mcp:a", name: "a", kind: "mcp", status: "unauthorized", detail: "Sign-in required." }, "Sign in: Sign-in required."],
    [{ id: "mcp:a", name: "a", kind: "mcp", status: "probe_succeeded", scope: "separate_probe" }, "Separate probe succeeded. Live session unknown. Account and target identity unknown."],
    [{ id: "credential:A", name: "A", kind: "credential", status: "provided" }, "Provided"],
  ];
  for (const [entry, want] of cases) assert.equal(usageWords(listConnectors([{ goblin: ZEKE, entries: entries(entry) }])[0].uses[0]), want, JSON.stringify(entry));
});

test("a connector's tools are grouped as its harness marks them, in words, each once", () => {
  // Arrange
  const tools = [{ name: "list_projects", read_only: true }, { name: "execute_sql", read_only: false }, { name: "supabase_get_project_url", read_only: true }, { name: "apply_migration" }];
  const readings = [ZEKE, IVY].map((goblin) => ({ goblin, entries: entries({ id: "mcp:claude.ai Supabase", name: "claude.ai Supabase", kind: "mcp", status: "connected", tools }) }));

  // Act
  const [connector] = listConnectors(readings);

  // Assert
  assert.deepEqual(toolGroups(connector), { readOnly: ["Get project URL", "List projects"], write: ["Apply migration", "Execute SQL"] });
  assert.deepEqual(toolGroups(listConnectors([{ goblin: ZEKE, entries: entries({ id: "service:github", name: "github", kind: "service", status: "connected" }) }])[0]), { readOnly: [], write: [] });
});
