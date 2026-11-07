import { parseBrowserLogins } from "./browser-logins.ts";
import { array, object, string, strings } from "./types.ts";

export function parseConnections(value: unknown) {
  const result = object(value);
  const runtime = object(result.runtime ?? {});
  return {
    generation: string(result.generation),
    runtime: { processId: typeof runtime.process_id === "number" ? runtime.process_id : 0, startedAt: string(runtime.started_at), workspace: string(runtime.workspace) },
    instance: string(result.instance),
    checking: result.checking === true,
    error: string(result.error),
    checkedAt: string(result.checked_at),
    entries: array(result.entries).map((value) => {
      const entry = object(value);
      const grant = string(entry.grant);
      return { id: string(entry.id), name: string(entry.name), kind: string(entry.kind), status: string(entry.status), detail: string(entry.detail), source: string(entry.source), actions: strings(entry.actions), grant: grant === "grant" || grant === "revoke" ? grant : "", scope: string(entry.scope), liveStatus: string(entry.live_status) || "unknown", accountIdentity: string(entry.account_identity) || "unknown", targetIdentity: string(entry.target_identity) || "unknown", checkedAt: string(entry.checked_at), isRestartRequired: entry.restart_required === true, restartReason: string(entry.restart_reason) };
    }),
    browserLogins: parseBrowserLogins(result.browser_logins),
  };
}

export type ConnectionEntry = ReturnType<typeof parseConnections>["entries"][number];

export function connectionStatus(status: string): string {
  return ({ connected: "Connected", probe_succeeded: "Probe succeeded", provided: "Provided", unauthorized: "Sign in", expired: "Expired", missing: "Missing", wrong_target: "Wrong project", unreachable: "Unreachable", failed: "Failed", unverified: "Unverified", disabled: "Disabled", withheld: "Withheld", skipped: "Optional", checking: "Checking" } as Record<string, string>)[status] || "Unverified";
}

// connectionTrouble is what a connector that is failing says in its faint
// mark's tooltip: its state and its reason. One that works, is being checked,
// is optional or is not carried has none.
export function connectionTrouble(entry: ConnectionEntry): string {
  if (["connected", "probe_succeeded", "provided", "withheld", "skipped", "checking"].includes(entry.status)) return "";
  return connectionStatus(entry.status) + (entry.detail ? ": " + entry.detail : "");
}

export function connectionEvidence(entry: ConnectionEntry): string {
  const scope = ({ separate_probe: "Separate probe", launch_environment: "Observed process environment", configuration: "Configuration" } as Record<string, string>)[entry.scope] || "Observation scope unknown";
  const outcome = entry.status === "probe_succeeded" ? " succeeded" : "";
  return scope + outcome + ". Live session unknown. Account and target identity unknown.";
}

export function connectionAction(action: string, name: string): string {
  if (action.startsWith("store:")) return "Store " + action.slice(6) + " from clipboard";
  return (action === "cli" ? "Open CLI sign-in for " : "Sign in to ") + name;
}
