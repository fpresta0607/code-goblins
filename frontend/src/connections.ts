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
      // The tools an MCP server's harness listed, each by name with whether
      // it only reads.
      const tools = array(entry.tools).map((value) => {
        const tool = object(value);
        return { name: string(tool.name), isReadOnly: tool.read_only === true };
      });
      return { id: string(entry.id), name: string(entry.name), kind: string(entry.kind), status: string(entry.status), detail: string(entry.detail), source: string(entry.source), actions: strings(entry.actions), grant: grant === "grant" || grant === "revoke" ? grant : "", scope: string(entry.scope), accounts: strings(entry.accounts), liveStatus: string(entry.live_status) || "unknown", accountIdentity: string(entry.account_identity) || "unknown", targetIdentity: string(entry.target_identity) || "unknown", checkedAt: string(entry.checked_at), isRestartRequired: entry.restart_required === true, restartReason: string(entry.restart_reason), tools };
    }),
    browserLogins: parseBrowserLogins(result.browser_logins),
  };
}

export type ConnectionEntry = ReturnType<typeof parseConnections>["entries"][number];

export function connectionStatus(status: string): string {
  return ({ connected: "Connected", probe_succeeded: "Probe succeeded", provided: "Provided", unauthorized: "Sign in", expired: "Expired", missing: "Missing", wrong_target: "Wrong project", unreachable: "Unreachable", failed: "Failed", unverified: "Unverified", unchecked: "Not checked", disabled: "Disabled", withheld: "Withheld", skipped: "Optional", checking: "Checking" } as Record<string, string>)[status] || "Unknown";
}

// connectionTrouble is what a connector that is failing says: its state and
// its reason. A failing one is one he must act on or whose check failed, so
// one that works, is set, is being checked, is optional, disabled, not
// carried or could not be checked has none.
export function connectionTrouble(entry: ConnectionEntry): string {
  if (["connected", "probe_succeeded", "provided", "withheld", "skipped", "checking", "disabled", "unchecked"].includes(entry.status)) return "";
  return connectionStatus(entry.status) + (entry.detail ? ": " + entry.detail : "");
}

// connectionEvidence says in plain words where a verdict was seen and what
// it leaves unchecked: no check reads the live session or which account and
// project the credentials reach.
export function connectionEvidence(entry: ConnectionEntry): string {
  const where = ({ separate_probe: "A separate check with its credentials", launch_environment: "The goblin's environment", configuration: "Its configuration" } as Record<string, string>)[entry.scope];
  const seen = !where ? "How it was checked is unknown." : where + (entry.status === "probe_succeeded" ? " succeeded." : " has it.");
  return seen + " Not checked: whether the goblin's own session can use it, or which account and project it reaches.";
}

export function connectionAction(action: string, name: string): string {
  if (action.startsWith("store:")) return "Store " + action.slice(6) + " from clipboard";
  if (action.startsWith("replace:")) return "Replace " + action.slice(8) + " from clipboard";
  return (action === "cli" ? "Open CLI sign-in for " : "Sign in to ") + name;
}
