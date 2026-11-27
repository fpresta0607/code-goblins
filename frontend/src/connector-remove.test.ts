import { test } from "node:test";
import assert from "node:assert/strict";
import { removePlan } from "./connector-remove.ts";

const row = (level: "pc" | "account" | "project" | "goblin", group: string, name: string, extra: { project?: string; origin?: string; about?: string } = {}) =>
  ({ level, group, name, project: extra.project ?? "", origin: extra.origin ?? "", about: extra.about ?? "" });

test("Remove's question names the one declaration it edits for each kind of item, and only what Code Goblins declares can be removed", () => {
  const cases: [ReturnType<typeof row>, boolean, string[]][] = [
    [row("pc", "environment", "ACME_API_KEY"), true, ["Deletes ACME_API_KEY from this PC's user environment", "every program started from now on no longer gets it", "a server in your Claude settings", "kept in the credential vault under ACME_API_KEY", "another value under that name, nothing changes"]],
    [row("project", "service", "Supabase", { project: "globex" }), true, ["Takes the service Supabase out of globex's auth.json", "stay in the vault", "globex's auth.removed.json"]],
    [row("project", "mcp", "docs", { project: "globex" }), false, ["declared in globex's .mcp.json", "through a pull request"]],
    [row("goblin", "failed", "Acme Docs", { origin: "Zeke in acme" }), true, ["Takes Acme Docs out of the task record of Zeke in acme", "next start", "Its worktree and every other connection stay"]],
    [row("pc", "cli", "gcloud", { about: "read from gcloud auth list" }), false, ["Code Goblins does not declare gcloud, read from gcloud auth list", "change it where it lives"]],
    [row("account", "harness", "claude"), false, ["Code Goblins does not declare claude, so Remove leaves it"]],
  ];
  for (const [item, canRemove, words] of cases) {
    const plan = removePlan(item);
    assert.equal(plan.canRemove, canRemove, item.name);
    for (const said of words) assert.ok(plan.words.includes(said), item.name + ": " + plan.words);
  }
});
