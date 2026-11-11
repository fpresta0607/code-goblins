import { test } from "node:test";
import assert from "node:assert/strict";
import { askKey, cliVariables, goblinKey, listConnectors, matchesConnector, toolGroups, usageState } from "./connector-list.ts";
import { parseConnections } from "./connections.ts";

const entries = (...list: object[]) => parseConnections({ entries: list }).entries;
const ZEKE = goblinKey({ id: "cg-touch", generation: "cg-touch-1" });
const IVY = goblinKey({ id: "cg-other", generation: "cg-other-1" });
const withheld = { id: "withheld:supabase", name: "supabase", kind: "mcp", status: "withheld", grant: "grant" };
const UNSEEN = "Not checked: whether the goblin's own session can use it, or which account and project it reaches.";

test("a connector two goblins report is listed once, by its proper name, with each goblin's use of it", () => {
  // Arrange
  const readings = [
    { goblin: ZEKE, entries: entries({ id: "mcp:claude.ai Supabase", name: "claude.ai Supabase", kind: "mcp", status: "connected" }, { id: "service:github", name: "github", kind: "service", status: "connected", grant: "revoke" }) },
    { goblin: IVY, entries: entries({ id: "mcp:claude.ai Supabase", name: "claude.ai Supabase", kind: "mcp", status: "connected" }, { id: "service:github", name: "github", kind: "service", status: "withheld", grant: "grant" }) },
  ];

  // Act
  const connectors = listConnectors(readings);

  // Assert
  assert.deepEqual(connectors.map(({ name, type, status, word }) => [name, type, status, word]), [["GitHub", "Repository service", "verified", "Verified"], ["Supabase", "Claude MCP connector", "verified", "Verified"]]);
  assert.deepEqual(connectors[0].uses.map((use) => [use.goblin, use.isCarried]), [[ZEKE, true], [IVY, false]]);
  assert.equal(connectors[1].reported, "claude.ai Supabase");
});

test("a connector's type says whether it reaches a goblin as an MCP server or a CLI variable, and the same name of another kind is its own connector", () => {
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
    ["Crawler", "Built in"], ["Supabase", "Claude MCP connector"], ["Supabase", "MCP server"], ["Supabase", "Plugin MCP server"], ["Supabase", "Repository service"], ["SUPABASE_ACCESS_TOKEN", "CLI variable"],
  ]);
});

// The Overlord, 2026-11-11: "clean up the sign in and amber alerts to make it
// much more simple and user friendly especially with verification" (#2358).
test("a row's state is one word, Verified only when every goblin's check confirmed it, amber only for trouble, and never better than any goblin's check", () => {
  const docs = (status: string, more: object = {}) => ({ id: "mcp:docs", name: "docs", kind: "mcp", status, ...more });
  const cases: [string, object[][], string, string][] = [
    ["every goblin's check confirmed it", [[docs("connected")], [docs("probe_succeeded")]], "verified", "Verified"],
    ["one goblin's needs a sign-in", [[docs("connected")], [docs("unauthorized", { detail: "Sign-in required." })]], "trouble", "Sign in"],
    ["one goblin's has expired", [[docs("expired")]], "trouble", "Expired"],
    ["a check ran and did not confirm it", [[docs("unverified")]], "trouble", "Unverified"],
    ["a status the board does not know", [[docs("mystery")]], "trouble", "Unknown"],
    ["one is still being checked", [[docs("connected")], [docs("checking")]], "quiet", "Checking"],
    ["no check could run", [[docs("unchecked", { detail: "Not checked: the credential store could not be opened." })]], "quiet", "Not checked"],
    ["its harness has it disabled", [[docs("disabled")]], "quiet", "Disabled"],
    ["a variable the goblin's terminal holds", [[{ id: "credential:A_TOKEN", name: "A_TOKEN", kind: "credential", status: "provided" }]], "quiet", "Set"],
    ["a service that resolves and that no check confirms", [[{ id: "service:docs", name: "docs", kind: "service", status: "provided", grant: "revoke" }]], "quiet", "Set"],
    ["one goblin's is confirmed and another's only set", [[{ id: "service:docs", name: "docs", kind: "service", status: "connected", grant: "revoke" }], [{ id: "service:docs", name: "docs", kind: "service", status: "provided", grant: "revoke" }]], "quiet", "Set"],
    ["the crawler on a goblin's PATH", [[{ id: "connector:crawler", name: "crawler", kind: "connector", status: "provided" }]], "quiet", "Installed"],
    ["no goblin carries it", [[withheld], [withheld]], "available", ""],
    ["the one that fails is not carried", [[{ id: "service:docs", name: "docs", kind: "service", status: "withheld", detail: "Not granted.", grant: "grant" }], [{ id: "service:docs", name: "docs", kind: "service", status: "connected", grant: "revoke" }]], "verified", "Verified"],
  ];
  for (const [name, goblins, status, word] of cases) {
    // Act
    const [connector] = listConnectors(goblins.map((list, index) => ({ goblin: String(index), entries: entries(...list) })));

    // Assert
    assert.deepEqual([connector.status, connector.word], [status, word], name);
  }
});

test("a row waits for a goblin's next terminal when any goblin's change does, carried or not", () => {
  // Arrange
  const waiting = { id: "service:docs", name: "docs", kind: "service", status: "withheld", grant: "grant", restart_required: true, restart_reason: "Disconnected. Its variables leave this goblin at its next terminal; the one running still has them." };
  const settled = { id: "service:docs", name: "docs", kind: "service", status: "connected", grant: "revoke" };

  // Act
  const [taken] = listConnectors([{ goblin: ZEKE, entries: entries(waiting) }, { goblin: IVY, entries: entries(settled) }]);
  const [kept] = listConnectors([{ goblin: IVY, entries: entries(settled) }]);

  // Assert
  assert.equal(taken.isWaiting, true);
  assert.equal(kept.isWaiting, false);
});

test("what he just asked of a goblin's connector shows at once, for that goblin alone", () => {
  // Arrange
  const readings = [{ goblin: ZEKE, entries: entries(withheld) }, { goblin: IVY, entries: entries(withheld) }];

  // Act
  const [given] = listConnectors(readings, new Map([[askKey(ZEKE, "withheld:supabase"), true]]));
  const [taken] = listConnectors([{ goblin: ZEKE, entries: entries({ ...withheld, id: "mcp:supabase", status: "connected", grant: "revoke" }) }], new Map([[askKey(ZEKE, "mcp:supabase"), false]]));

  // Assert
  assert.deepEqual(given.uses.map((use) => use.isCarried), [true, false]);
  assert.deepEqual([given.status, given.word], ["quiet", "Checking"], "a connector just given waits for its check");
  assert.deepEqual(given.uses.map((use) => usageState(use).word), ["Checking", "Not connected"]);
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
  for (const [search, want] of [["", true], ["  ", true], ["supa", true], ["SUPABASE", true], ["claude.ai", true], ["mcp", true], ["connector", true], ["github", false]] as const) {
    assert.equal(matchesConnector(connector, search), want, JSON.stringify(search));
  }
});

test("what a goblin's use says: not connected, or its state in a word with its reason in plain words", () => {
  const cases: [object, { tone: string; word: string; reason: string }][] = [
    [{ id: "service:a", name: "a", kind: "service", status: "withheld", grant: "grant" }, { tone: "quiet", word: "Not connected", reason: "" }],
    [{ id: "mcp:a", name: "a", kind: "mcp", status: "checking" }, { tone: "quiet", word: "Checking", reason: "" }],
    [{ id: "mcp:a", name: "a", kind: "mcp", status: "unauthorized", detail: "Sign-in required." }, { tone: "trouble", word: "Sign in", reason: "Sign-in required." }],
    [{ id: "mcp:a", name: "a", kind: "mcp", status: "probe_succeeded", scope: "separate_probe" }, { tone: "verified", word: "Verified", reason: "A separate check with its credentials succeeded. " + UNSEEN }],
    [{ id: "credential:A", name: "A", kind: "credential", status: "provided", scope: "launch_environment" }, { tone: "quiet", word: "Set", reason: "The goblin's environment has it. " + UNSEEN }],
    [{ id: "service:a", name: "a", kind: "service", status: "provided", detail: "Credentials resolved. This account declares no connection check." }, { tone: "quiet", word: "Set", reason: "Credentials resolved. This account declares no connection check." }],
    [{ id: "service:a", name: "a", kind: "service", status: "unchecked", detail: "Not checked: the credential store could not be opened." }, { tone: "quiet", word: "Not checked", reason: "Not checked: the credential store could not be opened." }],
  ];
  for (const [entry, want] of cases) assert.deepEqual(usageState(listConnectors([{ goblin: ZEKE, entries: entries(entry) }])[0].uses[0]), want, JSON.stringify(entry));
});

test("a service's CLI variables are those its manifest declares, by name, with what each goblin's terminal holds of them, never a value", () => {
  // Arrange
  const service = { id: "service:repo", name: "repo", kind: "service", status: "provided", grant: "revoke" };
  const readings = [
    { goblin: "Zeke", entries: entries(service, { id: "credential:REPO_URL", name: "REPO_URL", kind: "credential", status: "provided", account: "repo" }, { id: "credential:REPO_TOKEN", name: "REPO_TOKEN", kind: "credential", status: "missing", account: "repo" }, { id: "credential:OTHER_TOKEN", name: "OTHER_TOKEN", kind: "credential", status: "provided", account: "other" }) },
    { goblin: "Ivy", entries: entries({ ...service, status: "withheld", grant: "grant" }, { id: "credential:REPO_URL", name: "REPO_URL", kind: "credential", status: "withheld", account: "repo" }) },
  ];
  const [connector] = listConnectors(readings).filter((found) => found.kind === "service");

  // Act
  const variables = cliVariables(connector, readings);

  // Assert
  assert.deepEqual(variables, [
    { name: "REPO_TOKEN", states: [{ goblin: "Zeke", word: "Not set" }] },
    { name: "REPO_URL", states: [{ goblin: "Zeke", word: "Set" }, { goblin: "Ivy", word: "Not given" }] },
  ]);
  const [server] = listConnectors([{ goblin: "Zeke", entries: entries({ id: "mcp:repo", name: "repo", kind: "mcp", status: "connected" }) }]);
  assert.deepEqual(cliVariables(server, readings), []);
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
