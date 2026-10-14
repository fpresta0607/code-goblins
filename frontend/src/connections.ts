import { parseBrowserLogins } from "./browser-logins.ts";
import { array, object, string, strings } from "./types.ts";

export function parseConnections(value: unknown) {
  const result = object(value);
  return {
    instance: string(result.instance),
    checking: result.checking === true,
    error: string(result.error),
    checkedAt: string(result.checked_at),
    entries: array(result.entries).map((value) => {
      const entry = object(value);
      const grant = string(entry.grant);
      return { id: string(entry.id), name: string(entry.name), kind: string(entry.kind), status: string(entry.status), detail: string(entry.detail), source: string(entry.source), actions: strings(entry.actions), grant: grant === "grant" || grant === "revoke" ? grant : "" };
    }),
    browserLogins: parseBrowserLogins(result.browser_logins),
  };
}

export type ConnectionEntry = ReturnType<typeof parseConnections>["entries"][number];

export function connectionStatus(status: string): string {
  return ({ connected: "Connected", provided: "Provided", unauthorized: "Sign in", expired: "Expired", missing: "Missing", wrong_target: "Wrong project", unreachable: "Unreachable", failed: "Failed", unverified: "Unverified", disabled: "Disabled", withheld: "Withheld", skipped: "Optional", checking: "Checking" } as Record<string, string>)[status] || "Unverified";
}

// connectionTrouble is what a connector that is failing says in its faint
// mark's tooltip: its state and its reason. One that works, is being checked,
// is optional or is not carried has none.
export function connectionTrouble(entry: ConnectionEntry): string {
  if (["connected", "provided", "withheld", "skipped", "checking"].includes(entry.status)) return "";
  return connectionStatus(entry.status) + (entry.detail ? ": " + entry.detail : "");
}

export function connectionAction(action: string, name: string): string {
  if (action.startsWith("store:")) return "Store " + action.slice(6) + " from clipboard";
  return (action === "cli" ? "Open CLI sign-in for " : "Sign in to ") + name;
}
