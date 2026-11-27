import type { ConnectorRow } from "./connector-rows.ts";

// What Remove would change for a row of Needs a look, said before he confirms
// it (#3002): the one declaration it edits and what it keeps, or, where Code
// Goblins does not declare the item, where it lives and that Remove leaves it.
export interface RemovePlan { words: string; canRemove: boolean }

export function removePlan(row: Pick<ConnectorRow, "level" | "group" | "name" | "logoName" | "project" | "origin" | "about">): RemovePlan {
  if (row.level === "pc" && row.group === "environment") {
    return {
      canRemove: true,
      words: "Deletes " + row.name + " from this PC's user environment, so every program started from now on no longer gets it: terminals and goblins, "
        + "and any other program that reads it, such as a server in your Claude settings. Programs already running keep it until they restart. "
        + "Its value is first kept in the credential vault under " + row.name + ". If the vault already holds another value under that name, nothing changes.",
    };
  }
  if (row.level === "project" && row.group === "service") {
    return {
      canRemove: true,
      words: "Takes the service " + row.logoName + " out of " + row.project + "'s auth.json, so its goblins no longer carry it. "
        + "The credentials stored for it stay in the vault, and its entry is kept in " + row.project + "'s auth.removed.json to put back.",
    };
  }
  if (row.level === "project" && row.group === "mcp") {
    return { canRemove: false, words: row.logoName + " is declared in " + row.project + "'s .mcp.json, a file of its repository, so Remove leaves it: change it there through a pull request." };
  }
  if (row.level === "goblin" && (row.group === "failed" || row.group === "loaded")) {
    return {
      canRemove: true,
      words: "Takes " + row.logoName + " out of the task record of " + row.origin + ", so it no longer loads from the goblin's next start, and its failure leaves this list. "
        + "Its worktree and every other connection stay. If every goblin of its project gets it from the project's .mcp.json, or it comes from your Claude settings, nothing changes.",
    };
  }
  return { canRemove: false, words: "Code Goblins does not declare " + row.name + (row.about ? ", " + row.about : "") + ", so Remove leaves it: change it where it lives." };
}
