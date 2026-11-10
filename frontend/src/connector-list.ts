import { connectionEvidence, connectionStatus, connectionTrouble, type ConnectionEntry } from "./connections.ts";
import { connectorName, toolWords } from "./connector-names.ts";

// The Connectors page lists each connector once, whichever goblins report
// it (#2303): the supervisor reads connections one goblin at a time, and
// what each goblin said of a connector is one of its uses.

// What one goblin's check found, by that goblin's key.
export interface GoblinReading { goblin: string; entries: ConnectionEntry[] }

// One goblin's use of a connector: what its check said of it, and whether
// the goblin carries it, which a Connect or Disconnect he just pressed
// decides until a finished check shows it.
export interface ConnectorUse { goblin: string; entry: ConnectionEntry; isCarried: boolean }

export type ConnectorStatus = "connected" | "trouble" | "checking" | "available";

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
  // What the first goblin it fails for says, in a word and in full.
  troubleWord: string;
  trouble: string;
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
  if (entry.kind === "credential") return "Credential";
  if (entry.kind === "connector") return "Built in";
  if (/^claude[._ ]ai[._ ]/i.test(entry.name)) return "Claude connector";
  if (/^plugin[_:]/i.test(entry.name)) return "Plugin";
  return "MCP server";
}

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
        uses: [], status: "available" as ConnectorStatus, troubleWord: "", trouble: "",
      };
      connector.uses.push({ goblin, entry, isCarried: isCarried(entry, asked.get(askKey(goblin, entry.id))) });
      found.set(key, connector);
    }
  }
  const connectors = [...found.values()].filter((connector) => connector.kind !== "credential" || connector.uses.some((use) => use.isCarried));
  for (const connector of connectors) {
    const carried = connector.uses.filter((use) => use.isCarried);
    const failing = carried.find((use) => connectionTrouble(use.entry));
    if (failing) {
      connector.status = "trouble";
      connector.troubleWord = connectionStatus(failing.entry.status);
      connector.trouble = connectionTrouble(failing.entry);
    } else if (carried.some((use) => use.entry.status === "checking" || use.entry.status === "withheld")) connector.status = "checking";
    else if (carried.length) connector.status = "connected";
  }
  return connectors.sort((first, second) => first.name.localeCompare(second.name) || first.type.localeCompare(second.type));
}

// matchesConnector says whether a search finds the connector: by its name
// as shown or as reported, or by its type, without case.
export function matchesConnector(connector: Connector, search: string): boolean {
  const wanted = search.trim().toLowerCase();
  return !wanted || [connector.name, connector.reported, connector.type].some((text) => text.toLowerCase().includes(wanted));
}

// usageWords is what one goblin's use of a connector says under the goblin's
// name: that it is not connected or is being checked, its trouble, what a
// separate probe of it does and does not prove, or the state its check found.
export function usageWords(use: ConnectorUse): string {
  if (!use.isCarried) return "Not connected";
  if (use.entry.status === "withheld" || use.entry.status === "checking") return "Checking";
  if (use.entry.status === "probe_succeeded") return connectionEvidence(use.entry);
  return connectionTrouble(use.entry) || connectionStatus(use.entry.status);
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
