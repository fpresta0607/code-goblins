import { connectorName } from "./connector-names.ts";
import type { Place } from "./connector-place.ts";
import type { ConnectorCredential, ConnectorGoblin, ConnectorItem, ConnectorLevel, ConnectorLevelResult, ConnectorProject, ConnectorSnapshot } from "./connector-snapshot.ts";
import { hasChecked, LEVEL_WORDS, scopeOf, statusTone, statusWord, type ConnectorScope, type ConnectorTone } from "./connector-status.ts";

// What a refresh asks the supervisor to check again: the machine, one project
// or one goblin.
export interface RefreshTarget { level: "machine" | "project" | "goblin"; scope: string }

// One row the page draws, whichever level it comes from. It carries its level,
// and for a row of a project or a goblin the project and the goblin it is
// of, so two projects' same-named services stay two rows, each with its own
// project and target.
export interface ConnectorRow {
  key: string;
  level: ConnectorLevel;
  scope: ConnectorScope;
  group: string;
  name: string;
  // The name a logo is found by, empty for a project or a goblin.
  logoName: string;
  // Where it is from, for a row listed outside its own place: its project, or its goblin in its project.
  origin: string;
  target: string;
  about: string;
  word: string;
  tone: ConnectorTone;
  detail: string;
  checkedAt: string;
  isChecking: boolean;
  credentials: ConnectorCredential[];
  tools: string[];
  // The repairs a project's item offers, and whether a goblin's item can be given or taken back.
  actions: string[];
  grant: "grant" | "revoke" | "";
  connection: string;
  project: string;
  task: string;
  refresh?: RefreshTarget;
  // The place it opens.
  place: Place;
  haystack: string;
}

const GROUP_TITLES: Record<string, string> = {
  cli: "Command-line tools", environment: "User environment credentials", built_in: "Built in to every goblin",
  harness: "Harness sign-ins", account_connector: "Comes with your Claude account", user_config: "In your Claude and Codex user config",
  service: "Services", mcp: "MCP servers",
  failed: "Failed to load", loaded: "Loaded by its session", carried: "Carries", not_given: "Not given",
};

const GROUP_WHY: Record<string, string> = {
  account_connector: "Your own Claude sessions load these. Goblins do not: each starts with a strict MCP config.",
  user_config: "Servers your own sessions load. Each row says whether goblins get it.",
  environment: "Credential names in this PC's user environment, read as names only.",
  service: "From the project's manifest. Each credential is named, never shown.",
  mcp: "From the project's .mcp.json, with what each points at.",
};

// The groups of This PC and of Your Claude and Codex, in the order each is read in.
export const PC_GROUPS = ["cli", "harness", "environment", "built_in"];
export const YOURS_GROUPS = ["account_connector", "user_config"];

const LEVEL_ORDER: ConnectorLevel[] = ["pc", "account", "project", "goblin"];

const SHOWN_AS_CONNECTOR = new Set(["mcp", "service", "connector"]);

export const groupTitle = (group: string): string => GROUP_TITLES[group] ?? group;
export const groupWhy = (group: string): string => GROUP_WHY[group] ?? "";

export function countWords(count: number, singular: string, plural = singular + "s"): string {
  return count + " " + (count === 1 ? singular : plural);
}

interface RowContext { level: ConnectorLevelResult; origin: string; project: string; task: string; refresh?: RefreshTarget; place: Place }

function itemRow(item: ConnectorItem, context: RowContext): ConnectorRow {
  const name = SHOWN_AS_CONNECTOR.has(item.kind) ? connectorName(item.name) : item.name;
  const word = statusWord(item);
  return {
    key: [item.level, context.task || context.project, item.id].join("|"), level: item.level, scope: scopeOf(item), group: item.group, name, logoName: item.name,
    origin: context.origin, target: item.target, about: item.source ? "read from " + item.source : "", word, tone: statusTone(item.status), detail: item.detail,
    checkedAt: hasChecked(item.checkedAt) ? item.checkedAt : context.level.checkedAt, isChecking: context.level.isChecking,
    credentials: item.credentials, tools: item.tools, actions: item.actions, grant: item.grant, connection: item.id, project: context.project, task: context.task,
    refresh: context.refresh, place: context.place,
    haystack: [name, item.name, LEVEL_WORDS[item.level], word, groupTitle(item.group), context.origin, item.target, item.source, item.detail, ...item.credentials.map((credential) => credential.name), ...item.tools].join("\n").toLowerCase(),
  };
}

// Every item of the snapshot, a row each: the machine's, then each project's,
// then each goblin's. nameOf is how a goblin is called by its task.
export function allRows(snapshot: ConnectorSnapshot, nameOf: (task: string) => string): ConnectorRow[] {
  const machine = snapshot.machine.items.map((item) => itemRow(item, { level: snapshot.machine, origin: "", project: "", task: "", refresh: { level: "machine", scope: "" }, place: { kind: "pc" } }));
  const projects = snapshot.projects.flatMap((project) => project.items.map((item) => itemRow(item, {
    level: project, origin: project.name, project: project.name, task: "", refresh: { level: "project", scope: project.name }, place: { kind: "project", project: project.name },
  })));
  const goblins = snapshot.goblins.flatMap((goblin) => goblin.items.map((item) => itemRow(item, {
    level: goblin, origin: nameOf(goblin.task) + " in " + goblin.project, project: goblin.project, task: goblin.task, place: { kind: "goblin", task: goblin.task },
  })));
  return [...machine, ...projects, ...goblins];
}

export const needsLook = (rows: ConnectorRow[]): ConnectorRow[] => rows.filter((row) => row.tone === "trouble");

// Every row that holds all the words of a search, in any order and case.
export function searchRows(rows: ConnectorRow[], search: string): ConnectorRow[] {
  const words = search.toLowerCase().split(/\s+/).filter(Boolean);
  return words.length ? rows.filter((row) => words.every((word) => row.haystack.includes(word))) : [];
}

export function groupByLevel(rows: ConnectorRow[]): { level: ConnectorLevel; rows: ConnectorRow[] }[] {
  return LEVEL_ORDER.map((level) => ({ level, rows: rows.filter((row) => row.level === level) })).filter((each) => each.rows.length > 0);
}

// Rows in their groups, in the order given, any group the order does not name
// last.
export function groupByGroup(rows: ConnectorRow[], known: string[]): { group: string; title: string; why: string; rows: ConnectorRow[] }[] {
  const found = [...new Set(rows.map((row) => row.group))];
  const groups = [...known.filter((group) => found.includes(group)), ...found.filter((group) => !known.includes(group))];
  return groups.map((group) => ({ group, title: groupTitle(group), why: groupWhy(group), rows: rows.filter((row) => row.group === group) }));
}

function placeRow(level: ConnectorLevel, fields: { key: string; name: string; about: string; place: Place; result: ConnectorLevelResult; refresh?: RefreshTarget; troubled: number; quietWord: string }): ConnectorRow {
  const { result, troubled } = fields;
  return {
    key: fields.key, level, scope: scopeOf({ level, group: "" }), group: "", name: fields.name, logoName: "", origin: "", target: "", about: fields.about,
    word: !hasChecked(result.checkedAt) ? "Not checked yet" : troubled ? troubled + " to look at" : fields.quietWord,
    tone: troubled ? "trouble" : "quiet", detail: result.error, checkedAt: result.checkedAt, isChecking: result.isChecking,
    credentials: [], tools: [], actions: [], grant: "", connection: "", project: "", task: "", refresh: fields.refresh, place: fields.place,
    haystack: [fields.name, fields.about].join("\n").toLowerCase(),
  };
}

export const troubledCount = (result: ConnectorLevelResult): number => result.items.filter((item) => statusTone(item.status) === "trouble").length;

// The goblins of a project, in the order the supervisor listed them.
export const goblinsOf = (snapshot: ConnectorSnapshot, project: string): ConnectorGoblin[] => snapshot.goblins.filter((goblin) => goblin.project === project);

// The projects, those with a goblin running first, each part in the order the
// supervisor listed them.
export function projectsInOrder(snapshot: ConnectorSnapshot): ConnectorProject[] {
  const isBusy = (project: ConnectorProject) => snapshot.goblins.some((goblin) => goblin.project === project.name);
  return [...snapshot.projects.filter(isBusy), ...snapshot.projects.filter((project) => !isBusy(project))];
}

export function projectRows(snapshot: ConnectorSnapshot): ConnectorRow[] {
  return projectsInOrder(snapshot).map((project) => {
    const services = project.items.filter((item) => item.group === "service").length, servers = project.items.filter((item) => item.group === "mcp").length;
    const goblins = goblinsOf(snapshot, project.name).length;
    const about = countWords(services, "service") + (servers ? " · " + countWords(servers, "MCP server") : "") + " · " + (goblins ? countWords(goblins, "goblin") : "no goblin running");
    return placeRow("project", { key: "project|" + project.name, name: project.name, about, place: { kind: "project", project: project.name }, result: project, refresh: { level: "project", scope: project.name }, troubled: troubledCount(project), quietWord: "Checked" });
  });
}

export function goblinRows(snapshot: ConnectorSnapshot, nameOf: (task: string) => string): ConnectorRow[] {
  return snapshot.goblins.map((goblin) => {
    const failed = goblin.items.filter((item) => item.group === "failed").length;
    const about = goblin.project + " · " + goblin.harness + (goblin.isConfirmed ? "" : " · not confirmed by its session");
    const row = placeRow("goblin", { key: "goblin|" + goblin.task, name: nameOf(goblin.task), about, place: { kind: "goblin", task: goblin.task }, result: goblin, troubled: failed, quietWord: "As its project" });
    return failed ? { ...row, word: countWords(failed, "server") + " failed to load" } : row;
  });
}

export function isUnchecked(result: ConnectorLevelResult): boolean {
  return !hasChecked(result.checkedAt) && result.items.length === 0;
}

// When the oldest check among every level ended, or nothing when none has.
export function oldestCheck(snapshot: ConnectorSnapshot): string {
  const times = [snapshot.machine, ...snapshot.projects, ...snapshot.goblins].map((level) => level.checkedAt).filter(hasChecked);
  return times.sort((first, second) => Date.parse(first) - Date.parse(second))[0] ?? "";
}
