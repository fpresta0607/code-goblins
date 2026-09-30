import { array, object, string, strings } from "./types";

export function parseConnections(value: unknown) {
  const result = object(value);
  return {
    instance: string(result.instance),
    checking: result.checking === true,
    error: string(result.error),
    checkedAt: string(result.checked_at),
    entries: array(result.entries).map((value) => {
      const entry = object(value);
      return { id: string(entry.id), name: string(entry.name), kind: string(entry.kind), status: string(entry.status), detail: string(entry.detail), source: string(entry.source), checkedAt: string(entry.checked_at), actions: strings(entry.actions) };
    }),
  };
}

export type ConnectionEntry = ReturnType<typeof parseConnections>["entries"][number];

export function connectionStatus(status: string): string {
  return ({ connected: "Connected", provided: "Provided", unauthorized: "Sign in", expired: "Expired", missing: "Missing", wrong_target: "Wrong project", unreachable: "Unreachable", failed: "Failed", unverified: "Unverified", disabled: "Disabled", withheld: "Withheld", skipped: "Optional", checking: "Checking" } as Record<string, string>)[status] || "Unverified";
}

export function connectionAction(action: string, name: string): string {
  if (action.startsWith("store:")) return "Store " + action.slice(6) + " from clipboard";
  return (action === "cli" ? "Open CLI sign-in for " : "Sign in to ") + name;
}

export function checkedTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) || date.getFullYear() < 2000 ? "Not checked yet" : "Checked " + date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
