import { ago } from "./browser-logins.ts";
import type { ConnectorItem, ConnectorLevel } from "./connector-snapshot.ts";

// A status's tone: verified, the only green, only when a probe, a sign-in check
// or the session itself confirmed it; trouble, amber, where the Overlord must
// act or a check failed; and quiet for every other status (the Overlord,
// 2026-11-11, #2358, kept by the drill-down, #2902).
export type ConnectorTone = "verified" | "trouble" | "quiet";

const TROUBLE = new Set(["missing", "unauthorized", "expired", "wrong_target", "unreachable", "failed", "everywhere", "not_checked"]);

export function statusTone(status: string): ConnectorTone {
  if (status === "verified") return "verified";
  return TROUBLE.has(status) ? "trouble" : "quiet";
}

export const LEVEL_WORDS: Record<ConnectorLevel, string> = { pc: "This PC", account: "Account", project: "Project", goblin: "Goblin" };

// How long a level's check is trusted before the supervisor rechecks it and
// the page marks it Stale. A goblin is read from its record, so it never is.
const HOUR_MS = 60 * 60_000;
const LEVEL_LIFE_MS: Partial<Record<ConnectorLevel, number>> = { pc: 6 * HOUR_MS, account: 6 * HOUR_MS, project: HOUR_MS };

// Go writes its zero time, year 1, for a check that has not ended yet.
export function hasChecked(checkedAt: string): boolean {
  return Date.parse(checkedAt) > 0;
}

export function isStale(level: ConnectorLevel, checkedAt: string, now: number): boolean {
  const life = LEVEL_LIFE_MS[level];
  return !!life && hasChecked(checkedAt) && now - Date.parse(checkedAt) > life;
}

export function checkedWords(checkedAt: string, now: number): string {
  return hasChecked(checkedAt) ? "checked " + ago(checkedAt, now) : "not checked yet";
}

// An item's status in a word, which depends on what it is: a tool on the PC is
// Installed, a server its goblin loaded is Loaded, one it failed to load Failed
// to load.
export function statusWord(item: Pick<ConnectorItem, "status" | "group" | "kind">): string {
  switch (item.status) {
    case "verified": return item.group === "loaded" ? "Loaded" : "Verified";
    case "set":
      if (item.group === "carried") return "Carried";
      return item.kind === "cli" || item.kind === "harness" ? "Installed" : item.kind === "connector" ? "Built in" : "Set";
    case "missing": return "Missing";
    case "unauthorized": return "Sign in";
    case "expired": return "Expired";
    case "wrong_target": return "Wrong target";
    case "unreachable": return "Unreachable";
    case "failed": return item.group === "failed" ? "Failed to load" : "Failed";
    case "not_given": return "Not given";
    case "not_checked": return "Not checked";
    case "everywhere": return "Every goblin has it";
    default: return "Unknown";
  }
}

// What a repair's button says, and what it is called to a screen reader.
export function repairWords(action: string): string {
  if (action.startsWith("store:")) return "Store " + action.slice(6) + " from clipboard";
  return action === "cli" ? "CLI sign-in" : "Sign in";
}

export function repairLabel(action: string, name: string): string {
  if (action.startsWith("store:")) return repairWords(action);
  return (action === "cli" ? "Open CLI sign-in for " : "Sign in to ") + name;
}
