import { parseBrowserLogins, type BrowserLogin } from "./browser-logins.ts";
import { array, object, string, strings } from "./types.ts";

// What GET /api/connectors answers: the saved snapshot of three levels, read
// at once with nothing checked to answer it. The machine is checked once, each
// project once however many goblins it has, and each goblin is read from its
// own session record. Names and states only, never a credential's value.

export type ConnectorLevel = "pc" | "account" | "project" | "goblin";

export interface ConnectorCredential { name: string; isResolved: boolean }

// One row of a level, as the supervisor saved it.
export interface ConnectorItem {
  id: string; level: ConnectorLevel; group: string; kind: string; name: string; status: string;
  detail: string; target: string; source: string; credentials: ConnectorCredential[]; tools: string[];
  actions: string[]; grant: "grant" | "revoke" | ""; checkedAt: string;
}

// What a check of one level left: its rows, when it ended (Go's zero time until
// the first does), whether another is running and why the last failed.
export interface ConnectorLevelResult { items: ConnectorItem[]; checkedAt: string; isChecking: boolean; error: string }

export interface ConnectorProject extends ConnectorLevelResult { name: string; path: string; browserLogins: BrowserLogin[] }

export interface ConnectorGoblin extends ConnectorLevelResult {
  task: string; generation: string; project: string; harness: string; isConfirmed: boolean; note: string;
}

export interface ConnectorSnapshot { machine: ConnectorLevelResult; projects: ConnectorProject[]; goblins: ConnectorGoblin[] }

const LEVELS: readonly string[] = ["pc", "account", "project", "goblin"];

function parseLevel(value: unknown): ConnectorLevel {
  const level = string(value);
  if (!LEVELS.includes(level)) throw new Error("Invalid connector level");
  return level as ConnectorLevel;
}

function parseItem(value: unknown): ConnectorItem {
  const item = object(value);
  const grant = string(item.grant);
  return {
    id: string(item.id), level: parseLevel(item.level), group: string(item.group), kind: string(item.kind), name: string(item.name), status: string(item.status),
    detail: string(item.detail), target: string(item.target), source: string(item.source), tools: strings(item.tools), actions: strings(item.actions),
    credentials: array(item.credentials).map((each) => {
      const credential = object(each);
      return { name: string(credential.name), isResolved: credential.is_resolved === true };
    }),
    grant: grant === "grant" || grant === "revoke" ? grant : "", checkedAt: string(item.checked_at),
  };
}

function parseLevelResult(value: unknown): ConnectorLevelResult {
  const level = object(value);
  return { items: array(level.items).map(parseItem), checkedAt: string(level.checked_at), isChecking: level.checking === true, error: string(level.error) };
}

export function parseConnectorSnapshot(value: unknown): ConnectorSnapshot {
  const snapshot = object(value);
  return {
    machine: parseLevelResult(snapshot.machine),
    projects: array(snapshot.projects).map((each) => {
      const project = object(each);
      return { ...parseLevelResult(project), name: string(project.name), path: string(project.path), browserLogins: parseBrowserLogins(project.browser_logins) };
    }),
    goblins: array(snapshot.goblins).map((each) => {
      const goblin = object(each);
      return {
        ...parseLevelResult(goblin), task: string(goblin.task), generation: string(goblin.generation), project: string(goblin.project), harness: string(goblin.harness),
        isConfirmed: goblin.is_confirmed === true, note: string(goblin.note),
      };
    }),
  };
}

// Whether any level is being checked, which the page reads again until none is.
export function isAnyChecking(snapshot: ConnectorSnapshot): boolean {
  return snapshot.machine.isChecking || snapshot.projects.some((project) => project.isChecking) || snapshot.goblins.some((goblin) => goblin.isChecking);
}
