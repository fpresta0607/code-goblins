import { countWords, goblinsOf, groupTitle, isUnchecked, PC_GROUPS, projectsInOrder, troubledCount, type ConnectorRow } from "./connector-rows.ts";
import type { ConnectorSnapshot } from "./connector-snapshot.ts";
import { scopeOf } from "./connector-status.ts";

// What the Connectors page's tree and its scopes' pages say (#3010), from the
// widest scope down: This PC, which every goblin gets; each project, one git
// repository, with its running goblins under it; and the Overlord's own Claude
// and Codex, which goblins start without.

// The tree lists every project with a goblin running, and at least this many.
const TREE_PROJECTS = 4;
// A project's page shows every service that needs a look, and at least this many.
const SHOWN_SERVICES = 6;

export function listWords(parts: string[]): string {
  return parts.length < 2 ? parts.join("") : parts.slice(0, -1).join(", ") + " and " + parts[parts.length - 1];
}

// This PC's groups, each with how many it holds, and how many it holds in all.
export function pcScope(snapshot: ConnectorSnapshot): { total: number; words: string; groups: { group: string; title: string; count: number }[] } {
  const global = snapshot.machine.items.filter((item) => scopeOf(item) === "global");
  const count = (group: string) => global.filter((item) => item.group === group).length;
  return {
    total: global.length,
    words: listWords([countWords(count("cli"), "tool"), countWords(count("harness"), "harness sign-in"), countWords(count("environment"), "credential"), countWords(count("built_in"), "built-in")]),
    groups: PC_GROUPS.map((group) => ({ group, title: groupTitle(group), count: count(group) })),
  };
}

export const yoursCount = (snapshot: ConnectorSnapshot): number => snapshot.machine.items.filter((item) => scopeOf(item) === "yours").length;

export interface TreeGoblin { task: string; name: string; carries: number; isTroubled: boolean }
export interface TreeProject { name: string; about: string; isTroubled: boolean; goblins: TreeGoblin[] }

// The projects the tree lists, those with a goblin first, and how many more
// the Projects page lists.
export function treeProjects(snapshot: ConnectorSnapshot, nameOf: (task: string) => string): { shown: TreeProject[]; more: number } {
  const ordered = projectsInOrder(snapshot).map((project): TreeProject => {
    const count = (group: string) => project.items.filter((item) => item.group === group).length;
    const servers = count("mcp");
    return {
      name: project.name, isTroubled: troubledCount(project) > 0,
      about: isUnchecked(project) ? "not checked yet" : count("service") + " private" + (servers ? " · " + servers + " in repo" : ""),
      goblins: goblinsOf(snapshot, project.name).map((goblin) => ({
        task: goblin.task, name: nameOf(goblin.task), carries: goblin.items.filter((item) => item.group === "carried").length, isTroubled: troubledCount(goblin) > 0,
      })),
    };
  });
  const shown = Math.max(TREE_PROJECTS, ordered.filter((project) => project.goblins.length > 0).length);
  return { shown: ordered.slice(0, shown), more: Math.max(0, ordered.length - shown) };
}

const isTrouble = (row: ConnectorRow) => row.tone === "trouble";
export const troubleFirst = (rows: ConnectorRow[]): ConnectorRow[] => [...rows.filter(isTrouble), ...rows.filter((row) => !isTrouble(row))];

export function carriersWords(names: string[]): string {
  if (names.length === 0) return "no goblin carries it";
  return names.join(", ") + (names.length === 1 ? " carries it" : " carry it");
}

const withAbout = (row: ConnectorRow, words: string[]): ConnectorRow => ({ ...row, about: [row.about, ...words].filter(Boolean).join(" · ") });

// A project's two places: the MCP servers its repository's .mcp.json declares,
// and the services Code Goblins keeps private to this PC in its auth.json,
// trouble first, the quiet services past the first few folded. Each service
// says which of the project's goblins carry it, and an item declared in both
// places says so.
export function projectScope(snapshot: ConnectorSnapshot, rows: ConnectorRow[], name: string, nameOf: (task: string) => string): { servers: ConnectorRow[]; services: ConnectorRow[]; folded: ConnectorRow[] } {
  const own = rows.filter((row) => row.level === "project" && row.project === name);
  const servers = own.filter((row) => row.group === "mcp"), services = own.filter((row) => row.group === "service");
  const isIn = (list: ConnectorRow[], row: ConnectorRow) => list.some((other) => other.logoName.toLowerCase() === row.logoName.toLowerCase());
  const goblins = goblinsOf(snapshot, name);
  const carriers = (row: ConnectorRow) => goblins.filter((goblin) => goblin.items.some((item) => item.group === "carried" && item.name === row.logoName)).map((goblin) => nameOf(goblin.task));
  const listed = troubleFirst(services).map((row) => withAbout(row, [carriersWords(carriers(row)), isIn(servers, row) ? "also a server above" : ""]));
  const shown = Math.max(SHOWN_SERVICES, services.filter(isTrouble).length);
  return {
    servers: troubleFirst(servers).map((row) => withAbout(row, [isIn(services, row) ? "also a service below" : ""])),
    services: listed.slice(0, shown), folded: listed.slice(shown),
  };
}

// A goblin's rows from the widest scope down: the project's services it is
// given and those it is not, and what its own session loaded, failed to load
// or is starting, trouble first.
export function goblinScope(rows: ConnectorRow[], task: string): { granted: ConnectorRow[]; notGiven: ConnectorRow[]; session: ConnectorRow[] } {
  const own = rows.filter((row) => row.level === "goblin" && row.task === task);
  return {
    granted: own.filter((row) => row.group === "carried"), notGiven: own.filter((row) => row.group === "not_given"),
    session: troubleFirst(own.filter((row) => row.group !== "carried" && row.group !== "not_given")),
  };
}
