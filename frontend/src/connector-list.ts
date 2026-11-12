import { connectionEvidence, connectionStatus, connectionTrouble, type ConnectionEntry } from "./connections.ts";
import { connectorName, toolWords } from "./connector-names.ts";
import { listNames } from "./credentials.ts";

// The Connectors page lists each connector once, whichever goblins report
// it (#2303): the supervisor reads connections one goblin at a time, and
// what each goblin said of a connector is one of its uses.

// What one goblin's check found, by that goblin's key.
export interface GoblinReading { goblin: string; entries: ConnectionEntry[] }

// One goblin's use of a connector: what its check said of it, and whether
// the goblin carries it, which a Connect or Disconnect he just pressed
// decides until a finished check shows it.
export interface ConnectorUse { goblin: string; entry: ConnectionEntry; isCarried: boolean }

// A state's tone: verified, the only green, only when a check confirmed the
// access; trouble, the only amber, where he must act or a check failed; and
// quiet for every other state (the Overlord, 2026-11-11: "clean up the sign
// in and amber alerts to make it much more simple and user friendly
// especially with verification and difference between mcp and env cli
// variables", #2358).
export type ConnectorTone = "verified" | "trouble" | "quiet";

// A connector is listed in a tone once a goblin carries it, as sign-in when
// the goblin whose check says least must sign in and can, since where he
// must act is a button in the board's look and not coloured words (the
// Overlord, 2026-11-11), and is available when none carries it.
export type ConnectorStatus = ConnectorTone | "sign-in" | "available";

export interface Connector {
  key: string;
  // Its name as it is shown, and as its goblins report it, which finds its
  // logo and what it does.
  name: string;
  reported: string;
  kind: string;
  type: string;
  uses: ConnectorUse[];
  status: ConnectorStatus;
  // Its state in one word, as the goblin whose check says least has it.
  word: string;
  // Whether a Connect or Disconnect waits for a goblin's next terminal.
  isWaiting: boolean;
}

// goblinKey names a goblin's running session: its task and its generation.
export const goblinKey = (task: { id: string; generation: string }): string => task.id + "\n" + task.generation;

// askKey names what he asked of one goblin's connector.
export const askKey = (goblin: string, entry: string): string => goblin + "\n" + entry;

// How a connector reaches a goblin, for the list's Type column. A harness's
// prefix on a server's name says where it is from, which the name no longer
// shows.
function connectorType(entry: ConnectionEntry): string {
  if (entry.kind === "service") return "Repository service";
  if (entry.kind === "credential") return "CLI variable";
  if (entry.kind === "connector") return "Built in";
  if (/^claude[._ ]ai[._ ]/i.test(entry.name)) return "Claude MCP connector";
  if (/^plugin[_:]/i.test(entry.name)) return "Plugin MCP server";
  return "MCP server";
}

// entryState is what one goblin's check of a connector says, in a word and
// its tone. A credential or a service whose credentials resolve and that no
// check confirmed is Set, and the crawler on a goblin's PATH is Installed:
// neither is Verified.
export function entryState(entry: ConnectionEntry): { tone: ConnectorTone; word: string } {
  if (entry.status === "connected" || entry.status === "probe_succeeded") return { tone: "verified", word: "Verified" };
  if (entry.status === "provided") return { tone: "quiet", word: entry.kind === "connector" ? "Installed" : "Set" };
  if (entry.status === "withheld" || entry.status === "checking") return { tone: "quiet", word: "Checking" };
  return { tone: connectionTrouble(entry) ? "trouble" : "quiet", word: connectionStatus(entry.status) };
}

// The quiet states in the order a row prefers to show them, the one that says
// least first: a row shows its goblins' least, so it never reads better than
// any one of their checks.
const QUIET = ["checking", "withheld", "unchecked", "disabled", "skipped", "provided"];
const rank = (entry: ConnectionEntry): number => {
  const { tone } = entryState(entry);
  return tone === "trouble" ? -1 : tone === "verified" ? QUIET.length : QUIET.indexOf(entry.status);
};

// Whether a goblin's check asks for a sign-in he can start from the board. A
// repair that stores a variable from the clipboard is no sign-in.
const isSignIn = (entry: ConnectionEntry): boolean => entry.status === "unauthorized" && entry.actions.some((action) => !action.startsWith("store:"));

// Whether the goblin carries the connector: what he last asked, else what
// the check found. A connector with nothing to give or take back is carried
// unless the check says it is withheld.
function isCarried(entry: ConnectionEntry, asked: boolean | undefined): boolean {
  return asked ?? (entry.grant ? entry.grant === "revoke" : entry.status !== "withheld");
}

// listConnectors merges every goblin's entries into the connectors the page
// lists, by name within each kind, in the order of their names. A variable
// no goblin carries is left out: it is its service's, which offers it.
export function listConnectors(readings: GoblinReading[], asked: ReadonlyMap<string, boolean> = new Map()): Connector[] {
  const found = new Map<string, Connector>();
  for (const { goblin, entries } of readings) {
    for (const entry of entries) {
      const key = entry.kind + ":" + entry.name.toLowerCase();
      const connector = found.get(key) ?? {
        key, name: entry.kind === "credential" ? entry.name : connectorName(entry.name), reported: entry.name, kind: entry.kind, type: connectorType(entry),
        uses: [], status: "available" as ConnectorStatus, word: "", isWaiting: false,
      };
      connector.uses.push({ goblin, entry, isCarried: isCarried(entry, asked.get(askKey(goblin, entry.id))) });
      found.set(key, connector);
    }
  }
  const connectors = [...found.values()].filter((connector) => connector.kind !== "credential" || connector.uses.some((use) => use.isCarried));
  for (const connector of connectors) {
    connector.isWaiting = connector.uses.some((use) => use.entry.isRestartRequired);
    const carried = connector.uses.filter((use) => use.isCarried);
    if (!carried.length) continue;
    const least = carried.reduce((found, use) => rank(use.entry) < rank(found.entry) ? use : found);
    const { tone, word } = entryState(least.entry);
    connector.status = isSignIn(least.entry) ? "sign-in" : tone;
    connector.word = word;
  }
  return connectors.sort((first, second) => first.name.localeCompare(second.name) || first.type.localeCompare(second.type));
}

// matchesConnector says whether a search finds the connector: by its name
// as shown or as reported, or by its type, without case.
export function matchesConnector(connector: Connector, search: string): boolean {
  const wanted = search.trim().toLowerCase();
  return !wanted || [connector.name, connector.reported, connector.type].some((text) => text.toLowerCase().includes(wanted));
}

// usageState is what one goblin's use of a connector says under the goblin's
// name on the connector's page: Not connected, or its state in a word and
// its tone, with the reason the check gave. Verified is its word alone, and a
// sign-in he can start is its reason and its button, with no word.
export function usageState(use: ConnectorUse): { tone: ConnectorTone; word: string; reason: string } {
  if (!use.isCarried) return { tone: "quiet", word: "Not connected", reason: "" };
  const { entry } = use;
  const { tone, word } = entryState(entry);
  if (tone === "verified" || word === "Checking") return { tone, word, reason: "" };
  return { tone, word: isSignIn(entry) ? "" : word, reason: entry.detail };
}

// checkEvidence is what a goblin's check did and did not prove, for the
// connector page's check details, when the goblin carries the connector and
// its check found it Verified, or Set with no reason of its own.
export function checkEvidence(use: ConnectorUse): string {
  const { tone } = entryState(use.entry);
  const isEvidence = tone === "verified" || (use.entry.status === "provided" && !use.entry.detail);
  return use.isCarried && isEvidence ? connectionEvidence(use.entry) : "";
}

// The words for what a goblin's terminal holds of a CLI variable.
function variableWord(entry: ConnectionEntry): string {
  return ({ provided: "Set", missing: "Not set", withheld: "Not given", unchecked: "Not checked" } as Record<string, string>)[entry.status] || connectionStatus(entry.status);
}

export interface CliVariable { name: string; states: { goblin: string; word: string }[] }

// cliVariables are the CLI variables a service brings, the ones the auth
// manifest declares for it, shared with another service or not, each by name
// with what every goblin's terminal holds of it, in the order of their
// names. Never a value.
export function cliVariables(connector: Connector, readings: GoblinReading[]): CliVariable[] {
  if (connector.kind !== "service") return [];
  const found = new Map<string, CliVariable>();
  for (const { goblin, entries } of readings) {
    for (const entry of entries) {
      if (entry.kind !== "credential" || !entry.accounts.includes(connector.reported)) continue;
      const variable = found.get(entry.name) ?? { name: entry.name, states: [] };
      variable.states.push({ goblin, word: variableWord(entry) });
      found.set(entry.name, variable);
    }
  }
  return [...found.values()].sort((first, second) => first.name.localeCompare(second.name));
}

// declaredBy is what a CLI variable's page says of where it is from: each
// repository service that declares it, by name, or that none does.
export function declaredBy(connector: Connector): string {
  const services = [...new Set(connector.uses.flatMap((use) => use.entry.accounts))].map(connectorName);
  if (!services.length) return "No repository service declares it.";
  return "Declared by the " + listNames(services) + (services.length > 1 ? " repository services." : " repository service.");
}

// A connector's tools in two groups, as its harness marks them: those that
// only read, and those that can write. Each is named in words, in the order
// of those words, and a tool two goblins list is listed once.
export function toolGroups(connector: Connector): { readOnly: string[]; write: string[] } {
  const tools = new Map<string, boolean>();
  for (const use of connector.uses) for (const tool of use.entry.tools) tools.set(tool.name, tool.isReadOnly);
  const named = (isReadOnly: boolean) => [...tools].filter(([, marked]) => marked === isReadOnly).map(([name]) => toolWords(name, connector.name)).sort((first, second) => first.localeCompare(second));
  return { readOnly: named(true), write: named(false) };
}
