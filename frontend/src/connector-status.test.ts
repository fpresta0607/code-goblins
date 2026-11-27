import { test } from "node:test";
import assert from "node:assert/strict";
import { crumbsTo } from "./connector-place.ts";
import { checkedWords, hasChecked, isStale, repairLabel, repairWords, scopeOf, statusTone, statusWord } from "./connector-status.ts";

const NOW = Date.parse("2026-11-25T12:00:00Z");
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString();

for (const [status, tone] of [
  ["verified", "verified"],
  ["missing", "trouble"], ["unauthorized", "trouble"], ["expired", "trouble"], ["wrong_target", "trouble"], ["unreachable", "trouble"], ["failed", "trouble"], ["everywhere", "trouble"],
  ["set", "quiet"], ["not_given", "quiet"], ["not_checked", "trouble"],
] as const) {
  test(`the status ${status} has the tone ${tone}`, () => {
    assert.equal(statusTone(status), tone);
  });
}

test("a status says what it is of in one word: a tool is Installed, a loaded server Loaded, a failed one Failed to load", () => {
  // Arrange
  const word = (status: string, group: string, kind: string) => statusWord({ status, group, kind });

  // Act and Assert
  assert.equal(word("verified", "cli", "cli"), "Verified");
  assert.equal(word("verified", "loaded", "mcp"), "Loaded");
  assert.equal(word("set", "cli", "cli"), "Installed");
  assert.equal(word("set", "built_in", "connector"), "Built in");
  assert.equal(word("set", "carried", "service"), "Carried");
  assert.equal(word("set", "service", "service"), "Set");
  assert.equal(word("failed", "failed", "mcp"), "Failed to load");
  assert.equal(word("failed", "service", "service"), "Failed");
  assert.equal(word("unauthorized", "service", "service"), "Sign in");
  assert.equal(word("everywhere", "environment", "credential"), "Every goblin has it");
  assert.equal(word("surprise", "service", "service"), "Unknown");
});

test("a project's check is stale after an hour, this PC's and the account's after six, and a goblin's never is", () => {
  // Arrange
  const stale = (level: "pc" | "account" | "project" | "goblin", minutes: number) => isStale(level, minutesAgo(minutes), NOW);

  // Act and Assert
  assert.equal(stale("project", 59), false);
  assert.equal(stale("project", 61), true);
  assert.equal(stale("pc", 61), false);
  assert.equal(stale("pc", 359), false);
  assert.equal(stale("pc", 361), true);
  assert.equal(stale("account", 361), true);
  assert.equal(stale("goblin", 10_000), false);
});

test("a check that never ended is not stale and says so, and Go's zero time is no time", () => {
  // Arrange
  const zero = "0001-01-01T00:00:00Z";

  // Act and Assert
  assert.equal(hasChecked(zero), false);
  assert.equal(hasChecked(""), false);
  assert.equal(hasChecked(minutesAgo(1)), true);
  assert.equal(isStale("project", zero, NOW), false);
  assert.equal(checkedWords(zero, NOW), "not checked yet");
});

test("the age of a check is told in minutes, hours and days", () => {
  // Arrange and Act
  const words = [0, 4, 61, 25 * 60].map((minutes) => checkedWords(minutesAgo(minutes), NOW));

  // Assert
  assert.deepEqual(words, ["checked just now", "checked 4 minutes ago", "checked 1 hour ago", "checked 1 day ago"]);
});

test("a repair's words name what it does, and a variable it stores", () => {
  // Arrange and Act and Assert
  assert.equal(repairWords("login"), "Sign in");
  assert.equal(repairWords("cli"), "CLI sign-in");
  assert.equal(repairWords("store:SUPABASE_KEY"), "Store SUPABASE_KEY from clipboard");
  assert.equal(repairLabel("login", "Supabase"), "Sign in to Supabase");
  assert.equal(repairLabel("cli", "GitHub"), "Open CLI sign-in for GitHub");
  assert.equal(repairLabel("store:SUPABASE_KEY", "Supabase"), "Store SUPABASE_KEY from clipboard");
});

test("a goblin sits under its project, which sits under Projects, in the breadcrumb", () => {
  // Arrange
  const goblin = { name: "Zeke - Touch Wrangler", project: "acme" };

  // Act
  const goblinCrumbs = crumbsTo({ kind: "goblin", task: "t-1" }, goblin).map((crumb) => crumb.label);
  const projectCrumbs = crumbsTo({ kind: "project", project: "acme" }, goblin).map((crumb) => crumb.label);
  const homeCrumbs = crumbsTo({ kind: "home" }, goblin).map((crumb) => crumb.label);

  // Assert
  assert.deepEqual(goblinCrumbs, ["Connectors", "Projects", "acme", "Zeke - Touch Wrangler"]);
  assert.deepEqual(projectCrumbs, ["Connectors", "Projects", "acme"]);
  assert.deepEqual(homeCrumbs, ["Connectors"]);
  for (const [kind, label] of [["look", "Needs a look"], ["pc", "This PC"], ["yours", "Your Claude and Codex"], ["projects", "Projects"]] as const) {
    assert.deepEqual(crumbsTo({ kind }, goblin).map((crumb) => crumb.label), ["Connectors", label]);
  }
});

for (const [level, group, scope] of [
  ["pc", "cli", "global"], ["pc", "environment", "global"], ["pc", "built_in", "global"], ["account", "harness", "global"],
  ["account", "account_connector", "yours"], ["account", "user_config", "yours"], ["project", "service", "local"], ["project", "mcp", "local"], ["goblin", "carried", "goblin"],
] as const) {
  test(`an item of the ${level} level in ${group} lives in the ${scope} scope`, () => {
    assert.equal(scopeOf({ level, group }), scope);
  });
}
